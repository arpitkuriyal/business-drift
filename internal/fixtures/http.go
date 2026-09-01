package fixtures

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/auth"
)

const maxFixtureBodyBytes = 32 * 1024

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFixtureBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var input Input
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body must contain a valid fixture JSON object.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body must contain one JSON object.")
		return
	}

	identity, _ := auth.IdentityFromContext(r.Context())
	result, err := h.service.Ingest(r.Context(), identity, input)
	if errors.Is(err, ErrInvalidFixture) {
		writeError(w, http.StatusBadRequest, "invalid_fixture", "Customer IDs, name, and supported Stripe and HubSpot statuses are required.")
		return
	}
	if errors.Is(err, ErrIdentityConflict) {
		writeError(w, http.StatusConflict, "identity_conflict", "The Stripe and HubSpot identities are already mapped to different customers.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The fixture could not be processed.")
		return
	}
	writeJSON(w, http.StatusOK, result)
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
