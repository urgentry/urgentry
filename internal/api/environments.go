package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
)

// projectEnvironmentResponse is the Sentry-compatible shape for a project environment.
type projectEnvironmentResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsHidden bool   `json:"isHidden"`
}

// handleListProjectEnvironments handles GET /api/0/projects/{org}/{proj}/environments/.
func handleListProjectEnvironments(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		envs, err := catalog.ListProjectEnvironments(r.Context(), org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list environments.")
			return
		}
		if envs == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		out := make([]projectEnvironmentResponse, 0, len(envs))
		for _, env := range envs {
			out = append(out, projectEnvironmentResponse{
				ID:       envNameToID(env.Name),
				Name:     env.Name,
				IsHidden: env.IsHidden,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// handleGetProjectEnvironment handles GET /api/0/projects/{org}/{proj}/environments/{env_name}/.
func handleGetProjectEnvironment(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		envName := PathParam(r, "env_name")

		env, err := catalog.GetProjectEnvironment(r.Context(), org, proj, envName)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load environment.")
			return
		}
		if env == nil {
			httputil.WriteError(w, http.StatusNotFound, "Environment not found.")
			return
		}

		httputil.WriteJSON(w, http.StatusOK, &projectEnvironmentResponse{
			ID:       envNameToID(env.Name),
			Name:     env.Name,
			IsHidden: env.IsHidden,
		})
	}
}

type updateProjectEnvironmentRequest struct {
	IsHidden *bool `json:"isHidden"`
}

// handleUpdateProjectEnvironment handles PUT /api/0/projects/{org}/{proj}/environments/{env_name}/.
func handleUpdateProjectEnvironment(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		envName := PathParam(r, "env_name")

		var body updateProjectEnvironmentRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.IsHidden == nil {
			httputil.WriteError(w, http.StatusBadRequest, "isHidden is required.")
			return
		}

		env, err := catalog.UpdateProjectEnvironment(r.Context(), org, proj, envName, *body.IsHidden)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update environment.")
			return
		}
		if env == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		httputil.WriteJSON(w, http.StatusOK, &projectEnvironmentResponse{
			ID:       envNameToID(env.Name),
			Name:     env.Name,
			IsHidden: env.IsHidden,
		})
	}
}
