package hubspotintegration

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arpitkuriyal/business-drift/internal/detection"
)

type findingEvidence struct {
	source   string
	factType string
	value    string
	at       time.Time
}

func upsertFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID string, candidate *detection.CandidateFinding, observedAt time.Time, evidence []findingEvidence) (bool, error) {
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
		`, organizationID, findingID, item.source, item.factType, item.value, item.at,
			hashParts(item.source, item.factType, item.value, item.at.Format(time.RFC3339Nano)))
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func resolveFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID, ruleName string, resolvedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE findings SET status = 'resolved', resolved_at = $1, updated_at = now()
		WHERE organization_id = $2 AND fingerprint = $3 AND status = 'open'
	`, resolvedAt, organizationID, hashParts(organizationID, customerID, ruleName))
	return err
}

func (s *Service) findStripeCustomersMissingHubSpot(ctx context.Context, organizationID string) (int, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT customer.id, customer.name,
			COALESCE(domain.value #>> '{}', ''), COALESCE(domain.observed_at, customer.updated_at)
		FROM canonical_customers customer
		LEFT JOIN customer_facts domain
			ON domain.organization_id = customer.organization_id
			AND domain.customer_id = customer.id
			AND domain.source = 'stripe' AND domain.fact_type = 'stripe.customer.domain'
		WHERE customer.organization_id = $1
			AND EXISTS (
				SELECT 1 FROM customer_identities identity
				WHERE identity.organization_id = customer.organization_id
					AND identity.customer_id = customer.id AND identity.source = 'stripe'
			)
			AND NOT EXISTS (
				SELECT 1 FROM customer_identities identity
				WHERE identity.organization_id = customer.organization_id
					AND identity.customer_id = customer.id AND identity.source = 'hubspot'
			)
	`, organizationID)
	if err != nil {
		return 0, err
	}
	type unmatchedCustomer struct {
		id, name, domain string
		observedAt       time.Time
	}
	var customers []unmatchedCustomer
	for rows.Next() {
		var customer unmatchedCustomer
		if err := rows.Scan(&customer.id, &customer.name, &customer.domain, &customer.observedAt); err != nil {
			rows.Close()
			return 0, err
		}
		customers = append(customers, customer)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	count := 0
	for _, customer := range customers {
		candidate := detection.EvaluateMissingCustomer(detection.CustomerPresence{
			CustomerName: customer.name, HasStripe: true, HasHubSpot: false,
		})
		created, err := upsertFinding(ctx, tx, organizationID, customer.id, candidate, customer.observedAt, []findingEvidence{
			{source: "stripe", factType: "stripe.customer.domain", value: customer.domain, at: customer.observedAt},
		})
		if err != nil {
			return 0, err
		}
		if created {
			count++
		}
	}
	return count, tx.Commit(ctx)
}
