package hubspotintegration

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arpitkuriyal/business-drift/internal/detection"
)

func (s *Service) storeCompany(ctx context.Context, organizationID string, company companyRecord) (bool, int, error) {
	if company.ID == "" {
		return false, 0, errors.New("HubSpot company has no ID")
	}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := detection.LockOrganization(ctx, tx, organizationID); err != nil {
		return false, 0, err
	}
	customerID, matched, err := resolveCustomer(ctx, tx, organizationID, company)
	if err != nil {
		return false, 0, err
	}
	if err := saveHubSpotFact(ctx, tx, organizationID, customerID, "hubspot.company.domain", company.Domain, company.ObservedAt); err != nil {
		return false, 0, err
	}
	if err := saveHubSpotFact(ctx, tx, organizationID, customerID, "hubspot.customer.status", company.Status, company.ObservedAt); err != nil {
		return false, 0, err
	}

	findingCount := 0
	missing := detection.EvaluateMissingCustomer(detection.CustomerPresence{
		CustomerName: firstNonEmpty(company.Name, company.Domain, company.ID),
		HasStripe:    matched,
		HasHubSpot:   true,
	})
	if missing != nil {
		created, err := detection.UpsertFinding(ctx, tx, organizationID, customerID, missing, company.ObservedAt, []detection.Evidence{
			{Source: "hubspot", FactType: "hubspot.company.domain", Value: company.Domain, At: company.ObservedAt},
		})
		if err != nil {
			return false, 0, err
		}
		if created {
			findingCount++
		}
	} else {
		if err := detection.ResolveFinding(ctx, tx, organizationID, customerID, detection.MissingInHubSpotRuleName, company.ObservedAt); err != nil {
			return false, 0, err
		}
		if err := detection.ResolveFinding(ctx, tx, organizationID, customerID, detection.MissingInStripeRuleName, company.ObservedAt); err != nil {
			return false, 0, err
		}
	}

	statusFinding, err := detection.CompareStatus(ctx, tx, organizationID, customerID)
	if err != nil {
		return false, 0, err
	}
	if statusFinding {
		findingCount++
	}
	return matched, findingCount, tx.Commit(ctx)
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

func saveHubSpotFact(ctx context.Context, tx pgx.Tx, organizationID, customerID, factType, value string, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_facts (organization_id, customer_id, source, fact_type, value, observed_at, schema_version)
		VALUES ($1, $2, 'hubspot', $3, to_jsonb($4::text), $5, 1)
		ON CONFLICT (organization_id, customer_id, source, fact_type)
		DO UPDATE SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at, updated_at = now()
		WHERE customer_facts.observed_at <= EXCLUDED.observed_at
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
