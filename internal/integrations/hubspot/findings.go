package hubspotintegration

import (
	"context"
	"time"

	"github.com/arpitkuriyal/business-drift/internal/detection"
)

func (s *Service) findStripeCustomersMissingHubSpot(ctx context.Context, organizationID string) (int, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := detection.LockOrganization(ctx, tx, organizationID); err != nil {
		return 0, err
	}

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
		created, err := detection.UpsertFinding(ctx, tx, organizationID, customer.id, candidate, customer.observedAt, []detection.Evidence{
			{Source: "stripe", FactType: "stripe.customer.domain", Value: customer.domain, At: customer.observedAt},
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
