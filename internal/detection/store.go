package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Evidence struct {
	Source   string
	FactType string
	Value    string
	At       time.Time
}

func UpsertFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID string, candidate *CandidateFinding, observedAt time.Time, evidence []Evidence) (bool, error) {
	fingerprint := hashParts(organizationID, customerID, candidate.RuleName)
	var findingID string
	err := tx.QueryRow(ctx, `
		INSERT INTO findings (organization_id, customer_id, rule_name, rule_version, fingerprint,
			status, risk, title, explanation, first_detected_at, last_detected_at)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $8, $9, $9)
		ON CONFLICT (organization_id, fingerprint) DO UPDATE SET
			status = 'open', risk = EXCLUDED.risk, title = EXCLUDED.title,
			explanation = EXCLUDED.explanation, last_detected_at = EXCLUDED.last_detected_at,
			resolved_at = NULL, updated_at = now()
		RETURNING id
	`, organizationID, customerID, candidate.RuleName, candidate.RuleVersion, fingerprint,
		candidate.Risk, candidate.Title, candidate.Explanation, observedAt).Scan(&findingID)
	if err != nil {
		return false, err
	}

	for _, item := range evidence {
		_, err = tx.Exec(ctx, `
			INSERT INTO finding_evidence (organization_id, finding_id, source, fact_type, value, observed_at, fingerprint)
			VALUES ($1, $2, $3, $4, to_jsonb($5::text), $6, $7)
			ON CONFLICT (organization_id, finding_id, fingerprint) DO NOTHING
		`, organizationID, findingID, item.Source, item.FactType, item.Value, item.At,
			hashParts(item.Source, item.FactType, item.Value, item.At.Format(time.RFC3339Nano)))
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func ResolveFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID, ruleName string, resolvedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE findings SET status = 'resolved', resolved_at = $1, updated_at = now()
		WHERE organization_id = $2 AND fingerprint = $3 AND status = 'open'
	`, resolvedAt, organizationID, hashParts(organizationID, customerID, ruleName))
	return err
}

func hashParts(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}

// LockOrganization serializes fact updates and detection from both sources.
// Stripe callers acquire it before fetching remote state to prevent stale writes.
func LockOrganization(ctx context.Context, tx pgx.Tx, organizationID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, organizationID)
	return err
}

// CompareStatus uses stored facts, so either source can trigger the same rule.
func CompareStatus(ctx context.Context, tx pgx.Tx, organizationID, customerID string) (bool, error) {
	var name, stripeStatus, hubspotStatus string
	var stripeAt, hubspotAt time.Time
	err := tx.QueryRow(ctx, `
 SELECT c.name, s.value #>> '{}', s.observed_at, h.value #>> '{}', h.observed_at
 FROM canonical_customers c
 JOIN customer_facts s ON s.organization_id=c.organization_id AND s.customer_id=c.id
   AND s.source='stripe' AND s.fact_type='stripe.subscription.status'
 JOIN customer_facts h ON h.organization_id=c.organization_id AND h.customer_id=c.id
   AND h.source='hubspot' AND h.fact_type='hubspot.customer.status'
 WHERE c.organization_id=$1 AND c.id=$2`, organizationID, customerID).Scan(&name, &stripeStatus, &stripeAt, &hubspotStatus, &hubspotAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	candidate := EvaluateStatusMismatch(CustomerSnapshot{CustomerName: name, StripeSubscriptionStatus: stripeStatus, HubSpotCustomerStatus: hubspotStatus})
	if candidate == nil {
		return false, ResolveFinding(ctx, tx, organizationID, customerID, StatusMismatchRuleName, now)
	}
	return UpsertFinding(ctx, tx, organizationID, customerID, candidate, now, []Evidence{
		{Source: "stripe", FactType: "stripe.subscription.status", Value: stripeStatus, At: stripeAt},
		{Source: "hubspot", FactType: "hubspot.customer.status", Value: hubspotStatus, At: hubspotAt},
	})
}
