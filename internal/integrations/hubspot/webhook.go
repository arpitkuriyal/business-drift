package hubspotintegration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const maxWebhookBodyBytes = 1024 * 1024

var (
	errInvalidWebhook  = errors.New("invalid HubSpot webhook")
	errWebhookNotFound = errors.New("HubSpot integration not found")
)

type notification struct {
	EventID          int64  `json:"eventId"`
	SubscriptionType string `json:"subscriptionType"`
	EventType        string `json:"eventType"`
}

// ReceiveWebhook verifies the signed batch and records each notification with
// a reconciliation job in one transaction.
func (s *Service) ReceiveWebhook(r *http.Request) error {
	var integrationID pgtype.UUID
	if integrationID.Scan(r.PathValue("integrationID")) != nil {
		return errWebhookNotFound
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
	if err != nil || len(body) > maxWebhookBodyBytes {
		return errInvalidWebhook
	}
	var org, ciphertext string
	err = s.database.QueryRow(r.Context(), `SELECT organization_id, webhook_secret_ciphertext FROM integrations
	  WHERE id=$1 AND provider='hubspot' AND status <> 'disconnected'`, integrationID).Scan(&org, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return errWebhookNotFound
	}
	if err != nil {
		return err
	}
	secret, err := s.cipher.Decrypt(ciphertext)
	if err != nil || secret == "" || !verifyWebhook(r, body, secret) {
		return errInvalidWebhook
	}
	var rawEvents []json.RawMessage
	if json.Unmarshal(body, &rawEvents) != nil || len(rawEvents) == 0 || len(rawEvents) > 1000 {
		return errInvalidWebhook
	}

	tx, err := s.database.Begin(r.Context())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, raw := range rawEvents {
		var event notification
		if json.Unmarshal(raw, &event) != nil || event.EventID <= 0 {
			return errInvalidWebhook
		}
		typeName := event.SubscriptionType
		if typeName == "" {
			typeName = event.EventType
		}
		externalID := fmt.Sprintf("hubspot:%d", event.EventID)
		var eventID string
		err = tx.QueryRow(r.Context(), `INSERT INTO processed_events
   (organization_id,integration_id,external_event_id,event_type,payload,status)
   VALUES ($1,$2,$3,$4,$5::jsonb,'pending')
   ON CONFLICT (organization_id,external_event_id) DO NOTHING RETURNING id`,
			org, integrationID, externalID, typeName, string(raw)).Scan(&eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO integration_jobs
	   (organization_id,integration_id,event_id,kind,status) VALUES ($1,$2,$3,'hubspot_event','pending')`, org, integrationID, eventID); err != nil {
			return err
		}
	}
	return tx.Commit(r.Context())
}

func verifyWebhook(r *http.Request, body []byte, secret string) bool {
	timestamp := r.Header.Get("X-HubSpot-Request-Timestamp")
	provided := r.Header.Get("X-HubSpot-Signature-v3")
	stamp, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || provided == "" {
		return false
	}
	requestTime := time.UnixMilli(stamp)
	if delta := time.Since(requestTime); delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	message := r.Method + signedRequestURI(r) + string(body) + timestamp
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return len(want) == len(provided) && subtle.ConstantTimeCompare([]byte(want), []byte(provided)) == 1
}

func signedRequestURI(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}
