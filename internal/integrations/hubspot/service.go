package hubspotintegration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/platform/encryption"
)

var (
	ErrNotFound      = errors.New("HubSpot integration not found")
	ErrInvalidSecret = errors.New("invalid HubSpot credentials")
)

type Integration struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    *string    `json:"last_error,omitempty"`
}

type SyncResult struct {
	Companies int `json:"companies"`
	Matched   int `json:"matched"`
	Findings  int `json:"findings"`
}

type companyRecord struct {
	ID         string
	Name       string
	Domain     string
	Status     string
	ObservedAt time.Time
}

type Service struct {
	database *pgxpool.Pool
	cipher   *encryption.Cipher
	client   *http.Client
	logger   *zap.Logger
}

func NewService(database *pgxpool.Pool, cipher *encryption.Cipher, logger *zap.Logger) *Service {
	return &Service{database: database, cipher: cipher, client: &http.Client{Timeout: 20 * time.Second}, logger: logger}
}

func (s *Service) Save(ctx context.Context, identity auth.Identity, token string) (Integration, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 500 || s.checkAccess(ctx, token) != nil {
		return Integration{}, ErrInvalidSecret
	}
	encryptedToken, err := s.cipher.Encrypt(token)
	if err != nil {
		return Integration{}, err
	}
	encryptedEmpty, err := s.cipher.Encrypt("")
	if err != nil {
		return Integration{}, err
	}
	var integration Integration
	err = s.database.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, provider, api_key_ciphertext, webhook_secret_ciphertext, status)
		VALUES ($1, 'hubspot', $2, $3, 'active')
		ON CONFLICT (organization_id, provider) DO UPDATE SET
			api_key_ciphertext = EXCLUDED.api_key_ciphertext,
			status = 'active', last_error = NULL, updated_at = now()
		RETURNING id, status, last_synced_at, last_error
	`, identity.OrganizationID, encryptedToken, encryptedEmpty).Scan(
		&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError,
	)
	return integration, err
}

func (s *Service) Get(ctx context.Context, organizationID string) (Integration, error) {
	var integration Integration
	err := s.database.QueryRow(ctx, `
		SELECT id, status, last_synced_at, last_error FROM integrations
		WHERE organization_id = $1 AND provider = 'hubspot'
	`, organizationID).Scan(&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	return integration, err
}

func (s *Service) Sync(ctx context.Context, identity auth.Identity) (SyncResult, error) {
	integration, token, err := s.load(ctx, identity.OrganizationID)
	if err != nil {
		return SyncResult{}, err
	}
	companies, err := s.listCompanies(ctx, token)
	if err != nil {
		s.recordSyncError(ctx, integration.ID, err)
		return SyncResult{}, err
	}
	result := SyncResult{Companies: len(companies)}
	for _, company := range companies {
		company.Status = normalizeStatus(company.Status)
		matched, finding, err := s.storeCompany(ctx, identity.OrganizationID, company)
		if err != nil {
			s.recordSyncError(ctx, integration.ID, err)
			return SyncResult{}, err
		}
		if matched {
			result.Matched++
		}
		if finding {
			result.Findings++
		}
	}
	_, err = s.database.Exec(ctx, `
		UPDATE integrations SET status = 'active', last_synced_at = now(), last_error = NULL, updated_at = now()
		WHERE id = $1
	`, integration.ID)
	return result, err
}

func (s *Service) load(ctx context.Context, organizationID string) (Integration, string, error) {
	var integration Integration
	var encryptedToken string
	err := s.database.QueryRow(ctx, `
		SELECT id, status, last_synced_at, last_error, api_key_ciphertext FROM integrations
		WHERE organization_id = $1 AND provider = 'hubspot'
	`, organizationID).Scan(
		&integration.ID, &integration.Status, &integration.LastSyncedAt, &integration.LastError, &encryptedToken,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, "", ErrNotFound
	}
	if err != nil {
		return Integration{}, "", err
	}
	token, err := s.cipher.Decrypt(encryptedToken)
	return integration, token, err
}

func (s *Service) recordSyncError(ctx context.Context, integrationID string, syncError error) {
	message := syncError.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	if _, err := s.database.Exec(ctx, `UPDATE integrations SET status = 'error', last_error = $1 WHERE id = $2`, message, integrationID); err != nil {
		s.logger.Error("record HubSpot sync error", zap.Error(err))
	}
}
