package api

import (
	"context"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	sharedstore "urgentry/internal/store"
)

type catalogContextKey struct{}

func withCatalogContext(catalog controlplane.CatalogStore, next http.Handler) http.Handler {
	if catalog == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), catalogContextKey{}, catalog)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func catalogFromRequest(r *http.Request) controlplane.CatalogStore {
	if r == nil {
		return nil
	}
	catalog, _ := r.Context().Value(catalogContextKey{}).(controlplane.CatalogStore)
	return catalog
}

func getOrganizationFromCatalog(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, slug string) (*sharedstore.Organization, bool) {
	if catalog == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Control-plane catalog unavailable.")
		return nil, false
	}
	org, err := catalog.GetOrganization(r.Context(), slug)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
		return nil, false
	}
	if org == nil {
		httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
		return nil, false
	}
	return org, true
}

func getProjectFromCatalog(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, orgSlug, projectSlug string) (*sharedstore.Project, bool) {
	if catalog == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Control-plane catalog unavailable.")
		return nil, false
	}
	project, err := catalog.GetProject(r.Context(), orgSlug, projectSlug)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project.")
		return nil, false
	}
	if project == nil {
		httputil.WriteError(w, http.StatusNotFound, "Project not found.")
		return nil, false
	}
	return project, true
}

func resolveProjectIDWithCatalog(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore) (string, bool) {
	project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
	if !ok {
		return "", false
	}
	return project.ID, true
}
