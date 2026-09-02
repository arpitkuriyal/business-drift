package stripeintegration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/auth"
)

const maxConfigurationBodyBytes = 16 * 1024

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey        string `json:"api_key"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	identity, _ := auth.IdentityFromContext(r.Context())
	integration, err := h.service.Save(r.Context(), identity, input.APIKey, input.WebhookSecret)
	if errors.Is(err, ErrInvalidSecret) {
		writeError(w, http.StatusBadRequest, "invalid_stripe_credentials", "Stripe sandbox API and webhook secrets are required.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The Stripe integration could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, integration)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	integration, err := h.service.Get(r.Context(), identity.OrganizationID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "stripe_not_configured", "Stripe is not configured for this organization.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The Stripe integration could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, integration)
}

func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	identity, _ := auth.IdentityFromContext(r.Context())
	jobID, err := h.service.EnqueueSync(r.Context(), identity)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "stripe_not_configured", "Configure Stripe before starting a sync.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The Stripe sync could not be queued.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "pending"})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigurationBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("request body must contain valid JSON with only supported fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
