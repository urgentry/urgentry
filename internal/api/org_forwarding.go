package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/outboundhttp"
	"urgentry/internal/store"
)

// orgForwarderResponse is the JSON shape returned for a single org data forwarder.
type orgForwarderResponse struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	Enabled   bool            `json:"enabled"`
	CreatedAt string          `json:"dateCreated"`
	Config    json.RawMessage `json:"credentials,omitempty"`
}

func toOrgForwarderResponse(f *store.OrgDataForwarder) orgForwarderResponse {
	creds := json.RawMessage(f.CredentialsJSON)
	if !json.Valid(creds) {
		creds = json.RawMessage("{}")
	}
	return orgForwarderResponse{
		ID:        f.ID,
		Type:      f.Type,
		Name:      f.Name,
		URL:       f.URL,
		Enabled:   f.Enabled,
		CreatedAt: f.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Config:    creds,
	}
}

// handleListOrgForwarding handles GET /api/0/organizations/{org_slug}/forwarding/.
func handleListOrgForwarding(
	catalog controlplane.CatalogStore,
	fwdStore store.OrgForwarderStore,
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
		items, err := fwdStore.ListOrgForwarders(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list forwarders.")
			return
		}
		out := make([]orgForwarderResponse, 0, len(items))
		for _, f := range items {
			out = append(out, toOrgForwarderResponse(f))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

type createOrgForwarderRequest struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	Credentials json.RawMessage `json:"credentials"`
}

// handleCreateOrgForwarding handles POST /api/0/organizations/{org_slug}/forwarding/.
func handleCreateOrgForwarding(
	catalog controlplane.CatalogStore,
	fwdStore store.OrgForwarderStore,
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
		var body createOrgForwarderRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.URL == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: url")
			return
		}
		body.URL = strings.TrimSpace(body.URL)
		if _, err := outboundhttp.ValidateTargetURL(body.URL); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Type == "" {
			body.Type = "webhook"
		}
		credsStr := "{}"
		if len(body.Credentials) > 0 {
			credsStr = string(body.Credentials)
		}
		f := &store.OrgDataForwarder{
			OrgID:           org.ID,
			Type:            body.Type,
			Name:            body.Name,
			URL:             body.URL,
			CredentialsJSON: credsStr,
			Enabled:         true,
		}
		if err := fwdStore.CreateOrgForwarder(r.Context(), f); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create forwarder.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toOrgForwarderResponse(f))
	}
}

type updateOrgForwarderRequest struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	Credentials json.RawMessage `json:"credentials"`
	Enabled     *bool           `json:"enabled"`
}

// handleUpdateOrgForwarding handles PUT /api/0/organizations/{org_slug}/forwarding/{id}/.
func handleUpdateOrgForwarding(
	fwdStore store.OrgForwarderStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fwdID := PathParam(r, "id")
		if fwdID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing forwarder ID.")
			return
		}
		var body updateOrgForwarderRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		body.URL = strings.TrimSpace(body.URL)
		if body.URL == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: url")
			return
		}
		if _, err := outboundhttp.ValidateTargetURL(body.URL); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		f := &store.OrgDataForwarder{
			ID:   fwdID,
			Type: body.Type,
			Name: body.Name,
			URL:  body.URL,
		}
		if len(body.Credentials) > 0 {
			f.CredentialsJSON = string(body.Credentials)
		} else {
			f.CredentialsJSON = "{}"
		}
		if body.Enabled != nil {
			f.Enabled = *body.Enabled
		} else {
			f.Enabled = true
		}
		if err := fwdStore.UpdateOrgForwarder(r.Context(), f); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update forwarder.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toOrgForwarderResponse(f))
	}
}

// handleDeleteOrgForwarding handles DELETE /api/0/organizations/{org_slug}/forwarding/{id}/.
func handleDeleteOrgForwarding(
	fwdStore store.OrgForwarderStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fwdID := PathParam(r, "id")
		if fwdID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing forwarder ID.")
			return
		}
		if err := fwdStore.DeleteOrgForwarder(r.Context(), fwdID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete forwarder.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
