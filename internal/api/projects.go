package api

import (
	"strings"

	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	sharedstore "urgentry/internal/store"
)

// handleListAllProjects handles GET /api/0/projects/.
func handleListAllProjects(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projects, err := catalog.ListProjects(r.Context(), "")
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}
		page := Paginate(w, r, projects)
		if page == nil {
			page = []Project{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetProject handles GET /api/0/projects/{org_slug}/{proj_slug}/.
func handleGetProject(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		rec, err := catalog.GetProject(r.Context(), org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rec)
	}
}

// handleListOrgProjects handles GET /api/0/organizations/{org_slug}/projects/.
func handleListOrgProjects(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		projects, err := catalog.ListProjects(r.Context(), org)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization projects.")
			return
		}
		page := Paginate(w, r, projects)
		if page == nil {
			page = []Project{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// createProjectRequest is the JSON body for creating a project.
type createProjectRequest struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Platform string `json:"platform"`
}

// handleCreateProject handles POST /api/0/teams/{org_slug}/{team_slug}/projects/.
func handleCreateProject(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		teamSlug := PathParam(r, "team_slug")

		var body createProjectRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		slug := body.Slug
		if slug == "" {
			slug = strings.ToLower(strings.ReplaceAll(body.Name, " ", "-"))
		}

		project, err := catalog.CreateProject(r.Context(), orgSlug, teamSlug, sharedstore.ProjectCreateInput{
			Name:     body.Name,
			Slug:     slug,
			Platform: body.Platform,
		})
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create project.")
			return
		}
		if project == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization or team not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, project)
	}
}
