package api

import (
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/outboundhttp"
	"urgentry/internal/store"
)

// dataForwardingResponse is the JSON shape returned for a single forwarding config.
type dataForwardingResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

func toForwardingResponse(cfg *store.ForwardingConfig) dataForwardingResponse {
	return dataForwardingResponse{
		ID:        cfg.ID,
		ProjectID: cfg.ProjectID,
		Type:      cfg.Type,
		URL:       cfg.URL,
		Status:    cfg.Status,
		CreatedAt: cfg.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListDataForwarding handles GET /api/0/projects/{org}/{proj}/data-forwarding/.
func handleListDataForwarding(
	catalog controlplane.CatalogStore,
	fwdStore store.ForwardingStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		configs, err := fwdStore.ListForwardingByProject(r.Context(), project.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list data forwarding configs.")
			return
		}

		out := make([]dataForwardingResponse, 0, len(configs))
		for _, cfg := range configs {
			out = append(out, toForwardingResponse(cfg))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// createDataForwardingRequest is the JSON body for creating a forwarding config.
type createDataForwardingRequest struct {
	Type string `json:"type"` // "webhook"
	URL  string `json:"url"`
}

// handleCreateDataForwarding handles POST /api/0/projects/{org}/{proj}/data-forwarding/.
func handleCreateDataForwarding(
	catalog controlplane.CatalogStore,
	fwdStore store.ForwardingStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		var body createDataForwardingRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.URL == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: url")
			return
		}
		body.URL = strings.TrimSpace(body.URL)
		if body.Type == "" {
			body.Type = "webhook"
		}
		if body.Type != "webhook" {
			httputil.WriteError(w, http.StatusBadRequest, "Unsupported forwarding type. Supported: webhook")
			return
		}
		if _, err := outboundhttp.ValidateTargetURL(body.URL); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}

		cfg := &store.ForwardingConfig{
			ProjectID: project.ID,
			Type:      body.Type,
			URL:       body.URL,
		}
		if err := fwdStore.CreateForwarding(r.Context(), cfg); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create data forwarding config.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toForwardingResponse(cfg))
	}
}

// handleDeleteDataForwarding handles DELETE /api/0/projects/{org}/{proj}/data-forwarding/{forwarding_id}/.
func handleDeleteDataForwarding(
	catalog controlplane.CatalogStore,
	fwdStore store.ForwardingStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		forwardingID := PathParam(r, "forwarding_id")
		if forwardingID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing forwarding ID.")
			return
		}

		if err := fwdStore.DeleteForwarding(r.Context(), forwardingID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete data forwarding config.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
