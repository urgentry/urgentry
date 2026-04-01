package api

import (
	"encoding/json"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// symbolSourceRequest is the JSON body for creating/updating a symbol source.
type symbolSourceRequest struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Name            string          `json:"name"`
	Layout          json.RawMessage `json:"layout"`
	URL             string          `json:"url"`
	CredentialsJSON string          `json:"credentials,omitempty"`
}

// handleListSymbolSources handles GET /api/0/projects/{org}/{proj}/symbol-sources/.
func handleListSymbolSources(
	catalog controlplane.CatalogStore,
	store *sqlite.SymbolSourceStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		sources, err := store.List(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list symbol sources.")
			return
		}

		result := make([]sqlite.SymbolSourceResponse, 0, len(sources))
		for _, s := range sources {
			result = append(result, s.ToResponse())
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}

// handleCreateSymbolSource handles POST /api/0/projects/{org}/{proj}/symbol-sources/.
func handleCreateSymbolSource(
	catalog controlplane.CatalogStore,
	store *sqlite.SymbolSourceStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body symbolSourceRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Type == "" {
			httputil.WriteError(w, http.StatusBadRequest, "type is required.")
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "name is required.")
			return
		}

		ss := &sqlite.SymbolSource{
			ProjectID:       projectID,
			Type:            body.Type,
			Name:            body.Name,
			Layout:          body.Layout,
			URL:             body.URL,
			CredentialsJSON: body.CredentialsJSON,
		}

		created, err := store.Create(r.Context(), ss)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create symbol source.")
			return
		}
		resp := created.ToResponse()
		httputil.WriteJSON(w, http.StatusCreated, resp)
	}
}

// handleUpdateSymbolSource handles PUT /api/0/projects/{org}/{proj}/symbol-sources/.
func handleUpdateSymbolSource(
	catalog controlplane.CatalogStore,
	store *sqlite.SymbolSourceStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body symbolSourceRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.ID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "id is required.")
			return
		}

		ss := &sqlite.SymbolSource{
			ID:              body.ID,
			ProjectID:       projectID,
			Type:            body.Type,
			Name:            body.Name,
			Layout:          body.Layout,
			URL:             body.URL,
			CredentialsJSON: body.CredentialsJSON,
		}

		updated, err := store.Update(r.Context(), ss)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update symbol source.")
			return
		}
		if updated == nil {
			httputil.WriteError(w, http.StatusNotFound, "Symbol source not found.")
			return
		}
		resp := updated.ToResponse()
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

// deleteSymbolSourceRequest is the JSON body for deleting a symbol source.
type deleteSymbolSourceRequest struct {
	ID string `json:"id"`
}

// handleDeleteSymbolSource handles DELETE /api/0/projects/{org}/{proj}/symbol-sources/.
func handleDeleteSymbolSource(
	catalog controlplane.CatalogStore,
	store *sqlite.SymbolSourceStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body deleteSymbolSourceRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.ID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "id is required.")
			return
		}

		if err := store.Delete(r.Context(), projectID, body.ID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete symbol source.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
