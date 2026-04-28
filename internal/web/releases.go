package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Releases Page
// ---------------------------------------------------------------------------

type releasesData struct {
	Title        string
	Nav          string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Releases     []releaseRow
}

type releaseRow struct {
	Version          string
	CreatedAt        string
	EventCount       string
	SessionCount     string
	ErroredSessions  string
	CrashedSessions  string
	AbnormalSessions string
	AffectedUsers    string
	CrashFreeRate    string
	LastSeen         string
}

type releaseDetailData struct {
	Title          string
	Nav            string
	Environment    string   // selected environment ("" = all)
	Environments   []string // available environments for global nav
	DefaultProject string
	Release        releaseRow
	Regression     *releaseRegressionData
	Native         releaseNativeSummary
	DebugFiles     []releaseDebugFileRow
	Backfills      []releaseBackfillRow
	Deploys        []releaseDeployRow
	Commits        []releaseCommitRow
	Suspects       []releaseSuspectRow
	Profiles       []releaseProfileRow
}

type releaseNativeSummary struct {
	TotalEvents      string
	PendingEvents    string
	ProcessingEvents string
	FailedEvents     string
	ResolvedFrames   string
	UnresolvedFrames string
	LastError        string
	LastRunID        string
	LastRunStatus    string
	LastRunUpdatedAt string
	LastRunLastError string
}

type releaseDebugFileRow struct {
	ID                  string
	ProjectID           string
	Name                string
	Kind                string
	SymbolicationStatus string
	ReprocessStatus     string
	ReprocessLastError  string
	DateReprocessed     string
}

type releaseBackfillRow struct {
	ID          string
	Status      string
	Scope       string
	Processed   string
	Updated     string
	Failed      string
	LastError   string
	DateUpdated string
}

type releaseDeployRow struct {
	Environment  string
	Name         string
	URL          string
	DateStarted  string
	DateFinished string
	DateCreated  string
}

type releaseCommitRow struct {
	CommitSHA   string
	Repository  string
	Author      string
	Message     string
	Files       string
	DateCreated string
}

type releaseSuspectRow struct {
	GroupID     string
	ShortID     string
	Title       string
	Culprit     string
	CommitSHA   string
	Author      string
	Message     string
	MatchedFile string
	LastSeen    string
}

type releaseProfileRow struct {
	ID          string
	Transaction string
	Release     string
	Environment string
	Duration    string
	SampleCount string
	TopFunction string
	TraceID     string
	StartedAt   string
}

type releaseRegressionReader interface {
	GetReleaseRegression(ctx context.Context, orgSlug, version string) (*sharedstore.ReleaseRegressionSummary, error)
}

func (h *Handler) releasesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	data := releasesData{
		Title:        "Releases",
		Nav:          "releases",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
	}

	if h.webStore != nil {
		releases, err := h.listReleasesDB(ctx, 100)
		if err == nil {
			for _, rel := range releases {
				data.Releases = append(data.Releases, releaseRow{
					Version:          rel.version,
					CreatedAt:        timeAgo(rel.createdAt),
					EventCount:       formatNumber(rel.eventCount),
					SessionCount:     formatNumber(rel.sessionCount),
					ErroredSessions:  formatNumber(rel.erroredSessions),
					CrashedSessions:  formatNumber(rel.crashedSessions),
					AbnormalSessions: formatNumber(rel.abnormalSessions),
					AffectedUsers:    formatNumber(rel.affectedUsers),
					CrashFreeRate:    formatPercent(rel.crashFreeRate),
					LastSeen:         formatReleaseLastSeen(rel.lastSessionAt),
				})
			}
		}
	}

	h.render(w, "releases.html", data)
}

func (h *Handler) releaseDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil || h.releases == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	orgSlug, defaultProjectID, err := h.defaultReleaseContext(ctx)
	if err != nil || orgSlug == "" {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	version := strings.TrimSpace(r.PathValue("version"))
	release, err := h.releases.GetReleaseBySlug(ctx, orgSlug, version)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	if release == nil {
		http.NotFound(w, r)
		return
	}
	deploys, err := h.releases.ListDeploys(ctx, orgSlug, version, 50)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	commits, err := h.releases.ListCommits(ctx, orgSlug, version, 50)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	suspects, err := h.releases.ListSuspects(ctx, orgSlug, version, 25)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	nativeSummary, err := h.nativeControl.ReleaseSummary(ctx, release.OrganizationID, version)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	nativeRuns, err := h.nativeControl.ListReleaseRuns(ctx, release.OrganizationID, defaultProjectID, version, 10)
	if err != nil {
		http.Error(w, "Failed to load release.", http.StatusInternalServerError)
		return
	}
	var highlights []sharedstore.ProfileReference
	if strings.TrimSpace(defaultProjectID) != "" {
		highlights, err = h.queries.ListReleaseProfileHighlights(ctx, defaultProjectID, version, 6)
		if err != nil {
			http.Error(w, "Failed to load release.", http.StatusInternalServerError)
			return
		}
	}

	data := releaseDetailData{
		Title:          version,
		Nav:            "releases",
		DefaultProject: defaultProjectID,
		Release: releaseRow{
			Version:          release.Version,
			CreatedAt:        timeAgo(release.CreatedAt),
			EventCount:       formatNumber(release.EventCount),
			SessionCount:     formatNumber(release.SessionCount),
			ErroredSessions:  formatNumber(release.ErroredSessions),
			CrashedSessions:  formatNumber(release.CrashedSessions),
			AbnormalSessions: formatNumber(release.AbnormalSessions),
			AffectedUsers:    formatNumber(release.AffectedUsers),
			CrashFreeRate:    formatPercent(release.CrashFreeRate),
			LastSeen:         formatReleaseLastSeen(release.LastSessionAt),
		},
		Native: releaseNativeSummary{
			TotalEvents:      formatNumber(nativeSummary.TotalEvents),
			PendingEvents:    formatNumber(nativeSummary.PendingEvents),
			ProcessingEvents: formatNumber(nativeSummary.ProcessingEvents),
			FailedEvents:     formatNumber(nativeSummary.FailedEvents),
			ResolvedFrames:   formatNumber(nativeSummary.ResolvedFrames),
			UnresolvedFrames: formatNumber(nativeSummary.UnresolvedFrames),
			LastError:        nativeSummary.LastError,
			LastRunID:        nativeSummary.LastRunID,
			LastRunStatus:    nativeSummary.LastRunStatus,
			LastRunUpdatedAt: formatReleaseLastSeen(nativeSummary.LastRunUpdatedAt),
			LastRunLastError: nativeSummary.LastRunLastError,
		},
	}
	if regressionStore, ok := h.releases.(releaseRegressionReader); ok {
		regression, err := regressionStore.GetReleaseRegression(ctx, orgSlug, version)
		if err != nil {
			http.Error(w, "Failed to load release.", http.StatusInternalServerError)
			return
		}
		data.Regression = newReleaseRegressionData(regression)
	}
	if h.blobStore != nil && defaultProjectID != "" {
		statuses, err := h.nativeControl.ListReleaseDebugFiles(ctx, release.OrganizationID, defaultProjectID, version)
		if err != nil {
			http.Error(w, "Failed to load release.", http.StatusInternalServerError)
			return
		}
		for _, item := range statuses {
			if item.File == nil {
				continue
			}
			data.DebugFiles = append(data.DebugFiles, releaseDebugFileRow{
				ID:                  item.File.ID,
				ProjectID:           item.File.ProjectID,
				Name:                item.File.Name,
				Kind:                item.File.Kind,
				SymbolicationStatus: item.SymbolicationStatus,
				ReprocessStatus:     item.ReprocessStatus,
				ReprocessLastError:  item.ReprocessLastError,
				DateReprocessed:     formatReleaseLastSeen(item.ReprocessUpdatedAt),
			})
		}
	}
	for _, run := range nativeRuns {
		scope := "release"
		if run.DebugFileID != "" {
			scope = "debug file"
		}
		data.Backfills = append(data.Backfills, releaseBackfillRow{
			ID:          run.ID,
			Status:      string(run.Status),
			Scope:       scope,
			Processed:   formatNumber(run.ProcessedItems),
			Updated:     formatNumber(run.UpdatedItems),
			Failed:      formatNumber(run.FailedItems),
			LastError:   run.LastError,
			DateUpdated: formatReleaseLastSeen(run.UpdatedAt),
		})
	}
	for _, deploy := range deploys {
		data.Deploys = append(data.Deploys, releaseDeployRow{
			Environment:  deploy.Environment,
			Name:         deploy.Name,
			URL:          deploy.URL,
			DateStarted:  formatReleaseTimestamp(deploy.DateStarted),
			DateFinished: formatReleaseTimestamp(deploy.DateFinished),
			DateCreated:  timeAgo(deploy.DateCreated),
		})
	}
	for _, commit := range commits {
		data.Commits = append(data.Commits, releaseCommitRow{
			CommitSHA:   commit.CommitSHA,
			Repository:  commit.Repository,
			Author:      firstNonEmptyText(commit.AuthorName, commit.AuthorEmail),
			Message:     commit.Message,
			Files:       strings.Join(commit.Files, ", "),
			DateCreated: timeAgo(commit.DateCreated),
		})
	}
	for _, suspect := range suspects {
		data.Suspects = append(data.Suspects, releaseSuspectRow{
			GroupID:     suspect.GroupID,
			ShortID:     formatIssueShortID(suspect.ShortID, suspect.GroupID),
			Title:       suspect.Title,
			Culprit:     suspect.Culprit,
			CommitSHA:   suspect.CommitSHA,
			Author:      suspect.AuthorName,
			Message:     suspect.Message,
			MatchedFile: suspect.MatchedFile,
			LastSeen:    timeAgo(suspect.LastSeen),
		})
	}
	for _, item := range highlights {
		data.Profiles = append(data.Profiles, releaseProfileRow{
			ID:          item.ProfileID,
			Transaction: item.Transaction,
			Release:     item.Release,
			Environment: item.Environment,
			Duration:    formatProfileDuration(item.DurationNS),
			SampleCount: formatNumber(item.SampleCount),
			TopFunction: item.TopFunction,
			TraceID:     item.TraceID,
			StartedAt:   timeAgo(item.StartedAt),
		})
	}
	h.render(w, "release-detail.html", data)
}

func (h *Handler) createReleaseNativeReprocess(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		http.Error(w, "project id is required", http.StatusBadRequest)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeOrgAdmin); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	orgSlug, _, err := h.defaultReleaseContext(r.Context())
	if err != nil || orgSlug == "" {
		http.Error(w, "release context unavailable", http.StatusInternalServerError)
		return
	}
	org, err := h.catalog.GetOrganization(r.Context(), orgSlug)
	if err != nil || org == nil {
		http.Error(w, "organization not found", http.StatusNotFound)
		return
	}
	if _, err := h.nativeControl.CreateRun(r.Context(), sqlite.CreateNativeReprocessRun{
		OrganizationID: org.ID,
		ProjectID:      projectID,
		ReleaseVersion: strings.TrimSpace(r.PathValue("version")),
		Principal:      auth.PrincipalFromContext(r.Context()),
		RequestedVia:   "release_reprocess_web",
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	}); err != nil {
		http.Error(w, "failed to create reprocess run", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/releases/"+r.PathValue("version")+"/", http.StatusSeeOther)
}

func (h *Handler) createDebugFileNativeReprocess(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	debugFileID := strings.TrimSpace(r.PathValue("debug_file_id"))
	if projectID == "" || debugFileID == "" {
		http.Error(w, "project id and debug file id are required", http.StatusBadRequest)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeOrgAdmin); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	orgSlug, _, err := h.defaultReleaseContext(r.Context())
	if err != nil || orgSlug == "" {
		http.Error(w, "release context unavailable", http.StatusInternalServerError)
		return
	}
	org, err := h.catalog.GetOrganization(r.Context(), orgSlug)
	if err != nil || org == nil {
		http.Error(w, "organization not found", http.StatusNotFound)
		return
	}
	if _, err := h.nativeControl.CreateRun(r.Context(), sqlite.CreateNativeReprocessRun{
		OrganizationID: org.ID,
		ProjectID:      projectID,
		ReleaseVersion: strings.TrimSpace(r.PathValue("version")),
		DebugFileID:    debugFileID,
		Principal:      auth.PrincipalFromContext(r.Context()),
		RequestedVia:   "debug_file_reprocess_web",
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	}); err != nil {
		http.Error(w, "failed to create reprocess run", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/releases/"+r.PathValue("version")+"/", http.StatusSeeOther)
}

func (h *Handler) createReleaseDeploy(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		http.Error(w, "project id is required", http.StatusBadRequest)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeReleaseWrite); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	orgSlug, _, err := h.defaultReleaseContext(r.Context())
	if err != nil || orgSlug == "" {
		http.Error(w, "release context unavailable", http.StatusInternalServerError)
		return
	}
	_, err = h.releases.AddDeploy(r.Context(), orgSlug, r.PathValue("version"), sharedstore.ReleaseDeploy{
		Environment:  strings.TrimSpace(r.FormValue("environment")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		URL:          strings.TrimSpace(r.FormValue("url")),
		DateStarted:  parseReleaseFormTime(r.FormValue("date_started")),
		DateFinished: parseReleaseFormTime(r.FormValue("date_finished")),
	})
	if err != nil {
		http.Error(w, "failed to create deploy", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/releases/"+r.PathValue("version")+"/", http.StatusSeeOther)
}

func (h *Handler) createReleaseCommit(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		http.Error(w, "project id is required", http.StatusBadRequest)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeReleaseWrite); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	orgSlug, _, err := h.defaultReleaseContext(r.Context())
	if err != nil || orgSlug == "" {
		http.Error(w, "release context unavailable", http.StatusInternalServerError)
		return
	}
	_, err = h.releases.AddCommit(r.Context(), orgSlug, r.PathValue("version"), sharedstore.ReleaseCommit{
		CommitSHA:   strings.TrimSpace(r.FormValue("commit_sha")),
		Repository:  strings.TrimSpace(r.FormValue("repository")),
		AuthorName:  strings.TrimSpace(r.FormValue("author_name")),
		AuthorEmail: strings.TrimSpace(r.FormValue("author_email")),
		Message:     strings.TrimSpace(r.FormValue("message")),
		Files:       splitCommaList(r.FormValue("files")),
	})
	if err != nil {
		http.Error(w, "failed to create release commit", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/releases/"+r.PathValue("version")+"/", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// SQLite query helpers for releases
// ---------------------------------------------------------------------------

type dbRelease struct {
	version          string
	createdAt        time.Time
	eventCount       int
	sessionCount     int
	erroredSessions  int
	crashedSessions  int
	abnormalSessions int
	affectedUsers    int
	crashFreeRate    float64
	lastSessionAt    time.Time
}

func (h *Handler) listReleasesDB(ctx context.Context, limit int) ([]dbRelease, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := h.webStore.ListReleases(ctx, limit)
	if err != nil {
		return nil, err
	}
	releases := make([]dbRelease, 0, len(rows))
	for _, row := range rows {
		releases = append(releases, dbRelease{
			version:          row.Version,
			createdAt:        row.CreatedAt,
			eventCount:       row.EventCount,
			sessionCount:     row.SessionCount,
			erroredSessions:  row.ErroredSessions,
			crashedSessions:  row.CrashedSessions,
			abnormalSessions: row.AbnormalSessions,
			affectedUsers:    row.AffectedUsers,
			crashFreeRate:    row.CrashFreeRate,
			lastSessionAt:    row.LastSessionAt,
		})
	}
	return releases, nil
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

func formatReleaseLastSeen(ts time.Time) string {
	if ts.IsZero() {
		return "No sessions"
	}
	return timeAgo(ts)
}

func formatReleaseTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return "—"
	}
	return ts.Format("2006-01-02 15:04")
}

func formatIssueShortID(shortID int, fallback string) string {
	if shortID > 0 {
		return fmt.Sprintf("GENTRY-%d", shortID)
	}
	return fallback
}

func parseReleaseFormTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse("2006-01-02T15:04", raw); err == nil {
		return ts.UTC()
	}
	return time.Time{}
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func (h *Handler) defaultReleaseContext(ctx context.Context) (orgSlug, projectID string, err error) {
	projects, err := h.catalog.ListProjects(ctx, "")
	if err != nil || len(projects) == 0 {
		return "", "", err
	}
	return projects[0].OrgSlug, projects[0].ID, nil
}
