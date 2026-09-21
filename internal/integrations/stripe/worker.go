package stripeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/arpitkuriyal/business-drift/internal/detection"
	"github.com/jackc/pgx/v5"
	stripe "github.com/stripe/stripe-go/v86"
	"go.uber.org/zap"
)

// RunWorker polls PostgreSQL: no notification can be lost between commit and
// dispatch. A row lock owns each job until facts, findings and completion commit.
// A process crash rolls everything back, leaving the job available for retry.
func (s *Service) RunWorker(ctx context.Context, logger *zap.Logger) {
	for ctx.Err() == nil {
		jobCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		worked, err := s.processNext(jobCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Error("Stripe webhook worker failed", zap.Error(err))
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) processNext(ctx context.Context) (bool, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var jobID, org, eventID, encryptedKey string
	var payload []byte
	var attempts int
	err = tx.QueryRow(ctx, `SELECT j.id,j.organization_id,j.event_id,j.attempts,e.payload,i.api_key_ciphertext
 FROM integration_jobs j
 JOIN processed_events e ON e.organization_id=j.organization_id AND e.id=j.event_id
 JOIN integrations i ON i.organization_id=j.organization_id AND i.id=j.integration_id
 WHERE j.kind='stripe_event' AND j.status IN ('pending','failed') AND j.available_at <= now()
 AND i.provider='stripe' AND i.status <> 'disconnected'
 ORDER BY j.available_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(&jobID, &org, &eventID, &attempts, &payload, &encryptedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Savepoint lets a failed attempt roll back fact changes while recording retry state.
	work, err := tx.Begin(ctx)
	if err != nil {
		return true, err
	}
	processErr := detection.LockOrganization(ctx, work, org)
	if processErr == nil {
		var key string
		key, processErr = s.cipher.Decrypt(encryptedKey)
		if processErr == nil {
			processErr = s.processEvent(ctx, work, org, key, payload)
		}
	}
	if processErr != nil {
		if err := work.Rollback(ctx); err != nil {
			return true, err
		}
		// Keep provider responses and credentials out of durable error messages.
		delay := retryDelay(attempts)
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='failed',attempts=attempts+1,available_at=now()+$2 * interval '1 second',last_error='Stripe event processing failed; retry scheduled',updated_at=now() WHERE id=$1`, jobID, delay.Seconds())
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE processed_events SET status='failed',last_error='Stripe event processing failed; retry scheduled' WHERE organization_id=$1 AND id=$2`, org, eventID)
		}
	} else {
		if err := work.Commit(ctx); err != nil {
			return true, err
		}
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='completed',attempts=attempts+1,last_error=NULL,updated_at=now() WHERE id=$1`, jobID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE processed_events SET status='processed',processed_at=now(),last_error=NULL WHERE organization_id=$1 AND id=$2`, org, eventID)
		}
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func retryDelay(attempts int) time.Duration {
	if attempts > 9 {
		attempts = 9
	}
	return time.Duration(1<<attempts) * time.Second
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
