package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
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

// handleUpdateOrg handles PUT /api/0/organizations/{org_slug}/.
func handleUpdateOrg(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		org, ok := getOrganizationFromCatalog(w, r, catalog, slug)
		if !ok {
			return
		}
		_ = org

		var body store.OrganizationUpdate
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}
		if strings.TrimSpace(body.Name) == "" && strings.TrimSpace(body.Slug) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "At least one of name or slug is required.")
			return
		}

		updated, err := catalog.UpdateOrganization(r.Context(), slug, body)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update organization.")
			return
		}
		if updated == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, updated)
	}
}

// environmentEntry is the JSON response shape for a single environment.
type environmentEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleListOrgEnvironments handles GET /api/0/organizations/{org_slug}/environments/.
func handleListOrgEnvironments(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		_, ok := getOrganizationFromCatalog(w, r, catalog, slug)
		if !ok {
			return
		}

		envNames, err := catalog.ListEnvironments(r.Context(), slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list environments.")
			return
		}

		out := make([]environmentEntry, 0, len(envNames))
		for _, name := range envNames {
			out = append(out, environmentEntry{
				ID:   envNameToID(name),
				Name: name,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// envNameToID produces a stable deterministic ID from an environment name.
func envNameToID(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x", h[:8])
}
