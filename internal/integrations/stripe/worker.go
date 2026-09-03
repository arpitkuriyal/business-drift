package stripeintegration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type job struct {
	ID             string
	OrganizationID string
	IntegrationID  string
	EventID        *string
	Kind           string
	Attempts       int
}

// RunWorker processes durable jobs until the application begins shutdown.
// A single worker is enough for Phase 3; SKIP LOCKED permits safe scaling later.
func (s *Service) RunWorker(ctx context.Context) {
	go s.reconciliationLoop(ctx)

	for ctx.Err() == nil {
		jobID := s.waitForNotification(ctx)
		if ctx.Err() != nil {
			return
		}
		claimed, err := s.claimJob(ctx, jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			s.logger.Error("claim Stripe job", zap.Error(err))
			continue
		}

		if err := s.processJob(ctx, claimed); err != nil {
			s.failJob(ctx, claimed, err)
			continue
		}
		s.completeJob(ctx, claimed)
	}
}

func (s *Service) waitForNotification(ctx context.Context) string {
	result, err := s.redis.BLPop(ctx, 2*time.Second, jobQueue).Result()
	if err == nil && len(result) == 2 {
		return result[1]
	}
	if err != nil && !errors.Is(err, redis.Nil) && ctx.Err() == nil {
		s.logger.Warn("Stripe Redis queue unavailable; polling PostgreSQL", zap.Error(err))
	}
	return ""
}

func (s *Service) claimJob(ctx context.Context, preferredID string) (job, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Jobs left in processing by a crashed worker become retryable.
	_, _ = tx.Exec(ctx, `
		UPDATE integration_jobs
		SET status = 'failed', available_at = now(), updated_at = now(), last_error = 'worker interrupted'
		WHERE status = 'processing' AND updated_at < now() - interval '5 minutes'
	`)

	query := `
		SELECT id, organization_id, integration_id, event_id, kind, attempts
		FROM integration_jobs
		WHERE status IN ('pending', 'failed') AND attempts < 5 AND available_at <= now()
	`
	arguments := []any{}
	if preferredID != "" {
		query += " AND id = $1"
		arguments = append(arguments, preferredID)
	}
	query += " ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1"

	var claimed job
	err = tx.QueryRow(ctx, query, arguments...).Scan(
		&claimed.ID,
		&claimed.OrganizationID,
		&claimed.IntegrationID,
		&claimed.EventID,
		&claimed.Kind,
		&claimed.Attempts,
	)
	if err != nil {
		return job{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE integration_jobs
		SET status = 'processing', attempts = attempts + 1, updated_at = now()
		WHERE id = $1
	`, claimed.ID); err != nil {
		return job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return job{}, err
	}
	claimed.Attempts++
	return claimed, nil
}

func (s *Service) processJob(ctx context.Context, current job) error {
	_, apiKey, _, err := s.loadSecrets(ctx, current.IntegrationID)
	if err != nil {
		return err
	}

	switch current.Kind {
	case "stripe_sync":
		syncContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return s.syncAll(syncContext, current.OrganizationID, current.IntegrationID, apiKey)
	case "stripe_event":
		if current.EventID == nil {
			return fmt.Errorf("Stripe event job has no event")
		}
		return s.processStoredEvent(ctx, current.OrganizationID, *current.EventID)
	default:
		return fmt.Errorf("unsupported Stripe job kind %q", current.Kind)
	}
}

func (s *Service) completeJob(ctx context.Context, completed job) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		s.logger.Error("finish Stripe job", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE integration_jobs
		SET status = 'completed', last_error = NULL, updated_at = now()
		WHERE id = $1
	`, completed.ID)
	if err == nil && completed.EventID != nil {
		_, err = tx.Exec(ctx, `
			UPDATE processed_events
			SET status = 'processed', last_error = NULL, processed_at = now()
			WHERE id = $1
		`, *completed.EventID)
	}
	if err == nil && completed.Kind == "stripe_sync" {
		_, err = tx.Exec(ctx, `
			UPDATE integrations
			SET status = 'active', last_synced_at = now(), last_error = NULL, updated_at = now()
			WHERE id = $1
		`, completed.IntegrationID)
	}
	if err != nil {
		s.logger.Error("finish Stripe job", zap.String("job_id", completed.ID), zap.Error(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit Stripe job", zap.String("job_id", completed.ID), zap.Error(err))
	}
}

func (s *Service) failJob(ctx context.Context, failed job, jobError error) {
	backoff := time.Duration(failed.Attempts*failed.Attempts) * time.Minute
	status := "failed"
	availableAt := time.Now().Add(backoff)

	tx, err := s.database.Begin(ctx)
	if err != nil {
		s.logger.Error("record Stripe job failure", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE integration_jobs
		SET status = $1, available_at = $2, last_error = $3, updated_at = now()
		WHERE id = $4
	`, status, availableAt, safeError(jobError), failed.ID)
	if err == nil && failed.EventID != nil {
		_, err = tx.Exec(ctx, `
			UPDATE processed_events SET status = 'failed', last_error = $1 WHERE id = $2
		`, safeError(jobError), *failed.EventID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `
			UPDATE integrations SET status = 'error', last_error = $1, updated_at = now() WHERE id = $2
		`, safeError(jobError), failed.IntegrationID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	s.logger.Error("Stripe job failed",
		zap.String("job_id", failed.ID),
		zap.Int("attempt", failed.Attempts),
		zap.Error(jobError),
	)
}

func (s *Service) reconciliationLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := s.database.Exec(ctx, `
				INSERT INTO integration_jobs (organization_id, integration_id, kind, status)
				SELECT organization_id, id, 'stripe_sync', 'pending'
				FROM integrations i
				WHERE provider = 'stripe' AND status <> 'disconnected'
				  AND (last_synced_at IS NULL OR last_synced_at < now() - interval '6 hours')
				  AND NOT EXISTS (
					SELECT 1 FROM integration_jobs j
					WHERE j.integration_id = i.id AND j.kind = 'stripe_sync'
					  AND j.status IN ('pending', 'processing')
				  )
				ON CONFLICT DO NOTHING
			`)
			if err != nil {
				s.logger.Error("schedule Stripe reconciliation", zap.Error(err))
			}
		}
	}
}

func safeError(err error) string {
	message := err.Error()
	if len(message) > 500 {
		return message[:500]
	}
	return message
}
