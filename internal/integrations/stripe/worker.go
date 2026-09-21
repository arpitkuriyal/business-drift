package stripeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/arpitkuriyal/business-drift/internal/detection"
	"github.com/jackc/pgx/v5"
	stripe "github.com/stripe/stripe-go/v86"
)

// ProcessWebhook refreshes Stripe facts inside the worker's transaction.
func (s *Service) ProcessWebhook(ctx context.Context, tx pgx.Tx, organizationID, integrationID string, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := detection.LockOrganization(ctx, tx, organizationID); err != nil {
		return err
	}
	var encryptedKey string
	err := tx.QueryRow(ctx, `SELECT api_key_ciphertext FROM integrations
  WHERE organization_id=$1 AND id=$2 AND provider='stripe'`, organizationID, integrationID).Scan(&encryptedKey)
	if err != nil {
		return err
	}
	key, err := s.cipher.Decrypt(encryptedKey)
	if err != nil {
		return err
	}
	return s.processEvent(ctx, tx, organizationID, key, payload)
}

func (s *Service) processEvent(ctx context.Context, tx pgx.Tx, org, key string, payload []byte) error {
	var event stripe.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return err
	}
	id, err := eventSubscriptionID(event)
	if err != nil {
		return err
	}
	client := stripe.NewClient(key)
	subscription, err := client.V1Subscriptions.Retrieve(ctx, id, nil)
	if err != nil {
		return err
	}
	if subscription.Customer == nil {
		return errors.New("subscription has no customer")
	}
	customer, err := client.V1Customers.Retrieve(ctx, subscription.Customer.ID, nil)
	if err != nil {
		return err
	}
	if err := saveCustomer(ctx, tx, org, customer.ID, customer.Name, customer.Email); err != nil {
		return err
	}
	// Refresh all subscriptions for this customer: canceling one subscription
	// must not mark a customer canceled while another subscription remains active.
	params := &stripe.SubscriptionListParams{Customer: stripe.String(customer.ID), Status: stripe.String("all")}
	params.Limit = stripe.Int64(100)
	current := subscription
	for item, err := range client.V1Subscriptions.List(ctx, params).All(ctx) {
		if err != nil {
			return err
		}
		current = preferredSubscription(current, item)
	}
	return saveSubscription(ctx, tx, org, customer.ID, string(current.Status))
}

// Active/trialing subscriptions take precedence; otherwise use the newest.
func preferredSubscription(a, b *stripe.Subscription) *stripe.Subscription {
	if a == nil {
		return b
	}
	active := func(s *stripe.Subscription) bool { return s.Status == "active" || s.Status == "trialing" }
	if active(a) != active(b) {
		if active(b) {
			return b
		}
		return a
	}
	if b.Created > a.Created || (b.Created == a.Created && b.ID >= a.ID) {
		return b
	}
	return a
}
