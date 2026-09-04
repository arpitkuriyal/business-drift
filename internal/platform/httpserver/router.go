package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/findings"
	hubspotintegration "github.com/arpitkuriyal/business-drift/internal/integrations/hubspot"
	stripeintegration "github.com/arpitkuriyal/business-drift/internal/integrations/stripe"
	"github.com/arpitkuriyal/business-drift/internal/organizations"
	"github.com/arpitkuriyal/business-drift/internal/platform/database"
	"go.uber.org/zap"
)

func NewRouter(logger *zap.Logger, resources *database.Resources, stripeService *stripeintegration.Service, hubSpotService *hubspotintegration.Service) http.Handler {
	mux := http.NewServeMux()
	health := healthHandler{resources: resources}
	mux.HandleFunc("GET /health", health.live)
	mux.HandleFunc("GET /live", health.live)
	mux.HandleFunc("GET /ready", health.ready)

	authService := auth.NewService(resources.Postgres, resources.Redis)
	auth.NewHandler(authService).RegisterRoutes(mux)

	organizationHandler := organizations.NewHandler(organizations.NewRepository(resources.Postgres))
	mux.Handle(
		"GET /api/v1/organization",
		authService.RequireAuthentication(http.HandlerFunc(organizationHandler.Current)),
	)

	findingsHandler := findings.NewHandler(findings.NewRepository(resources.Postgres))
	mux.Handle(
		"GET /api/v1/findings",
		authService.RequireAuthentication(http.HandlerFunc(findingsHandler.List)),
	)
	mux.Handle(
		"GET /api/v1/findings/{id}",
		authService.RequireAuthentication(http.HandlerFunc(findingsHandler.Get)),
	)

	stripeHandler := stripeintegration.NewHandler(stripeService)
	mux.Handle(
		"POST /api/v1/integrations/stripe",
		authService.RequireAuthentication(auth.RequireOwner(http.HandlerFunc(stripeHandler.Save))),
	)
	mux.Handle(
		"GET /api/v1/integrations/stripe",
		authService.RequireAuthentication(http.HandlerFunc(stripeHandler.Get)),
	)
	mux.Handle(
		"POST /api/v1/integrations/stripe/sync",
		authService.RequireAuthentication(auth.RequireOwner(http.HandlerFunc(stripeHandler.Sync))),
	)

	hubSpotHandler := hubspotintegration.NewHandler(hubSpotService)
	mux.Handle(
		"POST /api/v1/integrations/hubspot",
		authService.RequireAuthentication(auth.RequireOwner(http.HandlerFunc(hubSpotHandler.Save))),
	)
	mux.Handle(
		"GET /api/v1/integrations/hubspot",
		authService.RequireAuthentication(http.HandlerFunc(hubSpotHandler.Get)),
	)
	mux.Handle(
		"POST /api/v1/integrations/hubspot/sync",
		authService.RequireAuthentication(auth.RequireOwner(http.HandlerFunc(hubSpotHandler.Sync))),
	)

	return requestID(requestLogger(logger, recoverPanic(logger, securityHeaders(mux))))
}

type healthHandler struct {
	resources *database.Resources
}

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

func (h healthHandler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h healthHandler) ready(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	response := healthResponse{Status: "ready", Checks: make(map[string]string)}
	for name, err := range h.resources.Check(r.Context()) {
		if err != nil {
			status = http.StatusServiceUnavailable
			response.Status = "not_ready"
			response.Checks[name] = "unavailable"
			continue
		}
		response.Checks[name] = "ok"
	}
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
