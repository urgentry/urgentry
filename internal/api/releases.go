package api

import (
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

// createReleaseRequest is the JSON body for creating a release.
type createReleaseRequest struct {
	Version string `json:"version"`
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
		httputil.WriteJSON(w, http.StatusCreated, &Release{
			ID:           release.ID,
			OrgSlug:      org,
			Version:      release.Version,
			ShortVersion: release.Version,
			DateCreated:  release.CreatedAt,
		})
	}
}

func handleGetRelease(catalog controlplane.CatalogStore, releases controlplane.ReleaseStore, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
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
		httputil.WriteJSON(w, http.StatusOK, mapRelease(*row, org, summary))
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
	}
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
	Ref          *string    `json:"ref"`
	URL          *string    `json:"url"`
	DateReleased *time.Time `json:"dateReleased"`
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

func handleListReleaseDeploys(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := releases.ListDeploys(r.Context(), PathParam(r, "org_slug"), PathParam(r, "version"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release deploys.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleCreateReleaseDeploy(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body releaseDeployRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		item, err := releases.AddDeploy(r.Context(), PathParam(r, "org_slug"), PathParam(r, "version"), sharedstore.ReleaseDeploy{
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

func handleListReleaseCommits(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := releases.ListCommits(r.Context(), PathParam(r, "org_slug"), PathParam(r, "version"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release commits.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleCreateReleaseCommit(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body releaseCommitRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		item, err := releases.AddCommit(r.Context(), PathParam(r, "org_slug"), PathParam(r, "version"), sharedstore.ReleaseCommit{
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

func handleListReleaseSuspects(releases controlplane.ReleaseStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := releases.ListSuspects(r.Context(), PathParam(r, "org_slug"), PathParam(r, "version"), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release suspects.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
