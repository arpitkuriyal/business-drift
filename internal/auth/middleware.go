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

// RequireRoles performs server-side authorization after authentication. Hiding
// a button in the web app is never an authorization control.
func RequireRoles(allowedRoles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedRoles))
	for _, role := range allowedRoles {
		allowed[role] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromContext(r.Context())
			if !ok || !allowed[identity.Role] {
				writeError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}
