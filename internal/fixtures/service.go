package fixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/detection"
)

var (
	ErrInvalidFixture   = errors.New("invalid fixture")
	ErrIdentityConflict = errors.New("source identities belong to different customers")
)

type Input struct {
	CustomerName             string     `json:"customer_name"`
	StripeCustomerID         string     `json:"stripe_customer_id"`
	HubSpotCompanyID         string     `json:"hubspot_company_id"`
	StripeSubscriptionStatus string     `json:"stripe_subscription_status"`
	HubSpotCustomerStatus    string     `json:"hubspot_customer_status"`
	ObservedAt               *time.Time `json:"observed_at,omitempty"`
}

type Result struct {
	CustomerID string  `json:"customer_id"`
	FindingID  *string `json:"finding_id"`
	Outcome    string  `json:"outcome"`
}

type Service struct {
	database *pgxpool.Pool
}

func NewService(database *pgxpool.Pool) *Service {
	return &Service{database: database}
}

// Ingest maps fixture identities, stores current normalized facts, evaluates
// one customer, and persists the result atomically.
func (s *Service) Ingest(ctx context.Context, identity auth.Identity, input Input) (Result, error) {
	input.CustomerName = strings.TrimSpace(input.CustomerName)
	input.StripeCustomerID = strings.TrimSpace(input.StripeCustomerID)
	input.HubSpotCompanyID = strings.TrimSpace(input.HubSpotCompanyID)
	input.StripeSubscriptionStatus = strings.ToLower(strings.TrimSpace(input.StripeSubscriptionStatus))
	input.HubSpotCustomerStatus = strings.ToLower(strings.TrimSpace(input.HubSpotCustomerStatus))
	if !validInput(input) {
		return Result{}, ErrInvalidFixture
	}

	observedAt := time.Now().UTC()
	if input.ObservedAt != nil {
		observedAt = input.ObservedAt.UTC()
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("start fixture ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	customerID, err := resolveCustomer(ctx, tx, identity.OrganizationID, input)
	if err != nil {
		return Result{}, err
	}

	if err := upsertFact(ctx, tx, identity.OrganizationID, customerID, "stripe", "stripe.subscription.status", input.StripeSubscriptionStatus, observedAt); err != nil {
		return Result{}, err
	}
	if err := upsertFact(ctx, tx, identity.OrganizationID, customerID, "hubspot", "hubspot.customer.status", input.HubSpotCustomerStatus, observedAt); err != nil {
		return Result{}, err
	}

	candidate := detection.EvaluateStatusMismatch(detection.CustomerSnapshot{
		CustomerName:             input.CustomerName,
		StripeSubscriptionStatus: input.StripeSubscriptionStatus,
		HubSpotCustomerStatus:    input.HubSpotCustomerStatus,
	})

	result := Result{CustomerID: customerID, Outcome: "no_mismatch"}
	if candidate == nil {
		if err := resolveOpenFinding(ctx, tx, identity.OrganizationID, customerID, observedAt); err != nil {
			return Result{}, err
		}
	} else {
		findingID, err := saveFinding(ctx, tx, identity.OrganizationID, customerID, *candidate, input, observedAt)
		if err != nil {
			return Result{}, err
		}
		result.FindingID = &findingID
		result.Outcome = "finding_open"
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, user_id, action, target_type, target_id)
		VALUES ($1, $2, 'fixture.ingested', 'canonical_customer', $3)
	`, identity.OrganizationID, identity.UserID, customerID); err != nil {
		return Result{}, fmt.Errorf("audit fixture ingestion: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("finish fixture ingestion: %w", err)
	}
	return result, nil
}

func resolveCustomer(ctx context.Context, tx pgx.Tx, organizationID string, input Input) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT customer_id
		FROM customer_identities
		WHERE organization_id = $1
		  AND ((source = 'stripe' AND external_id = $2)
		    OR (source = 'hubspot' AND external_id = $3))
	`, organizationID, input.StripeCustomerID, input.HubSpotCompanyID)
	if err != nil {
		return "", fmt.Errorf("find customer identities: %w", err)
	}

	customerIDs := make(map[string]bool)
	for rows.Next() {
		var customerID string
		if err := rows.Scan(&customerID); err != nil {
			rows.Close()
			return "", fmt.Errorf("read customer identity: %w", err)
		}
		customerIDs[customerID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("list customer identities: %w", err)
	}
	if len(customerIDs) > 1 {
		return "", ErrIdentityConflict
	}

	var customerID string
	for existingID := range customerIDs {
		customerID = existingID
	}
	if customerID == "" {
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_customers (organization_id, name)
			VALUES ($1, $2)
			RETURNING id
		`, organizationID, input.CustomerName).Scan(&customerID); err != nil {
			return "", fmt.Errorf("create canonical customer: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE canonical_customers SET name = $1, updated_at = now()
			WHERE organization_id = $2 AND id = $3
		`, input.CustomerName, organizationID, customerID); err != nil {
			return "", fmt.Errorf("update canonical customer: %w", err)
		}
	}

	for _, sourceIdentity := range []struct {
		source     string
		externalID string
	}{
		{source: "stripe", externalID: input.StripeCustomerID},
		{source: "hubspot", externalID: input.HubSpotCompanyID},
	} {
		_, err := tx.Exec(ctx, `
			INSERT INTO customer_identities (organization_id, customer_id, source, external_id, match_method)
			VALUES ($1, $2, $3, $4, 'explicit')
			ON CONFLICT (organization_id, source, external_id) DO NOTHING
		`, organizationID, customerID, sourceIdentity.source, sourceIdentity.externalID)
		if err != nil {
			return "", fmt.Errorf("store customer identity: %w", err)
		}
	}
	return customerID, nil
}

func upsertFact(ctx context.Context, tx pgx.Tx, organizationID, customerID, source, factType, value string, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_facts (
			organization_id, customer_id, source, fact_type, value, observed_at, schema_version
		)
		VALUES ($1, $2, $3, $4, to_jsonb($5::text), $6, 1)
		ON CONFLICT (organization_id, customer_id, source, fact_type)
		DO UPDATE SET
			value = EXCLUDED.value,
			observed_at = EXCLUDED.observed_at,
			schema_version = EXCLUDED.schema_version,
			updated_at = now()
	`, organizationID, customerID, source, factType, value, observedAt)
	if err != nil {
		return fmt.Errorf("store %s fact: %w", source, err)
	}
	return nil
}

func saveFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID string, candidate detection.CandidateFinding, input Input, observedAt time.Time) (string, error) {
	fingerprint := hashParts(organizationID, customerID, candidate.RuleName, fmt.Sprint(candidate.RuleVersion))
	var findingID string
	err := tx.QueryRow(ctx, `
		INSERT INTO findings (
			organization_id, customer_id, rule_name, rule_version, fingerprint,
			status, risk, title, explanation, first_detected_at, last_detected_at
		)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $8, $9, $9)
		ON CONFLICT (organization_id, fingerprint)
		DO UPDATE SET
			status = 'open',
			risk = EXCLUDED.risk,
			title = EXCLUDED.title,
			explanation = EXCLUDED.explanation,
			last_detected_at = EXCLUDED.last_detected_at,
			resolved_at = NULL,
			updated_at = now()
		RETURNING id
	`, organizationID, customerID, candidate.RuleName, candidate.RuleVersion, fingerprint, candidate.Risk, candidate.Title, candidate.Explanation, observedAt).Scan(&findingID)
	if err != nil {
		return "", fmt.Errorf("store finding: %w", err)
	}

	evidence := []struct {
		source   string
		factType string
		value    string
	}{
		{source: "stripe", factType: "stripe.subscription.status", value: input.StripeSubscriptionStatus},
		{source: "hubspot", factType: "hubspot.customer.status", value: input.HubSpotCustomerStatus},
	}
	for _, item := range evidence {
		evidenceFingerprint := hashParts(item.source, item.factType, item.value, observedAt.Format(time.RFC3339Nano))
		_, err := tx.Exec(ctx, `
			INSERT INTO finding_evidence (
				organization_id, finding_id, source, fact_type, value, observed_at, fingerprint
			)
			VALUES ($1, $2, $3, $4, to_jsonb($5::text), $6, $7)
			ON CONFLICT (organization_id, finding_id, fingerprint) DO NOTHING
		`, organizationID, findingID, item.source, item.factType, item.value, observedAt, evidenceFingerprint)
		if err != nil {
			return "", fmt.Errorf("store finding evidence: %w", err)
		}
	}
	return findingID, nil
}

func resolveOpenFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID string, resolvedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE findings
		SET status = 'resolved', resolved_at = $1, updated_at = now()
		WHERE organization_id = $2
		  AND customer_id = $3
		  AND rule_name = $4
		  AND status = 'open'
	`, resolvedAt, organizationID, customerID, detection.StatusMismatchRuleName)
	if err != nil {
		return fmt.Errorf("resolve finding: %w", err)
	}
	return nil
}

func validInput(input Input) bool {
	if input.CustomerName == "" || len(input.CustomerName) > 200 || input.StripeCustomerID == "" || input.HubSpotCompanyID == "" {
		return false
	}
	validStripeStatuses := map[string]bool{"active": true, "trialing": true, "past_due": true, "unpaid": true, "canceled": true, "cancelled": true, "ended": true}
	validHubSpotStatuses := map[string]bool{"active": true, "inactive": true, "churned": true, "unknown": true}
	return validStripeStatuses[input.StripeSubscriptionStatus] && validHubSpotStatuses[input.HubSpotCustomerStatus]
}

func hashParts(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}
