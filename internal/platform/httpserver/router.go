package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/audit"
	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/arpitkuriyal/business-drift/internal/findings"
	"github.com/arpitkuriyal/business-drift/internal/fixtures"
	stripeintegration "github.com/arpitkuriyal/business-drift/internal/integrations/stripe"
	"github.com/arpitkuriyal/business-drift/internal/organizations"
	"github.com/arpitkuriyal/business-drift/internal/platform/database"
	"go.uber.org/zap"
)

func NewRouter(logger *zap.Logger, resources *database.Resources, environment string, stripeService *stripeintegration.Service) http.Handler {
	mux := http.NewServeMux()
	health := healthHandler{resources: resources}
	mux.HandleFunc("GET /health", health.live)
	mux.HandleFunc("GET /live", health.live)
	mux.HandleFunc("GET /ready", health.ready)

	authService := auth.NewService(resources.Postgres, resources.Redis)
	auth.NewHandler(authService, environment).RegisterRoutes(mux)

	organizationHandler := organizations.NewHandler(organizations.NewRepository(resources.Postgres))
	mux.Handle(
		"GET /api/v1/organization",
		authService.RequireAuthentication(http.HandlerFunc(organizationHandler.Current)),
	)

	auditHandler := audit.NewHandler(audit.NewRepository(resources.Postgres))
	mux.Handle(
		"GET /api/v1/audit-events",
		authService.RequireAuthentication(
			auth.RequireRoles("owner", "admin")(http.HandlerFunc(auditHandler.List)),
		),
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

	// Fixture ingestion is a learning/demo tool and must never exist in the
	// production route table.
	if environment == "development" || environment == "test" {
		fixtureHandler := fixtures.NewHandler(fixtures.NewService(resources.Postgres))
		mux.Handle(
			"POST /api/v1/dev/fixture-events",
			authService.RequireAuthentication(
				auth.RequireRoles("owner", "admin")(http.HandlerFunc(fixtureHandler.Ingest)),
			),
		)
	}

	stripeHandler := stripeintegration.NewHandler(stripeService)
	mux.Handle(
		"POST /api/v1/integrations/stripe",
		authService.RequireAuthentication(
			auth.RequireRoles("owner", "admin")(http.HandlerFunc(stripeHandler.Save)),
		),
	)
	mux.Handle(
		"GET /api/v1/integrations/stripe",
		authService.RequireAuthentication(http.HandlerFunc(stripeHandler.Get)),
	)
	mux.Handle(
		"POST /api/v1/integrations/stripe/sync",
		authService.RequireAuthentication(
			auth.RequireRoles("owner", "admin")(http.HandlerFunc(stripeHandler.Sync)),
		),
	)
	mux.HandleFunc("POST /api/v1/webhooks/stripe/{integrationID}", stripeHandler.Webhook)

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
