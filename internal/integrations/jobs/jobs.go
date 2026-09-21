// Package jobs stores manual sync requests and runs the background queue.
package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
