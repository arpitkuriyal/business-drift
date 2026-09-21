package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Work contains the inputs needed by a provider; it is never returned by the API.
type Work struct {
	Kind           string
	OrganizationID string
	IntegrationID  string
	Payload        []byte
}

type Processor func(context.Context, pgx.Tx, Work) error

// Run processes one job at a time. PostgreSQL keeps pending jobs across restarts.
func Run(ctx context.Context, db *pgxpool.Pool, logger *zap.Logger, process Processor) {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		worked, err := processNext(attemptCtx, db, process)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Error("background job failed", zap.Error(err))
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

func processNext(ctx context.Context, db *pgxpool.Pool, process Processor) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job Work
	var id string
	var eventID *string
	var attempts int
	err = tx.QueryRow(ctx, `
  SELECT j.id,j.organization_id,j.integration_id,j.kind,j.event_id,j.attempts,COALESCE(e.payload,'{}'::jsonb)
  FROM integration_jobs j
  JOIN integrations i ON i.organization_id=j.organization_id AND i.id=j.integration_id
  LEFT JOIN processed_events e ON e.organization_id=j.organization_id AND e.id=j.event_id
  WHERE j.status IN ('pending','failed') AND j.available_at <= now() AND i.status <> 'disconnected'
  ORDER BY j.available_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1
 `).Scan(&id, &job.OrganizationID, &job.IntegrationID, &job.Kind, &eventID, &attempts, &job.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// A savepoint lets failures undo webhook facts while keeping the job locked
	// long enough to record a retry. A crash rolls back the whole transaction.
	work, err := tx.Begin(ctx)
	if err != nil {
		return true, err
	}
	processErr := process(ctx, work, job)
	if processErr != nil {
		if err := work.Rollback(ctx); err != nil {
			return true, err
		}
		if attempts > 9 {
			attempts = 9
		}
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='failed',attempts=attempts+1,
   available_at=now()+$2*interval '1 second',last_error='Processing failed; retry scheduled',updated_at=now() WHERE id=$1`, id, 1<<attempts)
		if err == nil && eventID != nil {
			_, err = tx.Exec(ctx, `UPDATE processed_events SET status='failed',last_error='Processing failed; retry scheduled'
    WHERE organization_id=$1 AND id=$2`, job.OrganizationID, *eventID)
		}
	} else {
		if err := work.Commit(ctx); err != nil {
			return true, err
		}
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='completed',attempts=attempts+1,last_error=NULL,updated_at=now() WHERE id=$1`, id)
		if err == nil && eventID != nil {
			_, err = tx.Exec(ctx, `UPDATE processed_events SET status='processed',processed_at=now(),last_error=NULL
    WHERE organization_id=$1 AND id=$2`, job.OrganizationID, *eventID)
		}
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
