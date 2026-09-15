package stripeintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
)

const maxWebhookBodyBytes = 1024 * 1024

// Webhook authenticates the original bytes; it deliberately does not use user sessions.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	var id pgtype.UUID
	if id.Scan(r.PathValue("integrationID")) != nil {
		writeError(w, http.StatusNotFound, "not_found", "Webhook integration not found.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, "invalid_payload", "Webhook body could not be read.")
		return
	}
	var org, ciphertext string
	err = h.service.database.QueryRow(r.Context(), `SELECT organization_id, webhook_secret_ciphertext FROM integrations WHERE id=$1 AND provider='stripe' AND status <> 'disconnected'`, id).Scan(&org, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Webhook integration not found.")
		return
	}
	if err != nil {
		writeError(w, 503, "unavailable", "Webhook storage is unavailable.")
		return
	}
	secret, err := h.service.cipher.Decrypt(ciphertext)
	if err != nil || secret == "" {
		writeError(w, 503, "not_configured", "Webhook signing is not configured.")
		return
	}
	event, err := verifyEvent(body, r.Header.Get("Stripe-Signature"), secret)
	if err != nil {
		writeError(w, 400, "invalid_event", "Webhook signature or event is invalid.")
		return
	}
	if !subscriptionEvent(event.Type) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.service.enqueueEvent(r.Context(), org, r.PathValue("integrationID"), event, body); err != nil {
		writeError(w, 503, "unavailable", "Webhook could not be stored. Retry delivery.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func verifyEvent(body []byte, signature, secret string) (stripe.Event, error) {
	// Only the stable event envelope and resource ID are used. Resource state is
	// fetched with the SDK's API version, independent of the event's API version.
	event, err := webhook.ConstructEventWithOptions(body, signature, secret, webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return event, err
	}
	if event.ID == "" || event.Livemode || event.Account != "" {
		return event, errors.New("expected an account test event")
	}
	if subscriptionEvent(event.Type) {
		_, err = eventSubscriptionID(event)
	}
	return event, err
}

func subscriptionEvent(kind stripe.EventType) bool {
	switch kind {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		return true
	}
	return false
}

func eventSubscriptionID(event stripe.Event) (string, error) {
	var object struct {
		ID string `json:"id"`
	}
	if event.Data == nil {
		return "", errors.New("missing event data")
	}
	if err := json.Unmarshal(event.Data.Raw, &object); err != nil {
		return "", err
	}
	if object.ID == "" {
		return "", errors.New("missing subscription ID")
	}
	return object.ID, nil
}

func (s *Service) enqueueEvent(ctx context.Context, org, integrationID string, event stripe.Event, body []byte) error {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var eventID string
	err = tx.QueryRow(ctx, `INSERT INTO processed_events (organization_id,integration_id,external_event_id,event_type,payload,status)
 VALUES ($1,$2,$3,$4,$5::jsonb,'pending') ON CONFLICT (organization_id,external_event_id) DO NOTHING RETURNING id`, org, integrationID, event.ID, string(event.Type), string(body)).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO integration_jobs (organization_id,integration_id,event_id,kind,status) VALUES ($1,$2,$3,'stripe_event','pending')`, org, integrationID, eventID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
