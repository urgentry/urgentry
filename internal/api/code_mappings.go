package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// codeMappingRequest is the JSON body for creating a code mapping.
type codeMappingRequest struct {
	StackRoot     string `json:"stackRoot"`
	SourceRoot    string `json:"sourceRoot"`
	DefaultBranch string `json:"defaultBranch"`
	RepoURL       string `json:"repoUrl"`
}

// handleListCodeMappings handles GET /api/0/projects/{org}/{proj}/code-mappings/.
func handleListCodeMappings(
	catalog controlplane.CatalogStore,
	mappings store.CodeMappingStore,
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
		list, err := mappings.ListCodeMappings(r.Context(), project.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list code mappings.")
			return
		}
		if list == nil {
			list = []*store.CodeMapping{}
		}
		httputil.WriteJSON(w, http.StatusOK, list)
	}
}

// handleCreateCodeMapping handles POST /api/0/projects/{org}/{proj}/code-mappings/.
func handleCreateCodeMapping(
	catalog controlplane.CatalogStore,
	mappings store.CodeMappingStore,
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
		var body codeMappingRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.RepoURL == "" {
			httputil.WriteError(w, http.StatusBadRequest, "repoUrl is required.")
			return
		}
		if body.DefaultBranch == "" {
			body.DefaultBranch = "main"
		}
		m := &store.CodeMapping{
			ProjectID:     project.ID,
			StackRoot:     body.StackRoot,
			SourceRoot:    body.SourceRoot,
			DefaultBranch: body.DefaultBranch,
			RepoURL:       body.RepoURL,
		}
		if err := mappings.CreateCodeMapping(r.Context(), m); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create code mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, m)
	}
}

// handleDeleteCodeMapping handles DELETE /api/0/projects/{org}/{proj}/code-mappings/{mapping_id}/.
func handleDeleteCodeMapping(
	catalog controlplane.CatalogStore,
	mappings store.CodeMappingStore,
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
		mappingID := PathParam(r, "mapping_id")
		if err := mappings.DeleteCodeMapping(r.Context(), project.ID, mappingID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete code mapping.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
