package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// handleListOrgs handles GET /api/0/organizations/.
func handleListOrgs(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgs, err := catalog.ListOrganizations(r.Context())
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organizations.")
			return
		}
		page := Paginate(w, r, orgs)
		if page == nil {
			page = []Organization{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetOrg handles GET /api/0/organizations/{org_slug}/.
func handleGetOrg(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		rec, err := catalog.GetOrganization(r.Context(), slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rec)
	}
}
