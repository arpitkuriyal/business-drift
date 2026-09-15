package stripeintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/platform/encryption"
)

var (
	ErrNotFound      = errors.New("Stripe integration not found")
	ErrInvalidSecret = errors.New("invalid Stripe test key")
)

type Integration struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    *string    `json:"last_error,omitempty"`
}

type SyncResult struct {
	Customers     int `json:"customers"`
	Subscriptions int `json:"subscriptions"`
}

type Service struct {
	database  *pgxpool.Pool
	cipher    *encryption.Cipher
	newClient func(string) *stripe.Client
}

func NewService(database *pgxpool.Pool, cipher *encryption.Cipher) *Service {
	return &Service{database: database, cipher: cipher, newClient: func(key string) *stripe.Client { return stripe.NewClient(key) }}
}

func (s *Service) Save(ctx context.Context, identity auth.Identity, apiKey string, webhookSecrets ...string) (Integration, error) {
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, "sk_test_") && !strings.HasPrefix(apiKey, "rk_test_") {
		return Integration{}, ErrInvalidSecret
	}
	encryptedKey, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return Integration{}, err
	}
	secret := ""
	if len(webhookSecrets) > 0 {
		secret = strings.TrimSpace(webhookSecrets[0])
	}
	if secret != "" && (!strings.HasPrefix(secret, "whsec_") || len(secret) <= 6 || len(secret) > 500) {
		return Integration{}, ErrInvalidSecret
	}
	encryptedSecret, err := s.cipher.Encrypt(secret)
	if err != nil {
		return Integration{}, err
	}
	var integration Integration
	err = s.database.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, provider, api_key_ciphertext, webhook_secret_ciphertext, status)
		VALUES ($1, 'stripe', $2, $3, 'active')
		ON CONFLICT (organization_id, provider) DO UPDATE SET
			api_key_ciphertext = EXCLUDED.api_key_ciphertext,
			webhook_secret_ciphertext = CASE WHEN $4 THEN EXCLUDED.webhook_secret_ciphertext ELSE integrations.webhook_secret_ciphertext END,
			status = 'active', last_error = NULL, updated_at = now()
		RETURNING id, status, last_synced_at, last_error
	`, identity.OrganizationID, encryptedKey, encryptedSecret, secret != "").Scan(
		&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError,
	)
	return integration, err
}

func (s *Service) Get(ctx context.Context, organizationID string) (Integration, error) {
	var integration Integration
	err := s.database.QueryRow(ctx, `
		SELECT id, status, last_synced_at, last_error FROM integrations
		WHERE organization_id = $1 AND provider = 'stripe'
	`, organizationID).Scan(&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	return integration, err
}

func (s *Service) Sync(ctx context.Context, identity auth.Identity) (SyncResult, error) {
	integration, key, err := s.load(ctx, identity.OrganizationID)
	if err != nil {
		return SyncResult{}, err
	}
	result, err := s.syncAll(ctx, identity.OrganizationID, key)
	if err != nil {
		message := err.Error()
		if len(message) > 500 {
			message = message[:500]
		}
		_, _ = s.database.Exec(ctx, `UPDATE integrations SET status = 'error', last_error = $1 WHERE id = $2`, message, integration.ID)
		return SyncResult{}, err
	}
	_, err = s.database.Exec(ctx, `
		UPDATE integrations SET status = 'active', last_synced_at = now(), last_error = NULL, updated_at = now()
		WHERE id = $1
	`, integration.ID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("finish Stripe sync: %w", err)
	}
	return result, nil
}

func (s *Service) load(ctx context.Context, organizationID string) (Integration, string, error) {
	var integration Integration
	var encryptedKey string
	err := s.database.QueryRow(ctx, `
		SELECT id, status, last_synced_at, last_error, api_key_ciphertext FROM integrations
		WHERE organization_id = $1 AND provider = 'stripe'
	`, organizationID).Scan(
		&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError, &encryptedKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, "", ErrNotFound
	}
	if err != nil {
		return Integration{}, "", err
	}
	key, err := s.cipher.Decrypt(encryptedKey)
	return integration, key, err
}
