package auth

import (
	"context"
	"net/http"
	"strings"
)

type identityContextKey struct{}

// RequireAuthentication validates the bearer token and attaches its trusted
// user and organization identity to the request context.
func (s *Service) RequireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "authentication_required", "A valid access token is required.")
			return
		}

		identity, err := s.Authenticate(r.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_access_token", "The access token is invalid or expired.")
			return
		}

		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok || (identity.Role != "owner" && identity.Role != "admin") {
			writeError(w, http.StatusForbidden, "permission_denied", "Owner access is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}
