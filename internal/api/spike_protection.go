package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/controlplane"
)

// spikeProtectionRequest is the body for enabling/disabling spike protection.
type spikeProtectionRequest struct {
	Projects []string `json:"projects"` // list of project slugs
}

// handleEnableSpikeProtection handles POST /api/0/organizations/{slug}/spike-protections/.
// Enables spike protection for the specified projects (stored as a project setting).
func handleEnableSpikeProtection(
	db *sql.DB,
	catalog controlplane.CatalogStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		_, ok := getOrganizationFromCatalog(w, r, catalog, orgSlug)
		if !ok {
			return
		}

		var body spikeProtectionRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}

		for _, projSlug := range body.Projects {
			project, err := catalog.GetProject(r.Context(), orgSlug, projSlug)
			if err != nil || project == nil {
				continue
			}
			_, _ = db.ExecContext(r.Context(),
				`UPDATE projects SET spike_protection = 1 WHERE id = ?`, project.ID)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDisableSpikeProtection handles DELETE /api/0/organizations/{slug}/spike-protections/.
// Disables spike protection for the specified projects.
func handleDisableSpikeProtection(
	db *sql.DB,
	catalog controlplane.CatalogStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		_, ok := getOrganizationFromCatalog(w, r, catalog, orgSlug)
		if !ok {
			return
		}

		var body spikeProtectionRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}

		for _, projSlug := range body.Projects {
			project, err := catalog.GetProject(r.Context(), orgSlug, projSlug)
			if err != nil || project == nil {
				continue
			}
			_, _ = db.ExecContext(r.Context(),
				`UPDATE projects SET spike_protection = 0 WHERE id = ?`, project.ID)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
