package api

import (
	"net/http"
	"sort"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/integration"
)

type sentryAppResponse struct {
	ID          string                    `json:"id"`
	Slug        string                    `json:"slug"`
	Name        string                    `json:"name"`
	Author      string                    `json:"author"`
	Overview    string                    `json:"overview"`
	Scopes      []string                  `json:"scopes"`
	Schema      []integration.ConfigField `json:"schema"`
	IsAlertable bool                      `json:"isAlertable"`
}

type sentryAppInstallationResponse struct {
	UUID   string            `json:"uuid"`
	App    sentryAppResponse `json:"app"`
	Status string            `json:"status"`
}

func handleListSentryApps(catalog controlplane.CatalogStore, registry *integration.Registry, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		items := sentryAppResponses(registry)
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleGetSentryApp(registry *integration.Registry, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		app := registry.Get(PathParam(r, "sentry_app_id_or_slug"))
		if app == nil {
			httputil.WriteError(w, http.StatusNotFound, "Sentry app not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, sentryAppFromIntegration(app))
	}
}

func handleListSentryAppInstallations(catalog controlplane.CatalogStore, registry *integration.Registry, store integration.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		configs, err := store.ListByOrganization(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Sentry app installations.")
			return
		}
		out := make([]sentryAppInstallationResponse, 0, len(configs))
		for _, cfg := range configs {
			app := registry.Get(cfg.IntegrationID)
			if app == nil {
				continue
			}
			out = append(out, sentryAppInstallationResponse{
				UUID:   cfg.ID,
				App:    sentryAppFromIntegration(app),
				Status: cfg.Status,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

func sentryAppResponses(registry *integration.Registry) []sentryAppResponse {
	all := registry.All()
	sort.Slice(all, func(i, j int) bool {
		return strings.Compare(all[i].ID(), all[j].ID()) < 0
	})
	out := make([]sentryAppResponse, 0, len(all))
	for _, app := range all {
		out = append(out, sentryAppFromIntegration(app))
	}
	return out
}

func sentryAppFromIntegration(app integration.Integration) sentryAppResponse {
	scopes := make([]string, 0, len(app.ConfigSchema()))
	for _, field := range app.ConfigSchema() {
		if field.Required {
			scopes = append(scopes, field.Key)
		}
	}
	return sentryAppResponse{
		ID:          app.ID(),
		Slug:        app.ID(),
		Name:        app.Name(),
		Author:      "urgentry",
		Overview:    app.Description(),
		Scopes:      scopes,
		Schema:      app.ConfigSchema(),
		IsAlertable: true,
	}
}
