package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

type projectUpdateStore interface {
	UpdateProject(ctx context.Context, orgSlug, projectSlug string, update sharedstore.ProjectUpdate) (*sharedstore.Project, error)
}

type updateProjectRequest struct {
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	Platform        *string `json:"platform"`
	IsBookmarked    *bool   `json:"isBookmarked"`
	ResolveAge      *int    `json:"resolveAge"`
	SubjectPrefix   *string `json:"subjectPrefix"`
	SubjectTemplate *string `json:"subjectTemplate"`
}

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

// handleUpdateProject handles PUT /api/0/projects/{org_slug}/{proj_slug}/.
func handleUpdateProject(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body updateProjectRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}

		update := sharedstore.ProjectUpdate{}
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if name == "" {
				httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
				return
			}
			update.Name = &name
		}
		if body.Slug != nil {
			slug := strings.TrimSpace(*body.Slug)
			if slug == "" {
				httputil.WriteError(w, http.StatusBadRequest, "Slug is required.")
				return
			}
			update.Slug = &slug
		}
		if body.Platform != nil {
			platform := strings.TrimSpace(*body.Platform)
			update.Platform = &platform
		}

		updater, ok := catalog.(projectUpdateStore)
		if !ok {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update project.")
			return
		}
		project, err := updater.UpdateProject(r.Context(), PathParam(r, "org_slug"), PathParam(r, "proj_slug"), update)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update project.")
			return
		}
		if project == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, project)
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

// handleDeleteProject handles DELETE /api/0/projects/{org_slug}/{proj_slug}/.
func handleDeleteProject(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
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
		if err := catalog.DeleteProject(r.Context(), org, proj); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete project.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListProjectTagValues handles GET /api/0/projects/{org_slug}/{proj_slug}/tags/{key}/values/.
func handleListProjectTagValues(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		tagKey := PathParam(r, "key")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		values, err := sqlite.ListTagValues(r.Context(), db, projectID, tagKey)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list tag values.")
			return
		}
		if values == nil {
			values = []sqlite.TagValueRow{}
		}
		httputil.WriteJSON(w, http.StatusOK, values)
	}
}
