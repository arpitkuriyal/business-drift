package stripeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	stripewebhook "github.com/stripe/stripe-go/v86/webhook"
	"go.uber.org/zap"
)

const maxWebhookBodyBytes = 1024 * 1024

var allowedEventTypes = map[string]bool{
	"customer.created":              true,
	"customer.updated":              true,
	"customer.deleted":              true,
	"customer.subscription.created": true,
	"customer.subscription.updated": true,
	"customer.subscription.deleted": true,
	"invoice.created":               true,
	"invoice.finalized":             true,
	"invoice.paid":                  true,
	"invoice.payment_failed":        true,
	"invoice.voided":                true,
}

type webhookEnvelope struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	integrationID := r.PathValue("integrationID")
	organizationID, _, webhookSecret, err := h.service.loadSecrets(r.Context(), integrationID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "integration_not_found", "The webhook endpoint was not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The webhook could not be accepted.")
		return
	}

	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", "The webhook payload is invalid or too large.")
		return
	}
	// Validation checks the signature and Stripe's five-minute timestamp
	// tolerance against the exact raw body before JSON parsing.
	if err := stripewebhook.ValidatePayload(payload, r.Header.Get("Stripe-Signature"), webhookSecret); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_signature", "The Stripe signature is invalid.")
		return
	}

	var event webhookEnvelope
	if err := json.Unmarshal(payload, &event); err != nil || event.ID == "" || event.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_event", "The Stripe event is invalid.")
		return
	}
	if !allowedEventTypes[event.Type] {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	eventID, jobID, duplicate, err := h.service.storeWebhook(r.Context(), organizationID, integrationID, event, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The Stripe event could not be stored.")
		return
	}
	if duplicate {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	if err := h.service.redis.LPush(r.Context(), jobQueue, jobID).Err(); err != nil {
		h.service.logger.Warn("Stripe event notification failed; database polling will recover it",
			zap.String("event_id", eventID),
			zap.Error(err),
		)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Service) storeWebhook(ctx context.Context, organizationID, integrationID string, event webhookEnvelope, payload []byte) (eventID, jobID string, duplicate bool, err error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return "", "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		INSERT INTO processed_events (
			organization_id, integration_id, external_event_id, event_type, payload, status
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, 'pending')
		ON CONFLICT (organization_id, external_event_id) DO NOTHING
		RETURNING id
	`, organizationID, integrationID, event.ID, event.Type, string(payload)).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", true, nil
	}
	if err != nil {
		return "", "", false, err
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO integration_jobs (organization_id, integration_id, event_id, kind, status)
		VALUES ($1, $2, $3, 'stripe_event', 'pending')
		RETURNING id
	`, organizationID, integrationID, eventID).Scan(&jobID)
	if err != nil {
		return "", "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", false, err
	}
	return eventID, jobID, false, nil
}
