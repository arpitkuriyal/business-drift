package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

const maxAuthBodyBytes = 16 * 1024

type Handler struct {
	service     *Service
	environment string
}

func NewHandler(service *Service, environment string) *Handler {
	return &Handler{service: service, environment: environment}
}

// RegisterRoutes keeps all authentication URLs together and makes it obvious
// which endpoints require an access token.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("POST /api/v1/auth/password/request-reset", h.requestPasswordReset)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)

	mux.Handle("GET /api/v1/auth/me", h.service.RequireAuthentication(http.HandlerFunc(h.me)))
	mux.Handle("POST /api/v1/auth/password/change", h.service.RequireAuthentication(http.HandlerFunc(h.changePassword)))
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var input RegisterInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	result, err := h.service.Register(r.Context(), input)
	if errors.Is(err, ErrInvalidInput) {
		writeError(w, http.StatusBadRequest, "invalid_registration", "Use a valid email, an organization name of 2–100 characters, and a password of 12–128 characters.")
		return
	}
	if errors.Is(err, ErrEmailExists) {
		writeError(w, http.StatusConflict, "email_exists", "This email is already registered.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Registration could not be completed.")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrganizationID string `json:"organization_id"`
		Email          string `json:"email"`
		Password       string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	allowed, err := h.service.AllowLogin(r.Context(), input.Email, clientIP(r))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "Login is temporarily unavailable.")
		return
	}
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Try again in one minute.")
		return
	}

	session, err := h.service.Login(r.Context(), input.OrganizationID, input.Email, input.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The email, organization, or password is incorrect.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Login could not be completed.")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	session, err := h.service.Refresh(r.Context(), input.RefreshToken)
	if errors.Is(err, ErrInvalidToken) {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The session could not be refreshed.")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.Logout(r.Context(), input.RefreshToken); errors.Is(err, ErrInvalidToken) {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Logout could not be completed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	identity, _ := IdentityFromContext(r.Context())
	writeJSON(w, http.StatusOK, identity)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	identity, _ := IdentityFromContext(r.Context())
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.ChangePassword(r.Context(), identity, input.CurrentPassword, input.NewPassword); errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrInvalidInput) {
		writeError(w, http.StatusBadRequest, "password_change_failed", "Check the current password and use a new password of 12–128 characters.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The password could not be changed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	token, err := h.service.RequestPasswordReset(r.Context(), input.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The reset request could not be completed.")
		return
	}

	response := map[string]string{"message": "If the account exists, password reset instructions are available."}
	// There is no email provider in Phase 1. Returning the token is strictly a
	// local demo aid and is impossible when APP_ENV is test or production.
	if h.environment == "development" && token != "" {
		response["development_reset_token"] = token
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.service.ResetPassword(r.Context(), input.Token, input.NewPassword); errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrInvalidInput) {
		writeError(w, http.StatusBadRequest, "password_reset_failed", "The reset token is invalid or expired, or the password does not meet requirements.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "The password could not be reset.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("request body must contain valid JSON with only supported fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
