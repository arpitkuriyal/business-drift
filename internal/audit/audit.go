package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arpitkuriyal/business-drift/internal/auth"
)

type Event struct {
	ID         string         `json:"id"`
	UserID     *string        `json:"user_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   *string        `json:"target_id"`
	Details    map[string]any `json:"details"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Repository requires the trusted organization ID for every read. The limit
// keeps this first endpoint bounded until cursor pagination is introduced.
type Repository struct {
	database *pgxpool.Pool
}

func NewRepository(database *pgxpool.Pool) *Repository {
	return &Repository{database: database}
}

func (r *Repository) List(ctx context.Context, organizationID string) ([]Event, error) {
	rows, err := r.database.Query(ctx, `
		SELECT id, user_id, action, target_type, target_id, details, created_at
		FROM audit_events
		WHERE organization_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.UserID, &event.Action, &event.TargetType, &event.TargetID, &event.Details, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type Handler struct {
	repository *Repository
}

func NewHandler(repository *Repository) *Handler {
	return &Handler{repository: repository}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	events, err := h.repository.List(r.Context(), identity.OrganizationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"code": "internal_error", "message": "Audit events could not be loaded."},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
