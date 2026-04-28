package api

import (
	"errors"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/integration"
)

// integrationSummary is the JSON shape returned when listing integrations.
type integrationSummary struct {
	ID          string                           `json:"id"`
	Name        string                           `json:"name"`
	Description string                           `json:"description"`
	Schema      []integration.ConfigField        `json:"configSchema"`
	Installed   []*integration.IntegrationConfig `json:"installed,omitempty"`
}

type integrationDetailResponse struct {
	ID                            string            `json:"id"`
	Name                          string            `json:"name"`
	Icon                          *string           `json:"icon"`
	DomainName                    *string           `json:"domainName"`
	AccountType                   *string           `json:"accountType"`
	Scopes                        []string          `json:"scopes"`
	Status                        string            `json:"status"`
	Provider                      map[string]any    `json:"provider"`
	ConfigOrganization            []any             `json:"configOrganization"`
	ConfigData                    map[string]string `json:"configData"`
	ExternalID                    string            `json:"externalId"`
	OrganizationID                int               `json:"organizationId"`
	OrganizationIntegrationStatus string            `json:"organizationIntegrationStatus"`
	GracePeriodEnd                *string           `json:"gracePeriodEnd"`
}

// handleListIntegrations handles GET /api/0/organizations/{org_slug}/integrations/.
// It returns every registered integration together with any installed configs
// for the requesting organization.
func handleListIntegrations(
	catalog controlplane.CatalogStore,
	registry *integration.Registry,
	store integration.Store,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}

		installed, err := store.ListByOrganization(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list installed integrations.")
			return
		}
		byIntegration := make(map[string][]*integration.IntegrationConfig)
		for _, c := range installed {
			byIntegration[c.IntegrationID] = append(byIntegration[c.IntegrationID], c)
		}

		all := registry.All()
		out := make([]integrationSummary, 0, len(all))
		for _, i := range all {
			out = append(out, integrationSummary{
				ID:          i.ID(),
				Name:        i.Name(),
				Description: i.Description(),
				Schema:      i.ConfigSchema(),
				Installed:   byIntegration[i.ID()],
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleGetIntegration handles GET /api/0/organizations/{org_slug}/integrations/{integration_id}/.
func handleGetIntegration(
	catalog controlplane.CatalogStore,
	registry *integration.Registry,
	store integration.Store,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}

		config, err := store.Get(r.Context(), PathParam(r, "integration_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load integration config.")
			return
		}
		if config == nil || config.OrganizationID != org.ID {
			httputil.WriteError(w, http.StatusNotFound, "Integration not found.")
			return
		}
		impl := registry.Get(config.IntegrationID)
		if impl == nil {
			httputil.WriteError(w, http.StatusNotFound, "Integration not found.")
			return
		}

		provider := map[string]any{
			"key":        impl.ID(),
			"slug":       impl.ID(),
			"name":       impl.Name(),
			"canAdd":     true,
			"canDisable": true,
			"features":   []string{},
		}
		resp := integrationDetailResponse{
			ID:                            config.ID,
			Name:                          impl.Name(),
			Icon:                          nil,
			DomainName:                    nil,
			AccountType:                   nil,
			Scopes:                        []string{},
			Status:                        config.Status,
			Provider:                      provider,
			ConfigOrganization:            []any{},
			ConfigData:                    config.Config,
			ExternalID:                    config.IntegrationID,
			OrganizationID:                0,
			OrganizationIntegrationStatus: config.Status,
			GracePeriodEnd:                nil,
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

// installIntegrationRequest is the JSON body for installing an integration.
type installIntegrationRequest struct {
	Config    map[string]string `json:"config"`
	ProjectID string            `json:"projectId,omitempty"`
}

// handleInstallIntegration handles POST /api/0/organizations/{org_slug}/integrations/{integration_id}/install.
func handleInstallIntegration(
	catalog controlplane.CatalogStore,
	registry *integration.Registry,
	store integration.Store,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		integrationID := PathParam(r, "integration_id")
		impl := registry.Get(integrationID)
		if impl == nil {
			httputil.WriteError(w, http.StatusNotFound, "Unknown integration.")
			return
		}

		var body installIntegrationRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}

		// Validate required config fields.
		for _, field := range impl.ConfigSchema() {
			if field.Required && body.Config[field.Key] == "" {
				httputil.WriteError(w, http.StatusBadRequest, "Missing required field: "+field.Key)
				return
			}
		}

		cfg := &integration.IntegrationConfig{
			OrganizationID: org.ID,
			IntegrationID:  integrationID,
			ProjectID:      body.ProjectID,
			Config:         body.Config,
			Status:         "active",
		}
		if err := store.Create(r.Context(), cfg); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to install integration.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, cfg)
	}
}

// handleUninstallIntegration handles DELETE /api/0/organizations/{org_slug}/integrations/{integration_id}/.
func handleUninstallIntegration(
	catalog controlplane.CatalogStore,
	store integration.Store,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		configID := PathParam(r, "integration_id")
		existing, err := store.Get(r.Context(), configID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to look up integration config.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Integration config not found.")
			return
		}
		if err := store.Delete(r.Context(), configID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to uninstall integration.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleIntegrationWebhook handles POST /api/0/organizations/{org_slug}/integrations/{integration_id}/webhook.
// This endpoint does NOT require auth — it is the inbound receiver for
// external services (GitHub, PagerDuty, etc.).
func handleIntegrationWebhook(
	catalog controlplane.CatalogStore,
	registry *integration.Registry,
	store integration.Store,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgSlug := PathParam(r, "org_slug")
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		active, impl, err := resolveIntegrationWebhookInstall(r, registry, store, org.ID)
		if err != nil {
			if webhookErr, ok := integration.AsWebhookError(err); ok {
				httputil.WriteError(w, webhookErr.StatusCode, webhookErr.Message)
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve integration config.")
			return
		}
		webhookImpl, ok := impl.(integration.InboundWebhookIntegration)
		if !ok || !webhookImpl.HandlesInboundWebhook() {
			httputil.WriteError(w, http.StatusNotFound, "Inbound webhook not supported.")
			return
		}

		body, err := readAtMost(r.Body, 1<<20)
		if err != nil {
			if errors.Is(err, errRequestBodyTooLarge) {
				httputil.WriteError(w, http.StatusRequestEntityTooLarge, "Request body too large.")
				return
			}
			httputil.WriteError(w, http.StatusBadRequest, "Failed to read request body.")
			return
		}
		if verifier, ok := impl.(integration.WebhookVerifier); ok {
			if err := verifier.VerifyWebhook(active.Config, r.Header, body); err != nil {
				if webhookErr, ok := integration.AsWebhookError(err); ok {
					httputil.WriteError(w, webhookErr.StatusCode, webhookErr.Message)
					return
				}
				httputil.WriteError(w, http.StatusUnauthorized, "Webhook verification failed.")
				return
			}
		}

		resp, err := impl.OnWebhook(r.Context(), active.Config, body)
		if err != nil {
			if webhookErr, ok := integration.AsWebhookError(err); ok {
				httputil.WriteError(w, webhookErr.StatusCode, webhookErr.Message)
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Webhook handler error.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp)
	}
}

func resolveIntegrationWebhookInstall(
	r *http.Request,
	registry *integration.Registry,
	store integration.Store,
	orgID string,
) (*integration.IntegrationConfig, integration.Integration, error) {
	routeID := PathParam(r, "integration_id")
	if cfg, err := store.Get(r.Context(), routeID); err != nil {
		return nil, nil, err
	} else if cfg != nil {
		if cfg.OrganizationID != orgID || cfg.Status != "active" {
			return nil, nil, &integration.WebhookError{StatusCode: http.StatusNotFound, Message: "Integration not installed or disabled."}
		}
		impl := registry.Get(cfg.IntegrationID)
		if impl == nil {
			return nil, nil, &integration.WebhookError{StatusCode: http.StatusNotFound, Message: "Unknown integration."}
		}
		return cfg, impl, nil
	}

	impl := registry.Get(routeID)
	if impl == nil {
		return nil, nil, &integration.WebhookError{StatusCode: http.StatusNotFound, Message: "Unknown integration."}
	}

	installed, err := store.ListByOrganization(r.Context(), orgID)
	if err != nil {
		return nil, nil, err
	}
	var matches []*integration.IntegrationConfig
	for _, cfg := range installed {
		if cfg.IntegrationID == routeID && cfg.Status == "active" {
			matches = append(matches, cfg)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil, &integration.WebhookError{StatusCode: http.StatusNotFound, Message: "Integration not installed or disabled."}
	case 1:
		return matches[0], impl, nil
	default:
		return nil, nil, &integration.WebhookError{StatusCode: http.StatusConflict, Message: "Multiple active integrations match this webhook URL. Use the installation ID instead."}
	}
}
