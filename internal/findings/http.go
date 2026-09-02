package findings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/auth"
)

type Handler struct {
	repository *Repository
}

func NewHandler(repository *Repository) *Handler {
	return &Handler{repository: repository}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	items, err := h.repository.List(r.Context(), identity.OrganizationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Findings could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	item, err := h.repository.Get(r.Context(), identity.OrganizationID, r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		// A finding from another organization is deliberately indistinguishable
		// from an ID that does not exist.
		writeError(w, http.StatusNotFound, "finding_not_found", "The finding was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The finding could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, item)
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
