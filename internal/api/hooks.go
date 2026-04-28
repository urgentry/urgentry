package api

import (
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/outboundhttp"
	"urgentry/internal/sqlite"
)

// hookResponse is the Sentry-compatible JSON shape for a service hook.
type hookResponse struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Status      string   `json:"status"`
	DateCreated string   `json:"dateCreated"`
}

func toHookResponse(h *sqlite.ServiceHook) hookResponse {
	return hookResponse{
		ID:          h.ID,
		URL:         h.URL,
		Events:      h.Events,
		Status:      h.Status,
		DateCreated: h.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListHooks handles GET /api/0/projects/{org}/{proj}/hooks/.
func handleListHooks(
	catalog controlplane.CatalogStore,
	hooks *sqlite.HookStore,
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
		items, err := hooks.List(r.Context(), project.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list hooks.")
			return
		}
		out := make([]hookResponse, 0, len(items))
		for i := range items {
			out = append(out, toHookResponse(&items[i]))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// createHookRequest is the JSON body for creating a hook.
type createHookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

var validHookEvents = map[string]struct{}{
	"event.created":  {},
	"event.alert":    {},
	"issue.created":  {},
	"issue.resolved": {},
}

func normalizeHookEvents(events []string) ([]string, bool) {
	if len(events) == 0 {
		return nil, false
	}
	normalized := make([]string, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, item := range events {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, false
		}
		if _, ok := validHookEvents[item]; !ok {
			return nil, false
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized, len(normalized) > 0
}

// handleCreateHook handles POST /api/0/projects/{org}/{proj}/hooks/.
func handleCreateHook(
	catalog controlplane.CatalogStore,
	hooks *sqlite.HookStore,
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
		var body createHookRequest
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
		events, ok := normalizeHookEvents(body.Events)
		if !ok {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid hook events.")
			return
		}
		h := &sqlite.ServiceHook{
			ProjectID: project.ID,
			URL:       body.URL,
			Events:    events,
		}
		if err := hooks.Create(r.Context(), h); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create hook.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toHookResponse(h))
	}
}

// handleGetHook handles GET /api/0/projects/{org}/{proj}/hooks/{hook_id}/.
func handleGetHook(
	catalog controlplane.CatalogStore,
	hooks *sqlite.HookStore,
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
		hookID := PathParam(r, "hook_id")
		h, err := hooks.Get(r.Context(), hookID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load hook.")
			return
		}
		if h == nil {
			httputil.WriteError(w, http.StatusNotFound, "Hook not found.")
			return
		}
		if h.ProjectID != project.ID {
			httputil.WriteError(w, http.StatusNotFound, "Hook not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toHookResponse(h))
	}
}

// updateHookRequest is the JSON body for updating a hook.
type updateHookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// handleUpdateHook handles PUT /api/0/projects/{org}/{proj}/hooks/{hook_id}/.
func handleUpdateHook(
	catalog controlplane.CatalogStore,
	hooks *sqlite.HookStore,
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
		hookID := PathParam(r, "hook_id")
		h, err := hooks.Get(r.Context(), hookID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load hook.")
			return
		}
		if h == nil {
			httputil.WriteError(w, http.StatusNotFound, "Hook not found.")
			return
		}
		if h.ProjectID != project.ID {
			httputil.WriteError(w, http.StatusNotFound, "Hook not found.")
			return
		}
		var body updateHookRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.URL != "" {
			body.URL = strings.TrimSpace(body.URL)
			if _, err := outboundhttp.ValidateTargetURL(body.URL); err != nil {
				httputil.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.URL = body.URL
		}
		if body.Events != nil {
			events, ok := normalizeHookEvents(body.Events)
			if !ok {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid hook events.")
				return
			}
			h.Events = events
		}
		if err := hooks.Update(r.Context(), h); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update hook.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toHookResponse(h))
	}
}

// handleDeleteHook handles DELETE /api/0/projects/{org}/{proj}/hooks/{hook_id}/.
func handleDeleteHook(
	catalog controlplane.CatalogStore,
	hooks *sqlite.HookStore,
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
		hookID := PathParam(r, "hook_id")
		h, err := hooks.Get(r.Context(), hookID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load hook.")
			return
		}
		if h == nil || h.ProjectID != project.ID {
			httputil.WriteError(w, http.StatusNotFound, "Hook not found.")
			return
		}
		if err := hooks.Delete(r.Context(), hookID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete hook.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
