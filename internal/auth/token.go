package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	accessTokenLifetime  = 15 * time.Minute
	refreshTokenLifetime = 30 * 24 * time.Hour
	resetTokenLifetime   = 30 * time.Minute
)

// SessionPair contains the only copies of the usable tokens. PostgreSQL stores
// SHA-256 hashes, similar to how a password reset token should be stored.
type SessionPair struct {
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func newToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func tokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}
