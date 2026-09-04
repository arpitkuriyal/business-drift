package hubspotintegration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arpitkuriyal/business-drift/internal/detection"
)

func (s *Service) storeCompany(ctx context.Context, organizationID string, company companyRecord) (bool, bool, error) {
	if company.ID == "" {
		return false, false, errors.New("HubSpot company has no ID")
	}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	customerID, matched, err := resolveCustomer(ctx, tx, organizationID, company)
	if err != nil {
		return false, false, err
	}
	if err := saveHubSpotFact(ctx, tx, organizationID, customerID, "hubspot.company.domain", company.Domain, company.ObservedAt); err != nil {
		return false, false, err
	}
	if err := saveHubSpotFact(ctx, tx, organizationID, customerID, "hubspot.customer.status", company.Status, company.ObservedAt); err != nil {
		return false, false, err
	}
	finding, err := compareStatus(ctx, tx, organizationID, customerID, company)
	if err != nil {
		return false, false, err
	}
	return matched, finding, tx.Commit(ctx)
}

func resolveCustomer(ctx context.Context, tx pgx.Tx, organizationID string, company companyRecord) (string, bool, error) {
	var customerID string
	err := tx.QueryRow(ctx, `
		SELECT customer_id FROM customer_identities
		WHERE organization_id = $1 AND source = 'hubspot' AND external_id = $2
	`, organizationID, company.ID).Scan(&customerID)
	if err == nil {
		return customerID, hasStripe(ctx, tx, organizationID, customerID), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}

	matched := false
	if company.Domain != "" {
		rows, err := tx.Query(ctx, `
			SELECT DISTINCT facts.customer_id FROM customer_facts facts
			WHERE facts.organization_id = $1 AND facts.source = 'stripe'
			  AND facts.fact_type = 'stripe.customer.domain' AND facts.value = to_jsonb($2::text)
			  AND NOT EXISTS (
				SELECT 1 FROM customer_identities identity
				WHERE identity.organization_id = facts.organization_id
				  AND identity.customer_id = facts.customer_id AND identity.source = 'hubspot'
			  )
			LIMIT 2
		`, organizationID, company.Domain)
		if err != nil {
			return "", false, err
		}
		var matches []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", false, err
			}
			matches = append(matches, id)
		}
		rows.Close()
		if len(matches) == 1 {
			customerID, matched = matches[0], true
		}
	}

	name := firstNonEmpty(company.Name, company.Domain, company.ID)
	if customerID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO canonical_customers (organization_id, name) VALUES ($1, $2) RETURNING id`, organizationID, name).Scan(&customerID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE canonical_customers SET name = $1, updated_at = now() WHERE id = $2`, name, customerID)
	}
	if err != nil {
		return "", false, err
	}
	method := "external_id"
	if matched {
		method = "domain"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO customer_identities (organization_id, customer_id, source, external_id, match_method)
		VALUES ($1, $2, 'hubspot', $3, $4)
	`, organizationID, customerID, company.ID, method)
	return customerID, matched, err
}

func hasStripe(ctx context.Context, tx pgx.Tx, organizationID, customerID string) bool {
	var exists bool
	_ = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM customer_identities
		WHERE organization_id = $1 AND customer_id = $2 AND source = 'stripe')
	`, organizationID, customerID).Scan(&exists)
	return exists
}

func compareStatus(ctx context.Context, tx pgx.Tx, organizationID, customerID string, company companyRecord) (bool, error) {
	var stripeStatus string
	var stripeObservedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT value #>> '{}', observed_at FROM customer_facts
		WHERE organization_id = $1 AND customer_id = $2
		  AND source = 'stripe' AND fact_type = 'stripe.subscription.status'
	`, organizationID, customerID).Scan(&stripeStatus, &stripeObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	name := firstNonEmpty(company.Name, company.Domain, company.ID)
	candidate := detection.EvaluateStatusMismatch(detection.CustomerSnapshot{
		CustomerName: name, StripeSubscriptionStatus: stripeStatus, HubSpotCustomerStatus: company.Status,
	})
	fingerprint := hashParts(organizationID, customerID, detection.StatusMismatchRuleName)
	if candidate == nil {
		_, err := tx.Exec(ctx, `
			UPDATE findings SET status = 'resolved', resolved_at = $1, updated_at = now()
			WHERE organization_id = $2 AND fingerprint = $3 AND status = 'open'
		`, company.ObservedAt, organizationID, fingerprint)
		return false, err
	}

	var findingID string
	err = tx.QueryRow(ctx, `
		INSERT INTO findings (organization_id, customer_id, rule_name, rule_version, fingerprint,
			status, risk, title, explanation, first_detected_at, last_detected_at)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $8, $9, $9)
		ON CONFLICT (organization_id, fingerprint) DO UPDATE SET
			status = 'open', title = EXCLUDED.title, explanation = EXCLUDED.explanation,
			last_detected_at = EXCLUDED.last_detected_at, resolved_at = NULL, updated_at = now()
		RETURNING id
	`, organizationID, customerID, candidate.RuleName, candidate.RuleVersion, fingerprint,
		candidate.Risk, candidate.Title, candidate.Explanation, company.ObservedAt).Scan(&findingID)
	if err != nil {
		return false, err
	}

	evidence := []struct {
		source, fact, value string
		at                  time.Time
	}{
		{"stripe", "stripe.subscription.status", stripeStatus, stripeObservedAt},
		{"hubspot", "hubspot.customer.status", company.Status, company.ObservedAt},
	}
	for _, item := range evidence {
		_, err = tx.Exec(ctx, `
			INSERT INTO finding_evidence (organization_id, finding_id, source, fact_type, value, observed_at, fingerprint)
			VALUES ($1, $2, $3, $4, to_jsonb($5::text), $6, $7)
			ON CONFLICT (organization_id, finding_id, fingerprint) DO NOTHING
		`, organizationID, findingID, item.source, item.fact, item.value, item.at,
			hashParts(item.source, item.fact, item.value, item.at.Format(time.RFC3339Nano)))
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func saveHubSpotFact(ctx context.Context, tx pgx.Tx, organizationID, customerID, factType, value string, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_facts (organization_id, customer_id, source, fact_type, value, observed_at, schema_version)
		VALUES ($1, $2, 'hubspot', $3, to_jsonb($4::text), $5, 1)
		ON CONFLICT (organization_id, customer_id, source, fact_type)
		DO UPDATE SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at, updated_at = now()
	`, organizationID, customerID, factType, value, observedAt)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Customer"
}

func hashParts(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}
