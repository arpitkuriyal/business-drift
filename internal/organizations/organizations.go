package organizations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arpitkuriyal/business-drift/internal/auth"
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository is intentionally small. Its methods always require an
// organization ID, making tenant scope visible at every database call.
type Repository struct {
	database *pgxpool.Pool
}

func NewRepository(database *pgxpool.Pool) *Repository {
	return &Repository{database: database}
}

func (r *Repository) Get(ctx context.Context, organizationID string) (Organization, error) {
	var organization Organization
	err := r.database.QueryRow(ctx, `
		SELECT id, name, created_at
		FROM organizations
		WHERE id = $1
	`, organizationID).Scan(&organization.ID, &organization.Name, &organization.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, errors.New("organization not found")
	}
	return organization, err
}

type Handler struct {
	repository *Repository
}

func NewHandler(repository *Repository) *Handler {
	return &Handler{repository: repository}
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	organization, err := h.repository.Get(r.Context(), identity.OrganizationID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organization_not_found", "The organization was not found.")
		return
	}
	writeJSON(w, http.StatusOK, organization)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
