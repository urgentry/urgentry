package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/integration"
)

type sentryAppResponse struct {
	ID             string          `json:"id"`
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	Author         string          `json:"author,omitempty"`
	Overview       string          `json:"overview,omitempty"`
	Scopes         []string        `json:"scopes"`
	Events         []string        `json:"events,omitempty"`
	Schema         json.RawMessage `json:"schema"`
	AllowedOrigins []string        `json:"allowedOrigins,omitempty"`
	Status         string          `json:"status,omitempty"`
	RedirectURL    string          `json:"redirectUrl,omitempty"`
	WebhookURL     string          `json:"webhookUrl,omitempty"`
	IsAlertable    bool            `json:"isAlertable"`
	VerifyInstall  bool            `json:"verifyInstall"`
}

type sentryAppMutationRequest struct {
	Name           string           `json:"name"`
	Scopes         []string         `json:"scopes"`
	Author         *string          `json:"author"`
	Events         *[]string        `json:"events"`
	Schema         *json.RawMessage `json:"schema"`
	WebhookURL     *string          `json:"webhookUrl"`
	RedirectURL    *string          `json:"redirectUrl"`
	IsAlertable    *bool            `json:"isAlertable"`
	Overview       *string          `json:"overview"`
	VerifyInstall  *bool            `json:"verifyInstall"`
	AllowedOrigins *[]string        `json:"allowedOrigins"`
}

type sentryAppInstallationResponse struct {
	UUID   string            `json:"uuid"`
	App    sentryAppResponse `json:"app"`
	Status string            `json:"status"`
}

type sentryAppModel struct {
	ID             string
	Slug           string
	Name           string
	Author         string
	Overview       string
	Scopes         []string
	Events         []string
	Schema         json.RawMessage
	AllowedOrigins []string
	Status         string
	RedirectURL    string
	WebhookURL     string
	IsAlertable    bool
	VerifyInstall  bool
}

func handleListSentryApps(catalog controlplane.CatalogStore, registry *integration.Registry, appStore integration.AppStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		items, err := sentryAppResponses(r.Context(), registry, appStore)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Sentry apps.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleGetSentryApp(registry *integration.Registry, appStore integration.AppStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		app, err := resolveSentryApp(r.Context(), registry, appStore, PathParam(r, "sentry_app_id_or_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Sentry app.")
			return
		}
		if app == nil {
			httputil.WriteError(w, http.StatusNotFound, "Sentry app not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, app.response())
	}
}

func handleUpdateSentryApp(registry *integration.Registry, appStore integration.AppStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		current, err := resolveSentryApp(r.Context(), registry, appStore, PathParam(r, "sentry_app_id_or_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Sentry app.")
			return
		}
		if current == nil {
			httputil.WriteError(w, http.StatusNotFound, "Sentry app not found.")
			return
		}

		var body sentryAppMutationRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if strings.TrimSpace(body.Name) == "" || len(body.Scopes) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required fields: name and scopes.")
			return
		}

		next := *current
		next.Name = strings.TrimSpace(body.Name)
		next.Scopes = cleanedStringSlice(body.Scopes)
		if len(next.Scopes) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required fields: name and scopes.")
			return
		}
		if body.Author != nil {
			next.Author = strings.TrimSpace(*body.Author)
		}
		if body.Events != nil {
			next.Events = cleanedStringSlice(*body.Events)
		}
		if body.Schema != nil {
			schema, ok := normalizeRawJSON(*body.Schema)
			if !ok {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid schema payload.")
				return
			}
			next.Schema = schema
		}
		if body.WebhookURL != nil {
			next.WebhookURL = strings.TrimSpace(*body.WebhookURL)
		}
		if body.RedirectURL != nil {
			next.RedirectURL = strings.TrimSpace(*body.RedirectURL)
		}
		if body.IsAlertable != nil {
			next.IsAlertable = *body.IsAlertable
		}
		if body.Overview != nil {
			next.Overview = strings.TrimSpace(*body.Overview)
		}
		if body.VerifyInstall != nil {
			next.VerifyInstall = *body.VerifyInstall
		}
		if body.AllowedOrigins != nil {
			next.AllowedOrigins = cleanedStringSlice(*body.AllowedOrigins)
		}

		if err := appStore.Upsert(r.Context(), next.record()); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update Sentry app.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, next.response())
	}
}

func handleDeleteSentryApp(registry *integration.Registry, appStore integration.AppStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		appID := PathParam(r, "sentry_app_id_or_slug")
		current, err := resolveSentryApp(r.Context(), registry, appStore, appID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Sentry app.")
			return
		}
		if current == nil {
			httputil.WriteError(w, http.StatusNotFound, "Sentry app not found.")
			return
		}
		if err := appStore.Delete(r.Context(), firstNonEmptyText(current.ID, appID)); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete Sentry app.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListSentryAppInstallations(catalog controlplane.CatalogStore, registry *integration.Registry, appStore integration.AppStore, store integration.Store, auth authFunc) http.HandlerFunc {
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
		apps, err := sentryAppModelMap(r.Context(), registry, appStore)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Sentry apps.")
			return
		}
		out := make([]sentryAppInstallationResponse, 0, len(configs))
		for _, cfg := range configs {
			app, ok := apps[cfg.IntegrationID]
			if !ok {
				continue
			}
			out = append(out, sentryAppInstallationResponse{
				UUID:   cfg.ID,
				App:    app.response(),
				Status: cfg.Status,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

func sentryAppResponses(ctx context.Context, registry *integration.Registry, appStore integration.AppStore) ([]sentryAppResponse, error) {
	apps, err := sentryAppModelMap(ctx, registry, appStore)
	if err != nil {
		return nil, err
	}
	ordered := make([]sentryAppModel, 0, len(apps))
	for _, app := range apps {
		ordered = append(ordered, app)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Compare(ordered[i].Slug, ordered[j].Slug) < 0
	})
	out := make([]sentryAppResponse, 0, len(ordered))
	for _, app := range ordered {
		out = append(out, app.response())
	}
	return out, nil
}

func sentryAppModelMap(ctx context.Context, registry *integration.Registry, appStore integration.AppStore) (map[string]sentryAppModel, error) {
	models := map[string]sentryAppModel{}
	for _, impl := range registry.All() {
		model, err := sentryAppModelFromIntegration(impl)
		if err != nil {
			return nil, err
		}
		models[model.ID] = model
	}
	if appStore == nil {
		return models, nil
	}
	records, err := appStore.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		key := firstNonEmptyText(record.ID, record.Slug)
		if key == "" {
			continue
		}
		if record.Deleted() {
			delete(models, key)
			continue
		}
		models[key] = sentryAppModelFromRecord(record)
	}
	return models, nil
}

func resolveSentryApp(ctx context.Context, registry *integration.Registry, appStore integration.AppStore, idOrSlug string) (*sentryAppModel, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)
	if appStore != nil {
		record, err := appStore.Get(ctx, idOrSlug)
		if err != nil {
			return nil, err
		}
		if record != nil {
			if record.Deleted() {
				return nil, nil
			}
			model := sentryAppModelFromRecord(record)
			return &model, nil
		}
	}
	if impl := registry.Get(idOrSlug); impl != nil {
		model, err := sentryAppModelFromIntegration(impl)
		if err != nil {
			return nil, err
		}
		return &model, nil
	}
	return nil, nil
}

func sentryAppModelFromIntegration(app integration.Integration) (sentryAppModel, error) {
	scopes := make([]string, 0, len(app.ConfigSchema()))
	for _, field := range app.ConfigSchema() {
		if field.Required {
			scopes = append(scopes, field.Key)
		}
	}
	schema, err := json.Marshal(app.ConfigSchema())
	if err != nil {
		return sentryAppModel{}, err
	}
	return sentryAppModel{
		ID:            app.ID(),
		Slug:          app.ID(),
		Name:          app.Name(),
		Author:        "urgentry",
		Overview:      app.Description(),
		Scopes:        scopes,
		Events:        []string{"issue"},
		Schema:        json.RawMessage(schema),
		Status:        "published",
		IsAlertable:   true,
		VerifyInstall: true,
	}, nil
}

func sentryAppModelFromRecord(app *integration.AppRecord) sentryAppModel {
	return sentryAppModel{
		ID:             app.ID,
		Slug:           app.Slug,
		Name:           app.Name,
		Author:         app.Author,
		Overview:       app.Overview,
		Scopes:         append([]string(nil), app.Scopes...),
		Events:         append([]string(nil), app.Events...),
		Schema:         normalizedSchema(app.Schema),
		AllowedOrigins: append([]string(nil), app.AllowedOrigins...),
		Status:         app.Status,
		RedirectURL:    app.RedirectURL,
		WebhookURL:     app.WebhookURL,
		IsAlertable:    app.IsAlertable,
		VerifyInstall:  app.VerifyInstall,
	}
}

func (a sentryAppModel) response() sentryAppResponse {
	return sentryAppResponse{
		ID:             a.ID,
		Slug:           a.Slug,
		Name:           a.Name,
		Author:         a.Author,
		Overview:       a.Overview,
		Scopes:         append([]string(nil), a.Scopes...),
		Events:         append([]string(nil), a.Events...),
		Schema:         normalizedSchema(a.Schema),
		AllowedOrigins: append([]string(nil), a.AllowedOrigins...),
		Status:         a.Status,
		RedirectURL:    a.RedirectURL,
		WebhookURL:     a.WebhookURL,
		IsAlertable:    a.IsAlertable,
		VerifyInstall:  a.VerifyInstall,
	}
}

func (a sentryAppModel) record() *integration.AppRecord {
	return &integration.AppRecord{
		ID:             a.ID,
		Slug:           a.Slug,
		Name:           a.Name,
		Author:         a.Author,
		Overview:       a.Overview,
		Scopes:         append([]string(nil), a.Scopes...),
		Events:         append([]string(nil), a.Events...),
		Schema:         normalizedSchema(a.Schema),
		AllowedOrigins: append([]string(nil), a.AllowedOrigins...),
		Status:         firstNonEmptyText(a.Status, "published"),
		RedirectURL:    a.RedirectURL,
		WebhookURL:     a.WebhookURL,
		IsAlertable:    a.IsAlertable,
		VerifyInstall:  a.VerifyInstall,
	}
}

func cleanedStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeRawJSON(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage("null"), true
	}
	if !json.Valid(raw) {
		return nil, false
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(canonical), true
}

func normalizedSchema(raw json.RawMessage) json.RawMessage {
	if schema, ok := normalizeRawJSON(raw); ok {
		return schema
	}
	return json.RawMessage("null")
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
