package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

type projectUpdateStore interface {
	UpdateProject(ctx context.Context, orgSlug, projectSlug string, update sharedstore.ProjectUpdate) (*sharedstore.Project, error)
}

type updateProjectRequest struct {
	Name                *string        `json:"name"`
	Slug                *string        `json:"slug"`
	Platform            *string        `json:"platform"`
	IsBookmarked        *bool          `json:"isBookmarked"`
	ResolveAge          *int           `json:"resolveAge"`
	SubjectPrefix       *string        `json:"subjectPrefix"`
	SubjectTemplate     *string        `json:"subjectTemplate"`
	DigestsMinDelay     *int           `json:"digestsMinDelay"`
	DigestsMaxDelay     *int           `json:"digestsMaxDelay"`
	DefaultEnvironment  *string        `json:"defaultEnvironment"`
	ScrubIPAddresses    *bool          `json:"scrubIPAddresses"`
	DataScrubber        *bool          `json:"dataScrubber"`
	DataScrubberDefaults *bool         `json:"dataScrubberDefaults"`
	SensitiveFields     []string       `json:"sensitiveFields"`
	SafeFields          []string       `json:"safeFields"`
	AllowedDomains      []string       `json:"allowedDomains"`
	ScrapeJavaScript    *bool          `json:"scrapeJavaScript"`
	Options             map[string]any `json:"options"`
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
func handleGetProject(db *sql.DB, catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
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

		detail := enrichProjectDetail(r.Context(), db, catalog, org, proj, rec)
		httputil.WriteJSON(w, http.StatusOK, detail)
	}
}

// enrichProjectDetail builds a ProjectDetail from a base Project record by
// querying for firstEvent, latestRelease, and teams.
func enrichProjectDetail(ctx context.Context, db *sql.DB, catalog controlplane.CatalogStore, orgSlug, projSlug string, rec *sharedstore.Project) *ProjectDetail {
	detail := &ProjectDetail{
		ID:                  rec.ID,
		Slug:                rec.Slug,
		Name:                rec.Name,
		OrgSlug:             rec.OrgSlug,
		Platform:            rec.Platform,
		Status:              rec.Status,
		EventRetentionDays:  rec.EventRetentionDays,
		AttachRetentionDays: rec.AttachRetentionDays,
		DebugRetentionDays:  rec.DebugRetentionDays,
		DateCreated:         rec.DateCreated,
		TeamSlug:            rec.TeamSlug,
		Features:            defaultProjectFeatures(),
		HasAccess:           true,
		Options:             map[string]any{},
		Plugins:             []any{},
		ProcessingIssues:    0,
		ScrapeJavaScript:    true,
		Teams:               []projectTeamResponse{},
	}

	// First event timestamp for this project.
	detail.FirstEvent = queryProjectFirstEvent(ctx, db, rec.ID)

	// Latest release for the project's organization.
	detail.LatestRelease = queryProjectLatestRelease(ctx, db, rec.ID)

	// Teams associated with this project.
	if teams, err := catalog.ListProjectTeams(ctx, orgSlug, projSlug); err == nil && len(teams) > 0 {
		out := make([]projectTeamResponse, 0, len(teams))
		for _, t := range teams {
			out = append(out, projectTeamResponse{
				ID:          t.ID,
				Slug:        t.Slug,
				Name:        t.Name,
				DateCreated: t.DateCreated,
			})
		}
		detail.Teams = out
	}

	return detail
}

// queryProjectFirstEvent returns the earliest event timestamp for a project.
func queryProjectFirstEvent(ctx context.Context, db *sql.DB, projectID string) *time.Time {
	var firstAt sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT MIN(occurred_at) FROM events WHERE project_id = ?`,
		projectID,
	).Scan(&firstAt)
	if err != nil || !firstAt.Valid || firstAt.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, firstAt.String)
	if err != nil {
		t, err = time.Parse(time.RFC3339, firstAt.String)
		if err != nil {
			return nil
		}
	}
	t = t.UTC()
	return &t
}

// queryProjectLatestRelease returns the most recent release for the project's org.
func queryProjectLatestRelease(ctx context.Context, db *sql.DB, projectID string) *ReleaseRef {
	var version string
	var createdAt string
	err := db.QueryRowContext(ctx,
		`SELECT r.version, r.created_at
		 FROM releases r
		 JOIN projects p ON p.organization_id = r.organization_id
		 WHERE p.id = ?
		 ORDER BY r.created_at DESC
		 LIMIT 1`,
		projectID,
	).Scan(&version, &createdAt)
	if err != nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, createdAt)
	}
	return &ReleaseRef{
		Version:     version,
		DateCreated: t.UTC(),
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
