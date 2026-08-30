package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidCredentials = errors.New("invalid email, organization, or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrEmailExists        = errors.New("email is already registered")
	ErrRateLimited        = errors.New("too many login attempts")
	ErrInvalidInput       = errors.New("invalid input")
)

// Identity is the authenticated tenant context attached to a request.
// Repositories should use OrganizationID from here, never from user input.
type Identity struct {
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
}

type RegisterInput struct {
	OrganizationName string `json:"organization_name"`
	Email            string `json:"email"`
	Password         string `json:"password"`
}

type RegisterResult struct {
	Identity Identity    `json:"identity"`
	Session  SessionPair `json:"session"`
}

// Service owns authentication rules. HTTP handlers only decode requests and
// translate these errors into safe client responses.
type Service struct {
	database *pgxpool.Pool
	redis    *redis.Client
}

func NewService(database *pgxpool.Pool, redisClient *redis.Client) *Service {
	return &Service{database: database, redis: redisClient}
}

// Register creates a new organization and its first owner in one transaction.
func (s *Service) Register(ctx context.Context, input RegisterInput) (RegisterResult, error) {
	input.OrganizationName = strings.TrimSpace(input.OrganizationName)
	input.Email = normalizeEmail(input.Email)
	if len(input.OrganizationName) < 2 || len(input.OrganizationName) > 100 || !validEmail(input.Email) || !validPassword(input.Password) {
		return RegisterResult{}, ErrInvalidInput
	}

	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return RegisterResult{}, err
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("start registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationID string
	if err := tx.QueryRow(ctx,
		"INSERT INTO organizations (name) VALUES ($1) RETURNING id",
		input.OrganizationName,
	).Scan(&organizationID); err != nil {
		return RegisterResult{}, fmt.Errorf("create organization: %w", err)
	}

	var userID string
	if err := tx.QueryRow(ctx,
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id",
		input.Email,
		passwordHash,
	).Scan(&userID); err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return RegisterResult{}, ErrEmailExists
		}
		return RegisterResult{}, fmt.Errorf("create user: %w", err)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, 'owner')",
		organizationID,
		userID,
	); err != nil {
		return RegisterResult{}, fmt.Errorf("create owner membership: %w", err)
	}

	session, err := issueSessionPair(ctx, tx, organizationID, userID, "")
	if err != nil {
		return RegisterResult{}, err
	}
	if err := addAuditEvent(ctx, tx, organizationID, userID, "organization.registered", "organization", organizationID); err != nil {
		return RegisterResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RegisterResult{}, fmt.Errorf("finish registration: %w", err)
	}

	return RegisterResult{
		Identity: Identity{UserID: userID, OrganizationID: organizationID, Email: input.Email, Role: "owner"},
		Session:  session,
	}, nil
}

// Login returns the same generic error for every failed credential check so an
// attacker cannot discover registered emails or organization memberships.
func (s *Service) Login(ctx context.Context, organizationID, email, password string) (SessionPair, error) {
	email = normalizeEmail(email)
	var userID, passwordHash string
	err := s.database.QueryRow(ctx, `
		SELECT u.id, u.password_hash
		FROM users u
		JOIN memberships m ON m.user_id = u.id
		WHERE u.email = $1 AND m.organization_id = $2
	`, email, organizationID).Scan(&userID, &passwordHash)
	if err != nil || !passwordMatches(password, passwordHash) {
		return SessionPair{}, ErrInvalidCredentials
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return SessionPair{}, fmt.Errorf("start login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	session, err := issueSessionPair(ctx, tx, organizationID, userID, "")
	if err != nil {
		return SessionPair{}, err
	}
	if err := addAuditEvent(ctx, tx, organizationID, userID, "auth.login", "user", userID); err != nil {
		return SessionPair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionPair{}, fmt.Errorf("finish login: %w", err)
	}
	return session, nil
}

// Refresh rotates both tokens. Reusing an old refresh token revokes its whole
// token family, limiting damage if a refresh token is stolen.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (SessionPair, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return SessionPair{}, fmt.Errorf("start token refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var familyID, organizationID, userID string
	var expiresAt time.Time
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT family_id, organization_id, user_id, expires_at, revoked_at
		FROM sessions
		WHERE token_hash = $1 AND token_kind = 'refresh'
		FOR UPDATE
	`, tokenHash(refreshToken)).Scan(&familyID, &organizationID, &userID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionPair{}, ErrInvalidToken
	}
	if err != nil {
		return SessionPair{}, fmt.Errorf("read refresh token: %w", err)
	}
	if expiresAt.Before(time.Now()) {
		return SessionPair{}, ErrInvalidToken
	}

	if revokedAt != nil {
		_, _ = tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL", familyID)
		_ = tx.Commit(ctx)
		return SessionPair{}, ErrInvalidToken
	}

	if _, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL", familyID); err != nil {
		return SessionPair{}, fmt.Errorf("rotate old session: %w", err)
	}
	session, err := issueSessionPair(ctx, tx, organizationID, userID, familyID)
	if err != nil {
		return SessionPair{}, err
	}
	if err := addAuditEvent(ctx, tx, organizationID, userID, "auth.token_refreshed", "user", userID); err != nil {
		return SessionPair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionPair{}, fmt.Errorf("finish token refresh: %w", err)
	}
	return session, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start logout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var familyID, organizationID, userID string
	err = tx.QueryRow(ctx, `
		SELECT family_id, organization_id, user_id
		FROM sessions
		WHERE token_hash = $1 AND token_kind = 'refresh'
		FOR UPDATE
	`, tokenHash(refreshToken)).Scan(&familyID, &organizationID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return fmt.Errorf("read logout session: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL", familyID); err != nil {
		return fmt.Errorf("revoke logout session: %w", err)
	}
	if err := addAuditEvent(ctx, tx, organizationID, userID, "auth.logout", "user", userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (Identity, error) {
	var identity Identity
	err := s.database.QueryRow(ctx, `
		SELECT u.id, s.organization_id, u.email, m.role
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN memberships m ON m.user_id = s.user_id AND m.organization_id = s.organization_id
		WHERE s.token_hash = $1
		  AND s.token_kind = 'access'
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
	`, tokenHash(accessToken)).Scan(
		&identity.UserID,
		&identity.OrganizationID,
		&identity.Email,
		&identity.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrInvalidToken
	}
	if err != nil {
		return Identity{}, fmt.Errorf("authenticate token: %w", err)
	}
	return identity, nil
}

func (s *Service) ChangePassword(ctx context.Context, identity Identity, currentPassword, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrInvalidInput
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start password change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentHash string
	if err := tx.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1 FOR UPDATE", identity.UserID).Scan(&currentHash); err != nil {
		return ErrInvalidCredentials
	}
	if !passwordMatches(currentPassword, currentHash) {
		return ErrInvalidCredentials
	}

	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2", newHash, identity.UserID); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL", identity.UserID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := addAuditEvent(ctx, tx, identity.OrganizationID, identity.UserID, "auth.password_changed", "user", identity.UserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestPasswordReset always succeeds from the caller's perspective. Returning
// an empty token for an unknown email prevents account enumeration.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) (string, error) {
	email = normalizeEmail(email)
	var userID string
	if err := s.database.QueryRow(ctx, "SELECT id FROM users WHERE email = $1", email).Scan(&userID); errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("find reset user: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.database.Exec(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash(token), time.Now().Add(resetTokenLifetime))
	if err != nil {
		return "", fmt.Errorf("store reset token: %w", err)
	}
	return token, nil
}

func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrInvalidInput
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID string
	err = tx.QueryRow(ctx, `
		SELECT user_id FROM password_reset_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		FOR UPDATE
	`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return fmt.Errorf("read reset token: %w", err)
	}

	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2", newHash, userID); err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	// Consuming every outstanding reset token prevents an older email link from
	// changing the password again after a successful reset.
	if _, err := tx.Exec(ctx, "UPDATE password_reset_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL", userID); err != nil {
		return fmt.Errorf("consume reset token: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL", userID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := addAuditEvent(ctx, tx, "", userID, "auth.password_reset", "user", userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AllowLogin uses a hashed key so email addresses and IP addresses are not
// stored as readable Redis keys.
func (s *Service) AllowLogin(ctx context.Context, email, clientIP string) (bool, error) {
	rawKey := strings.ToLower(strings.TrimSpace(email)) + "|" + clientIP
	keyHash := sha256.Sum256([]byte(rawKey))
	key := "login-rate:" + hex.EncodeToString(keyHash[:])

	count, err := s.redis.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check login rate: %w", err)
	}
	if count == 1 {
		if err := s.redis.Expire(ctx, key, time.Minute).Err(); err != nil {
			return false, fmt.Errorf("set login rate window: %w", err)
		}
	}
	return count <= 5, nil
}

func issueSessionPair(ctx context.Context, tx pgx.Tx, organizationID, userID, familyID string) (SessionPair, error) {
	accessToken, err := newToken()
	if err != nil {
		return SessionPair{}, err
	}
	refreshToken, err := newToken()
	if err != nil {
		return SessionPair{}, err
	}
	if familyID == "" {
		if err := tx.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&familyID); err != nil {
			return SessionPair{}, fmt.Errorf("create session family: %w", err)
		}
	}

	now := time.Now()
	pair := SessionPair{
		AccessToken:      accessToken,
		AccessExpiresAt:  now.Add(accessTokenLifetime),
		RefreshToken:     refreshToken,
		RefreshExpiresAt: now.Add(refreshTokenLifetime),
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (family_id, organization_id, user_id, token_hash, token_kind, expires_at)
		VALUES
			($1, $2, $3, $4, 'access', $5),
			($1, $2, $3, $6, 'refresh', $7)
	`, familyID, organizationID, userID, tokenHash(accessToken), pair.AccessExpiresAt, tokenHash(refreshToken), pair.RefreshExpiresAt)
	if err != nil {
		return SessionPair{}, fmt.Errorf("store session: %w", err)
	}
	return pair, nil
}

func addAuditEvent(ctx context.Context, tx pgx.Tx, organizationID, userID, action, targetType, targetID string) error {
	var orgValue any
	if organizationID != "" {
		orgValue = organizationID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, user_id, action, target_type, target_id)
		VALUES ($1, $2, $3, $4, $5)
	`, orgValue, userID, action, targetType, targetID)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

func validPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 128
}
