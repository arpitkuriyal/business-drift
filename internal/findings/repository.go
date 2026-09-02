package findings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("finding not found")

type Finding struct {
	ID              string     `json:"id"`
	CustomerID      string     `json:"customer_id"`
	CustomerName    string     `json:"customer_name"`
	RuleName        string     `json:"rule_name"`
	RuleVersion     int        `json:"rule_version"`
	Status          string     `json:"status"`
	Risk            string     `json:"risk"`
	Title           string     `json:"title"`
	Explanation     string     `json:"explanation"`
	FirstDetectedAt time.Time  `json:"first_detected_at"`
	LastDetectedAt  time.Time  `json:"last_detected_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	Evidence        []Evidence `json:"evidence,omitempty"`
}

type Evidence struct {
	ID         string          `json:"id"`
	Source     string          `json:"source"`
	FactType   string          `json:"fact_type"`
	Value      json.RawMessage `json:"value"`
	ObservedAt time.Time       `json:"observed_at"`
}

type Repository struct {
	database *pgxpool.Pool
}

func NewRepository(database *pgxpool.Pool) *Repository {
	return &Repository{database: database}
}

// List always scopes by organization before sorting or limiting. This order is
// a security boundary, not merely a dashboard filter.
func (r *Repository) List(ctx context.Context, organizationID string) ([]Finding, error) {
	rows, err := r.database.Query(ctx, `
		SELECT
			f.id, f.customer_id, c.name, f.rule_name, f.rule_version,
			f.status, f.risk, f.title, f.explanation,
			f.first_detected_at, f.last_detected_at, f.resolved_at
		FROM findings f
		JOIN canonical_customers c
		  ON c.organization_id = f.organization_id AND c.id = f.customer_id
		WHERE f.organization_id = $1
		ORDER BY f.last_detected_at DESC
		LIMIT 50
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Finding, 0)
	for rows.Next() {
		finding, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, finding)
	}
	return result, rows.Err()
}

func (r *Repository) Get(ctx context.Context, organizationID, findingID string) (Finding, error) {
	row := r.database.QueryRow(ctx, `
		SELECT
			f.id, f.customer_id, c.name, f.rule_name, f.rule_version,
			f.status, f.risk, f.title, f.explanation,
			f.first_detected_at, f.last_detected_at, f.resolved_at
		FROM findings f
		JOIN canonical_customers c
		  ON c.organization_id = f.organization_id AND c.id = f.customer_id
		WHERE f.organization_id = $1 AND f.id = $2
	`, organizationID, findingID)
	finding, err := scanFinding(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	}
	if err != nil {
		return Finding{}, err
	}

	rows, err := r.database.Query(ctx, `
		SELECT id, source, fact_type, value, observed_at
		FROM finding_evidence
		WHERE organization_id = $1 AND finding_id = $2
		ORDER BY observed_at, source
	`, organizationID, findingID)
	if err != nil {
		return Finding{}, err
	}
	defer rows.Close()

	finding.Evidence = make([]Evidence, 0)
	for rows.Next() {
		var evidence Evidence
		if err := rows.Scan(&evidence.ID, &evidence.Source, &evidence.FactType, &evidence.Value, &evidence.ObservedAt); err != nil {
			return Finding{}, err
		}
		finding.Evidence = append(finding.Evidence, evidence)
	}
	return finding, rows.Err()
}

type rowScanner interface {
	Scan(destinations ...any) error
}

func scanFinding(row rowScanner) (Finding, error) {
	var finding Finding
	err := row.Scan(
		&finding.ID,
		&finding.CustomerID,
		&finding.CustomerName,
		&finding.RuleName,
		&finding.RuleVersion,
		&finding.Status,
		&finding.Risk,
		&finding.Title,
		&finding.Explanation,
		&finding.FirstDetectedAt,
		&finding.LastDetectedAt,
		&finding.ResolvedAt,
	)
	return finding, err
}
