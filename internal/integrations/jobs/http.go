package jobs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/arpitkuriyal/business-drift/internal/auth"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func GetHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		var id pgtype.UUID
		if id.Scan(r.PathValue("id")) != nil {
			http.Error(w, "Job not found", 404)
			return
		}
		job, err := Get(r.Context(), db, identity.OrganizationID, r.PathValue("id"))
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "Job not found", 404)
			return
		}
		if err != nil {
			http.Error(w, "Job unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(job)
	}
}
