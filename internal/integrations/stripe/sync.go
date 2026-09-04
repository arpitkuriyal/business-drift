package stripeintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	stripe "github.com/stripe/stripe-go/v86"
)

func (s *Service) syncAll(ctx context.Context, organizationID, apiKey string) (SyncResult, error) {
	client := stripe.NewClient(apiKey)
	result := SyncResult{}
	customers := &stripe.CustomerListParams{}
	customers.Limit = stripe.Int64(100)
	for customer, err := range client.V1Customers.List(ctx, customers).All(ctx) {
		if err != nil {
			return result, fmt.Errorf("list Stripe customers: %w", err)
		}
		if err := s.saveCustomer(ctx, organizationID, customer.ID, customer.Name, customer.Email); err != nil {
			return result, err
		}
		result.Customers++
	}

	subscriptions := &stripe.SubscriptionListParams{}
	subscriptions.Limit = stripe.Int64(100)
	subscriptions.Status = stripe.String("all")
	for subscription, err := range client.V1Subscriptions.List(ctx, subscriptions).All(ctx) {
		if err != nil {
			return result, fmt.Errorf("list Stripe subscriptions: %w", err)
		}
		if subscription.Customer == nil {
			continue
		}
		if err := s.saveSubscription(ctx, organizationID, subscription.Customer.ID, string(subscription.Status)); err != nil {
			return result, err
		}
		result.Subscriptions++
	}
	return result, nil
}

func (s *Service) saveCustomer(ctx context.Context, organizationID, stripeID, name, email string) error {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	customerID, err := ensureCustomer(ctx, tx, organizationID, stripeID, firstNonEmpty(name, email, stripeID))
	if err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" {
		if err := saveFact(ctx, tx, organizationID, customerID, "stripe.customer.email", email); err != nil {
			return err
		}
		if domain := emailDomain(email); domain != "" {
			if err := saveFact(ctx, tx, organizationID, customerID, "stripe.customer.domain", domain); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) saveSubscription(ctx context.Context, organizationID, stripeID, status string) error {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	customerID, err := ensureCustomer(ctx, tx, organizationID, stripeID, stripeID)
	if err != nil {
		return err
	}
	if err := saveFact(ctx, tx, organizationID, customerID, "stripe.subscription.status", status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensureCustomer(ctx context.Context, tx pgx.Tx, organizationID, stripeID, name string) (string, error) {
	var customerID string
	err := tx.QueryRow(ctx, `
		SELECT customer_id FROM customer_identities
		WHERE organization_id = $1 AND source = 'stripe' AND external_id = $2
	`, organizationID, stripeID).Scan(&customerID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE canonical_customers SET name = $1, updated_at = now() WHERE id = $2`, name, customerID)
		return customerID, err
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO canonical_customers (organization_id, name) VALUES ($1, $2) RETURNING id`, organizationID, name).Scan(&customerID); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO customer_identities (organization_id, customer_id, source, external_id, match_method)
		VALUES ($1, $2, 'stripe', $3, 'external_id')
	`, organizationID, customerID, stripeID)
	return customerID, err
}

func saveFact(ctx context.Context, tx pgx.Tx, organizationID, customerID, factType, value string) error {
	encoded, _ := json.Marshal(value)
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_facts (organization_id, customer_id, source, fact_type, value, observed_at, schema_version)
		VALUES ($1, $2, 'stripe', $3, $4::jsonb, $5, 1)
		ON CONFLICT (organization_id, customer_id, source, fact_type)
		DO UPDATE SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at, updated_at = now()
	`, organizationID, customerID, factType, string(encoded), time.Now().UTC())
	return err
}

func emailDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	domain := strings.ToLower(strings.TrimSpace(parts[1]))
	free := map[string]bool{"gmail.com": true, "outlook.com": true, "hotmail.com": true, "yahoo.com": true, "icloud.com": true}
	if free[domain] {
		return ""
	}
	return domain
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Customer"
}
