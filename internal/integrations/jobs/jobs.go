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

	// One insert is already atomic. The unique active-sync index handles duplicates.
	var id string
	err := db.QueryRow(ctx, `
  INSERT INTO integration_jobs (organization_id,integration_id,kind,status)
  SELECT organization_id,id,$2 || '_sync','pending' FROM integrations
  WHERE organization_id=$1 AND provider=$2 AND status <> 'disconnected'
  ON CONFLICT DO NOTHING RETURNING id
 `, org, provider).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// A duplicate request returns the existing job, even if it just completed.
		err = db.QueryRow(ctx, `
   SELECT j.id FROM integration_jobs j
   JOIN integrations i ON i.organization_id=j.organization_id AND i.id=j.integration_id
   WHERE j.organization_id=$1 AND i.provider=$2 AND j.kind=$2 || '_sync'
    AND i.status <> 'disconnected'
   ORDER BY j.created_at DESC LIMIT 1
  `, org, provider).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return Get(ctx, db, org, id)
}

func Get(ctx context.Context, db *pgxpool.Pool, org, id string) (Job, error) {
	var job Job
	err := db.QueryRow(ctx, `SELECT id,kind,status,attempts,last_error,updated_at FROM integration_jobs WHERE organization_id=$1 AND id=$2`, org, id).Scan(&job.ID, &job.Kind, &job.Status, &job.Attempts, &job.LastError, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return job, err
}
