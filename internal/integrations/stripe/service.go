package stripeintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/platform/encryption"
)

const jobQueue = "business-drift:stripe-jobs"

var (
	ErrNotFound      = errors.New("stripe integration not found")
	ErrInvalidSecret = errors.New("invalid Stripe sandbox credentials")
)

type Integration struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    *string    `json:"last_error,omitempty"`
	WebhookPath  string     `json:"webhook_path"`
}

// Service owns the Stripe trust boundary: encrypted credentials, webhook
// acceptance, durable jobs, Stripe API calls, and normalized persistence.
type Service struct {
	database *pgxpool.Pool
	redis    *redis.Client
	cipher   *encryption.Cipher
	logger   *zap.Logger
}

func NewService(database *pgxpool.Pool, redisClient *redis.Client, cipher *encryption.Cipher, logger *zap.Logger) *Service {
	return &Service{database: database, redis: redisClient, cipher: cipher, logger: logger}
}

func (s *Service) Save(ctx context.Context, identity auth.Identity, apiKey, webhookSecret string) (Integration, error) {
	apiKey = strings.TrimSpace(apiKey)
	webhookSecret = strings.TrimSpace(webhookSecret)
	// Phase 3 intentionally accepts only sandbox/restricted test keys so a demo
	// cannot accidentally read a real Stripe account.
	if (!strings.HasPrefix(apiKey, "sk_test_") && !strings.HasPrefix(apiKey, "rk_test_")) || !strings.HasPrefix(webhookSecret, "whsec_") {
		return Integration{}, ErrInvalidSecret
	}

	encryptedAPIKey, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return Integration{}, err
	}
	encryptedWebhookSecret, err := s.cipher.Encrypt(webhookSecret)
	if err != nil {
		return Integration{}, err
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return Integration{}, fmt.Errorf("start Stripe configuration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var integration Integration
	err = tx.QueryRow(ctx, `
		INSERT INTO integrations (
			organization_id, provider, api_key_ciphertext, webhook_secret_ciphertext, status
		)
		VALUES ($1, 'stripe', $2, $3, 'active')
		ON CONFLICT (organization_id, provider)
		DO UPDATE SET
			api_key_ciphertext = EXCLUDED.api_key_ciphertext,
			webhook_secret_ciphertext = EXCLUDED.webhook_secret_ciphertext,
			status = 'active', last_error = NULL, updated_at = now()
		RETURNING id, status, last_synced_at, last_error
	`, identity.OrganizationID, encryptedAPIKey, encryptedWebhookSecret).Scan(
		&integration.ID,
		&integration.Status,
		&integration.LastSyncedAt,
		&integration.LastError,
	)
	if err != nil {
		return Integration{}, fmt.Errorf("save Stripe integration: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, user_id, action, target_type, target_id)
		VALUES ($1, $2, 'integration.stripe_configured', 'integration', $3)
	`, identity.OrganizationID, identity.UserID, integration.ID); err != nil {
		return Integration{}, fmt.Errorf("audit Stripe configuration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Integration{}, fmt.Errorf("finish Stripe configuration: %w", err)
	}
	integration.WebhookPath = "/api/v1/webhooks/stripe/" + integration.ID
	return integration, nil
}

func (s *Service) Get(ctx context.Context, organizationID string) (Integration, error) {
	var integration Integration
	err := s.database.QueryRow(ctx, `
		SELECT id, status, last_synced_at, last_error
		FROM integrations
		WHERE organization_id = $1 AND provider = 'stripe'
	`, organizationID).Scan(&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, fmt.Errorf("read Stripe integration: %w", err)
	}
	integration.WebhookPath = "/api/v1/webhooks/stripe/" + integration.ID
	return integration, nil
}

func (s *Service) EnqueueSync(ctx context.Context, identity auth.Identity) (string, error) {
	integration, err := s.Get(ctx, identity.OrganizationID)
	if err != nil {
		return "", err
	}

	var jobID string
	err = s.database.QueryRow(ctx, `
		INSERT INTO integration_jobs (organization_id, integration_id, kind, status)
		VALUES ($1, $2, 'stripe_sync', 'pending')
		ON CONFLICT (integration_id, kind)
			WHERE kind = 'stripe_sync' AND status IN ('pending', 'processing')
		DO NOTHING
		RETURNING id
	`, identity.OrganizationID, integration.ID).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.database.QueryRow(ctx, `
			SELECT id FROM integration_jobs
			WHERE integration_id = $1 AND kind = 'stripe_sync'
			  AND status IN ('pending', 'processing')
		`, integration.ID).Scan(&jobID)
	}
	if err != nil {
		return "", fmt.Errorf("create Stripe sync job: %w", err)
	}

	// Failure to notify Redis is safe: the worker also polls PostgreSQL.
	if err := s.redis.LPush(ctx, jobQueue, jobID).Err(); err != nil {
		s.logger.Warn("Stripe job notification failed; database polling will recover it", zap.Error(err))
	}
	return jobID, nil
}

func (s *Service) loadSecrets(ctx context.Context, integrationID string) (organizationID, apiKey, webhookSecret string, err error) {
	var encryptedAPIKey, encryptedWebhookSecret string
	err = s.database.QueryRow(ctx, `
		SELECT organization_id, api_key_ciphertext, webhook_secret_ciphertext
		FROM integrations
		WHERE id = $1 AND provider = 'stripe' AND status <> 'disconnected'
	`, integrationID).Scan(&organizationID, &encryptedAPIKey, &encryptedWebhookSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
		return
	}
	if err != nil {
		err = fmt.Errorf("read Stripe secrets: %w", err)
		return
	}
	apiKey, err = s.cipher.Decrypt(encryptedAPIKey)
	if err != nil {
		return
	}
	webhookSecret, err = s.cipher.Decrypt(encryptedWebhookSecret)
	return
}
