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

// handleListReleases handles GET /api/0/organizations/{org_slug}/releases/.
func handleListReleases(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}
		rows, err := releases.ListReleases(r.Context(), orgRecord.ID, 200)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list releases.")
			return
		}
		releases := make([]*Release, 0, len(rows))
		for _, row := range rows {
			summary, err := native.ReleaseSummary(r.Context(), orgRecord.ID, row.Version)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to summarize release.")
				return
			}
			releases = append(releases, mapRelease(row, org, summary))
		}
		page := Paginate(w, r, releases)
		if page == nil {
			page = []*Release{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// createReleaseCommitRequest maps the commit object sent inline by sentry-cli
// inside the create-release payload. The SHA may arrive as "id" (sentry-cli)
// or "commitSha" (our own API), so we accept both.
type createReleaseCommitRequest struct {
	ID          string `json:"id"`        // sentry-cli sends SHA as "id"
	CommitSHA   string `json:"commitSha"` // also accepted
	Repository  string `json:"repository"`
	AuthorName  string `json:"authorName"`
	AuthorEmail string `json:"authorEmail"`
	Message     string `json:"message"`
}

// sha returns whichever field carries the commit hash.
func (c createReleaseCommitRequest) sha() string {
	if c.ID != "" {
		return c.ID
	}
	return c.CommitSHA
}

// createReleaseRefRequest maps the refs array from sentry-cli.
type createReleaseRefRequest struct {
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	PreviousCommit string `json:"previousCommit"`
}

// createReleaseRequest is the JSON body for creating a release.
type createReleaseRequest struct {
	Version  string                       `json:"version"`
	Projects []string                     `json:"projects"`
	Commits  []createReleaseCommitRequest `json:"commits"`
	Refs     []createReleaseRefRequest    `json:"refs"`
}

type releaseDeployRequest struct {
	Environment  string     `json:"environment"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	DateStarted  *time.Time `json:"dateStarted"`
	DateFinished *time.Time `json:"dateFinished"`
}

type releaseCommitRequest struct {
	CommitSHA   string   `json:"commitSha"`
	Repository  string   `json:"repository"`
	AuthorName  string   `json:"authorName"`
	AuthorEmail string   `json:"authorEmail"`
	Message     string   `json:"message"`
	Files       []string `json:"files"`
}

// handleCreateRelease handles POST /api/0/organizations/{org_slug}/releases/.
func handleCreateRelease(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")

		var body createReleaseRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Version == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Version is required.")
			return
		}

		release, err := releases.CreateRelease(r.Context(), org, body.Version)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create release.")
			return
		}
		if release == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		// Store inline commits sent by sentry-cli.
		for _, c := range body.Commits {
			sha := c.sha()
			if sha == "" {
				continue
			}
			_, _ = releases.AddCommit(r.Context(), org, body.Version, sharedstore.ReleaseCommit{
				CommitSHA:   sha,
				Repository:  c.Repository,
				AuthorName:  c.AuthorName,
				AuthorEmail: c.AuthorEmail,
				Message:     c.Message,
			})
		}

		// Store refs as lightweight commit markers so they appear in the
		// commit list for this release (the ref's commit becomes a commit
		// record with the repository association).
		for _, ref := range body.Refs {
			if ref.Commit == "" {
				continue
			}
			msg := ""
			if ref.PreviousCommit != "" {
				msg = "ref range " + ref.PreviousCommit + ".." + ref.Commit
			}
			_, _ = releases.AddCommit(r.Context(), org, body.Version, sharedstore.ReleaseCommit{
				CommitSHA:  ref.Commit,
				Repository: ref.Repository,
				Message:    msg,
			})
		}

		resp := &Release{
			ID:           release.ID,
			OrgSlug:      org,
			Version:      release.Version,
			ShortVersion: release.Version,
			DateCreated:  release.CreatedAt,
			Projects:     body.Projects,
		}
		httputil.WriteJSON(w, http.StatusCreated, resp)
	}
}

func handleGetRelease(db *sql.DB, catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}
		row, err := releases.GetRelease(r.Context(), orgRecord.ID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release.")
			return
		}
		if row == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release not found.")
			return
		}
		summary, err := native.ReleaseSummary(r.Context(), orgRecord.ID, row.Version)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to summarize release.")
			return
		}
		rel := mapRelease(*row, org, summary)
		rel.Projects = loadReleaseProjects(r.Context(), db, org, row.Version)
		enrichReleaseDetail(r.Context(), db, releases, orgRecord.ID, row.Version, rel)
		httputil.WriteJSON(w, http.StatusOK, rel)
	}
}

func mapRelease(row sqlite.Release, orgSlug string, summary sqlite.NativeReleaseSummary) *Release {
	var lastSessionSeenAt *time.Time
	if !row.LastSessionAt.IsZero() {
		t := row.LastSessionAt
		lastSessionSeenAt = &t
	}
	var nativeReprocessUpdatedAt *time.Time
	if !summary.LastRunUpdatedAt.IsZero() {
		t := summary.LastRunUpdatedAt
		nativeReprocessUpdatedAt = &t
	}
	var dateReleased *time.Time
	if !row.DateReleased.IsZero() {
		t := row.DateReleased
		dateReleased = &t
	}
	return &Release{
		ID:                       row.ID,
		OrgSlug:                  orgSlug,
		Version:                  row.Version,
		ShortVersion:             row.Version,
		Ref:                      row.Ref,
		URL:                      row.URL,
		DateCreated:              row.CreatedAt,
		DateReleased:             dateReleased,
		NewGroups:                row.EventCount,
		SessionCount:             row.SessionCount,
		ErroredSessions:          row.ErroredSessions,
		CrashedSessions:          row.CrashedSessions,
		AbnormalSessions:         row.AbnormalSessions,
		AffectedUsers:            row.AffectedUsers,
		CrashFreeRate:            row.CrashFreeRate,
		LastSessionSeenAt:        lastSessionSeenAt,
		NativeEventCount:         summary.TotalEvents,
		NativePendingEvents:      summary.PendingEvents,
		NativeProcessingEvents:   summary.ProcessingEvents,
		NativeFailedEvents:       summary.FailedEvents,
		NativeResolvedFrames:     summary.ResolvedFrames,
		NativeUnresolvedFrames:   summary.UnresolvedFrames,
		NativeLastError:          summary.LastError,
		NativeReprocessRunID:     summary.LastRunID,
		NativeReprocessStatus:    summary.LastRunStatus,
		NativeReprocessLastError: summary.LastRunLastError,
		NativeReprocessUpdatedAt: nativeReprocessUpdatedAt,
		Status:                   "open",
		VersionInfo:              &VersionInfo{Version: row.Version},
	}
}

func enrichReleaseDetail(ctx context.Context, db *sql.DB, releases controlplane.ReleaseStore, orgID, version string, rel *Release) {
	// Commit count and authors
	commits, err := releases.ListCommits(ctx, orgID, version, 100)
	if err == nil {
		rel.CommitCount = len(commits)
		seen := map[string]bool{}
		for _, c := range commits {
			key := c.AuthorEmail
			if key == "" {
				key = c.AuthorName
			}
			if key != "" && !seen[key] {
				seen[key] = true
				rel.Authors = append(rel.Authors, ReleaseAuthor{
					Name:  c.AuthorName,
					Email: c.AuthorEmail,
				})
			}
		}
	}

	// Deploy count and last deploy
	deploys, err := releases.ListDeploys(ctx, orgID, version, 100)
	if err == nil {
		rel.DeployCount = len(deploys)
		if len(deploys) > 0 {
			d := deploys[0]
			deploy := &ReleaseDeploy{
				ID:          d.ID,
				Environment: d.Environment,
				Name:        d.Name,
				URL:         d.URL,
			}
			if !d.DateStarted.IsZero() {
				deploy.DateStarted = &d.DateStarted
			}
			if !d.DateFinished.IsZero() {
				deploy.DateFinished = &d.DateFinished
			}
			rel.LastDeploy = deploy
		}
	}
}

// loadReleaseProjects finds project slugs that have events for a given release version.
func loadReleaseProjects(ctx context.Context, db *sql.DB, orgSlug, version string) []string {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT p.slug
		FROM events e
		JOIN projects p ON p.id = e.project_id
		JOIN organizations o ON o.id = p.organization_id
		WHERE o.slug = ? AND e.release = ?
		LIMIT 50`, orgSlug, version)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			slugs = append(slugs, s)
		}
	}
	return slugs
}

// handleDeleteRelease handles DELETE /api/0/organizations/{org_slug}/releases/{version}/.
func handleDeleteRelease(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		version := PathParam(r, "version")

		if err := releases.DeleteRelease(r.Context(), org, version); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete release.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type updateReleaseRequest struct {
	Ref          *string                `json:"ref"`
	URL          *string                `json:"url"`
	DateReleased *time.Time             `json:"dateReleased"`
	Commits      []releaseCommitRequest `json:"commits"`
}

// handleUpdateRelease handles PUT /api/0/organizations/{org_slug}/releases/{version}/.
func handleUpdateRelease(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		version := PathParam(r, "version")

		var body updateReleaseRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}

		updated, err := releases.UpdateRelease(r.Context(), org, version, body.Ref, body.URL, body.DateReleased)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update release.")
			return
		}
		if updated == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release not found.")
			return
		}

		// Apply any commits included in the update.
		for _, c := range body.Commits {
			_, _ = releases.AddCommit(r.Context(), org, version, sharedstore.ReleaseCommit{
				CommitSHA:   c.CommitSHA,
				Repository:  c.Repository,
				AuthorName:  c.AuthorName,
				AuthorEmail: c.AuthorEmail,
				Message:     c.Message,
				Files:       c.Files,
			})
		}

		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}
		summary, err := native.ReleaseSummary(r.Context(), orgRecord.ID, updated.Version)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to summarize release.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapRelease(*updated, org, summary))
	}
}

func handleListReleaseDeploys(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		items, err := releases.ListDeploys(r.Context(), org, PathParam(r, "version"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release deploys.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleCreateReleaseDeploy(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		var body releaseDeployRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		item, err := releases.AddDeploy(r.Context(), org, PathParam(r, "version"), sharedstore.ReleaseDeploy{
			Environment:  strings.TrimSpace(body.Environment),
			Name:         body.Name,
			URL:          body.URL,
			DateStarted:  derefTime(body.DateStarted),
			DateFinished: derefTime(body.DateFinished),
		})
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Failed to create release deploy.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, item)
	}
}

func handleListReleaseCommits(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		items, err := releases.ListCommits(r.Context(), org, PathParam(r, "version"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release commits.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleCreateReleaseCommit(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		var body releaseCommitRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		item, err := releases.AddCommit(r.Context(), org, PathParam(r, "version"), sharedstore.ReleaseCommit{
			CommitSHA:   body.CommitSHA,
			Repository:  body.Repository,
			AuthorName:  body.AuthorName,
			AuthorEmail: body.AuthorEmail,
			Message:     body.Message,
			Files:       body.Files,
		})
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Failed to create release commit.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, item)
	}
}

func handleListReleaseSuspects(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		items, err := releases.ListSuspects(r.Context(), org, PathParam(r, "version"), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release suspects.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

// handleListReleaseCommitFiles handles GET /api/0/organizations/{org_slug}/releases/{version}/commitfiles/.
// Returns files changed across all commits in this release.
func handleListReleaseCommitFiles(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		if _, ok := getOrganizationFromCatalog(w, r, catalog, org); !ok {
			return
		}
		commits, err := releases.ListCommits(r.Context(), org, PathParam(r, "version"), 200)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release commits.")
			return
		}

		type commitFileResponse struct {
			Filename  string `json:"filename"`
			CommitSHA string `json:"commitSha,omitempty"`
		}
		seen := make(map[string]bool)
		var result []commitFileResponse
		for _, c := range commits {
			for _, f := range c.Files {
				if !seen[f] {
					seen[f] = true
					result = append(result, commitFileResponse{
						Filename:  f,
						CommitSHA: c.CommitSHA,
					})
				}
			}
		}
		if result == nil {
			result = []commitFileResponse{}
		}
		httputil.WriteJSON(w, http.StatusOK, result)
	}
}

// handleListProjectReleaseCommits handles GET /api/0/projects/{org}/{proj}/releases/{version}/commits/.
// It reuses the org-level release commit list, but only after verifying that
// the requested project is actually associated with the release version.
func handleListProjectReleaseCommits(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}
		version := PathParam(r, "version")
		hasRelease, err := releases.ProjectHasRelease(r.Context(), project.ID, version)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project release.")
			return
		}
		if !hasRelease {
			httputil.WriteError(w, http.StatusNotFound, "Release not found.")
			return
		}
		items, err := releases.ListCommits(r.Context(), PathParam(r, "org_slug"), version, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release commits.")
			return
		}
		if items == nil {
			items = []sharedstore.ReleaseCommit{}
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

// handleListProjectReleaseFiles handles GET /api/0/projects/{org}/{proj}/releases/{version}/files/.
func handleListProjectReleaseFiles(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		files, err := smStore.ListByRelease(r.Context(), project.ID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release files.")
			return
		}
		result := make([]*releaseFileResponse, 0, len(files))
		for _, f := range files {
			result = append(result, artifactToFileResponse(f))
		}
		page := Paginate(w, r, result)
		if page == nil {
			page = []*releaseFileResponse{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetProjectReleaseFile handles GET /api/0/projects/{org}/{proj}/releases/{version}/files/{file_id}/.
func handleGetProjectReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		art, _, err := smStore.GetArtifact(r.Context(), PathParam(r, "file_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release file.")
			return
		}
		if art == nil || art.ProjectID != project.ID {
			httputil.WriteError(w, http.StatusNotFound, "Release file not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, artifactToFileResponse(art))
	}
}

// handleUpdateProjectReleaseFile handles PUT /api/0/projects/{org}/{proj}/releases/{version}/files/{file_id}/.
func handleUpdateProjectReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		var body updateReleaseFileRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		// Verify the artifact belongs to this project before updating.
		art, _, err := smStore.GetArtifact(r.Context(), PathParam(r, "file_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release file.")
			return
		}
		if art == nil || art.ProjectID != project.ID {
			httputil.WriteError(w, http.StatusNotFound, "Release file not found.")
			return
		}

		// Reuse the org-level update by setting the org context. For project-scoped
		// artifacts the organization_id is empty, so we update by artifact ID directly.
		updated, err := smStore.UpdateProjectArtifactName(r.Context(), project.ID, PathParam(r, "version"), PathParam(r, "file_id"), body.Name)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update release file.")
			return
		}
		if updated == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release file not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, artifactToFileResponse(updated))
	}
}

// handleDeleteProjectReleaseFile handles DELETE /api/0/projects/{org}/{proj}/releases/{version}/files/{file_id}/.
func handleDeleteProjectReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		// Verify ownership before deleting.
		art, _, err := smStore.GetArtifact(r.Context(), PathParam(r, "file_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release file.")
			return
		}
		if art == nil || art.ProjectID != project.ID {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := smStore.DeleteArtifact(r.Context(), PathParam(r, "file_id")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete release file.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
