// Package jobs owns durable manual-sync requests. Webhook event jobs use the
// same table but commit their facts and completion in the Stripe event worker.
package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

var ErrNotFound = errors.New("integration or job not found")

type Job struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	LastError *string   `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func EnqueueSync(ctx context.Context, db *pgxpool.Pool, org, provider string) (Job, error) {
	if provider != "stripe" && provider != "hubspot" {
		return Job{}, errors.New("unsupported provider")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "sync-enqueue:"+org+":"+provider); err != nil {
		return Job{}, err
	}
	var integrationID string
	// Serialize enqueue requests without blocking the worker's long-running job.
	err = tx.QueryRow(ctx, `SELECT id FROM integrations WHERE organization_id=$1 AND provider=$2 AND status <> 'disconnected'`, org, provider).Scan(&integrationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO integration_jobs(organization_id,integration_id,kind,status) VALUES($1,$2,$3,'pending') ON CONFLICT DO NOTHING RETURNING id`, org, integrationID, provider+"_sync").Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM integration_jobs WHERE organization_id=$1 AND integration_id=$2 AND kind=$3 AND status IN ('pending','processing','failed')`, org, integrationID, provider+"_sync").Scan(&id)
	}
	if err != nil {
		return Job{}, err
	}
	var job Job
	err = tx.QueryRow(ctx, `SELECT id,kind,status,attempts,last_error,updated_at FROM integration_jobs WHERE organization_id=$1 AND id=$2`, org, id).Scan(&job.ID, &job.Kind, &job.Status, &job.Attempts, &job.LastError, &job.UpdatedAt)
	if err != nil {
		return Job{}, err
	}
	return job, tx.Commit(ctx)
}

func Get(ctx context.Context, db *pgxpool.Pool, org, id string) (Job, error) {
	var job Job
	err := db.QueryRow(ctx, `SELECT id,kind,status,attempts,last_error,updated_at FROM integration_jobs WHERE organization_id=$1 AND id=$2`, org, id).Scan(&job.ID, &job.Kind, &job.Status, &job.Attempts, &job.LastError, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return job, err
}

type Runner func(context.Context, string) error

func Run(ctx context.Context, db *pgxpool.Pool, logger *zap.Logger, runners map[string]Runner) {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		worked, err := processNext(attemptCtx, db, runners)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Error("sync worker failed", zap.Error(err))
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

func processNext(ctx context.Context, db *pgxpool.Pool, runners map[string]Runner) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, org, kind string
	var attempts int
	err = tx.QueryRow(ctx, `SELECT j.id,j.organization_id,j.kind,j.attempts FROM integration_jobs j
 JOIN integrations i ON i.organization_id=j.organization_id AND i.id=j.integration_id
 WHERE j.kind IN ('stripe_sync','hubspot_sync') AND j.status IN ('pending','failed') AND j.available_at <= now() AND i.status <> 'disconnected'
 ORDER BY j.available_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1`).Scan(&id, &org, &kind, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	runner, ok := runners[kind]
	if !ok {
		return true, errors.New("sync runner not registered")
	}
	// Sync writes are idempotent. A crash before this commit releases the row lock
	// and replays the import; already committed customer records are upserted.
	runErr := runner(ctx, org)
	if runErr != nil {
		if attempts > 9 {
			attempts = 9
		}
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='failed',attempts=attempts+1,last_error='Sync failed; retry scheduled',available_at=now()+$2*interval '1 second',updated_at=now() WHERE id=$1`, id, 1<<attempts)
	} else {
		_, err = tx.Exec(ctx, `UPDATE integration_jobs SET status='completed',attempts=attempts+1,last_error=NULL,updated_at=now() WHERE id=$1`, id)
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
