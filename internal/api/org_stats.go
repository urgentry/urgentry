package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/integration"
	"urgentry/internal/sqlite"
)

// handleListProcessingIssues returns processing issues for a project.
func handleListProcessingIssues(auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"hasMore":                  false,
			"hasMoreResolveableIssues": false,
			"numIssues":                0,
			"lastSeen":                 nil,
			"signedLink":               nil,
			"issues":                   []any{},
		})
	}
}

// handleRootCapabilities returns the API root with version and capabilities.
func handleRootCapabilities() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"version": "0",
			"auth": map[string]any{
				"login": map[string]string{
					"url": "/auth/login/",
				},
			},
			"user": nil,
			"features": map[string]bool{
				"organizations:discover":   true,
				"organizations:events":     true,
				"organizations:monitors":   true,
				"organizations:replays":    true,
				"organizations:profiles":   true,
				"organizations:dashboards": true,
				"organizations:alerts":     true,
			},
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/events-timeseries/
// ---------------------------------------------------------------------------

// handleListEventTimeSeries returns event counts bucketed by interval.
func handleListEventTimeSeries(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")

		interval := strings.TrimSpace(r.URL.Query().Get("interval"))
		if interval == "" {
			interval = "1h"
		}
		// Determine the SQLite date truncation format.
		var sqliteTrunc string
		switch interval {
		case "1d", "24h":
			sqliteTrunc = "%Y-%m-%d"
		default: // default to hourly
			sqliteTrunc = "%Y-%m-%dT%H:00:00"
		}

		// Parse start/end from query params; default to last 24 hours.
		now := time.Now().UTC()
		start := parseTimeParam(r, "start", now.Add(-24*time.Hour))
		end := parseTimeParam(r, "end", now)

		rows, err := db.QueryContext(r.Context(),
			`SELECT strftime(?, e.timestamp) AS bucket, COUNT(*)
			 FROM events e
			 JOIN projects p ON p.id = e.project_id
			 JOIN organizations o ON o.id = p.organization_id
			 WHERE o.slug = ?
			   AND e.timestamp >= ?
			   AND e.timestamp <= ?
			 GROUP BY bucket
			 ORDER BY bucket`,
			sqliteTrunc, orgSlug,
			start.Format(time.RFC3339), end.Format(time.RFC3339),
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to query event time series.")
			return
		}
		defer rows.Close()

		var series [][]any
		for rows.Next() {
			var bucket string
			var count int
			if err := rows.Scan(&bucket, &count); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan time series row.")
				return
			}
			series = append(series, []any{bucket, count})
		}
		if series == nil {
			series = [][]any{}
		}
		httputil.WriteJSON(w, http.StatusOK, series)
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/project-keys/
// ---------------------------------------------------------------------------

// handleListOrgProjectKeys returns all DSN keys across every project in the org.
func handleListOrgProjectKeys(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")

		// Get the org's projects so we can filter the all-keys list.
		projects, err := catalog.ListProjects(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}
		projectIDs := make(map[string]struct{}, len(projects))
		for _, p := range projects {
			projectIDs[p.ID] = struct{}{}
		}

		allKeys, err := catalog.ListAllProjectKeys(r.Context())
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list project keys.")
			return
		}

		keys := make([]*ProjectKey, 0, len(allKeys))
		for _, rec := range allKeys {
			if _, ok := projectIDs[rec.ProjectID]; !ok {
				continue
			}
			keys = append(keys, apiProjectKeyFromMeta(r, rec))
		}
		httputil.WriteJSON(w, http.StatusOK, keys)
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/repos/
// ---------------------------------------------------------------------------

// RepoResponse is the API response for a repository.
type RepoResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	URL          string `json:"url"`
	ExternalSlug string `json:"externalSlug,omitempty"`
	Status       string `json:"status"`
	DateCreated  string `json:"dateCreated,omitempty"`
}

// handleListOrgRepos lists repositories for an organization.
func handleListOrgRepos(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteJSON(w, http.StatusOK, []any{})
			return
		}
		rows, err := db.QueryContext(r.Context(),
			`SELECT id, name, provider, url, external_slug, status, created_at
			 FROM repositories WHERE organization_id = ? ORDER BY created_at DESC`, org.ID)
		if err != nil {
			httputil.WriteJSON(w, http.StatusOK, []any{})
			return
		}
		defer rows.Close()
		repos := make([]RepoResponse, 0, 8)
		for rows.Next() {
			var repo RepoResponse
			var createdAt sql.NullString
			if err := rows.Scan(&repo.ID, &repo.Name, &repo.Provider, &repo.URL, &repo.ExternalSlug, &repo.Status, &createdAt); err != nil {
				continue
			}
			if createdAt.Valid {
				repo.DateCreated = createdAt.String
			}
			repos = append(repos, repo)
		}
		httputil.WriteJSON(w, http.StatusOK, repos)
	}
}

// handleCreateOrgRepo creates a repository for an organization.
func handleCreateOrgRepo(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		var body struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
			URL      string `json:"url"`
		}
		if err := decodeJSON(r, &body); err != nil || body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "name is required.")
			return
		}
		if body.Provider == "" {
			body.Provider = "manual"
		}
		repoID := generateAPIID()
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO repositories (id, organization_id, name, provider, url) VALUES (?, ?, ?, ?, ?)`,
			repoID, org.ID, body.Name, body.Provider, body.URL)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create repository.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, RepoResponse{
			ID:       repoID,
			Name:     body.Name,
			Provider: body.Provider,
			URL:      body.URL,
			Status:   "active",
		})
	}
}

// handleListRepoCommits lists commits for a repository.
func handleListRepoCommits(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		repoID := PathParam(r, "repo_id")
		// Scope to org to prevent IDOR.
		rows, err := db.QueryContext(r.Context(),
			`SELECT rc.id, rc.commit_sha, rc.repository, rc.author_name, rc.author_email, rc.message, rc.created_at
			 FROM release_commits rc
			 JOIN repositories r ON r.id = rc.repository
			 JOIN organizations o ON o.id = r.organization_id
			 WHERE o.slug = ? AND rc.repository = ?
			 ORDER BY rc.created_at DESC LIMIT 100`, orgSlug, repoID)
		if err != nil {
			httputil.WriteJSON(w, http.StatusOK, []any{})
			return
		}
		defer rows.Close()
		type CommitResponse struct {
			ID          string `json:"id"`
			SHA         string `json:"sha,omitempty"`
			Repository  string `json:"repository,omitempty"`
			AuthorName  string `json:"authorName,omitempty"`
			AuthorEmail string `json:"authorEmail,omitempty"`
			Message     string `json:"message,omitempty"`
			DateCreated string `json:"dateCreated,omitempty"`
		}
		commits := make([]CommitResponse, 0, 16)
		for rows.Next() {
			var c CommitResponse
			var createdAt sql.NullString
			if err := rows.Scan(&c.ID, &c.SHA, &c.Repository, &c.AuthorName, &c.AuthorEmail, &c.Message, &createdAt); err != nil {
				continue
			}
			if createdAt.Valid {
				c.DateCreated = createdAt.String
			}
			commits = append(commits, c)
		}
		httputil.WriteJSON(w, http.StatusOK, commits)
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/stats-summary/
// ---------------------------------------------------------------------------

// handleGetStatsSummary returns event counts grouped by project.
func handleGetStatsSummary(db *sql.DB, catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")

		now := time.Now().UTC()
		start := parseTimeParam(r, "start", now.Add(-24*time.Hour))
		end := parseTimeParam(r, "end", now)

		projects, err := catalog.ListProjects(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}

		type statsBlock struct {
			Received [][]any `json:"received"`
		}
		type projectStats struct {
			ID    string     `json:"id"`
			Slug  string     `json:"slug"`
			Stats statsBlock `json:"stats"`
		}

		projectList := make([]projectStats, 0, len(projects))
		for _, p := range projects {
			rows, err := db.QueryContext(r.Context(),
				`SELECT strftime('%Y-%m-%dT%H:00:00', e.timestamp) AS bucket, COUNT(*)
				 FROM events e
				 WHERE e.project_id = ?
				   AND e.timestamp >= ?
				   AND e.timestamp <= ?
				 GROUP BY bucket
				 ORDER BY bucket`,
				p.ID, start.Format(time.RFC3339), end.Format(time.RFC3339),
			)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to query stats.")
				return
			}
			var series [][]any
			for rows.Next() {
				var bucket string
				var count int
				if err := rows.Scan(&bucket, &count); err != nil {
					rows.Close()
					httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan stats row.")
					return
				}
				series = append(series, []any{bucket, count})
			}
			rows.Close()
			if series == nil {
				series = [][]any{}
			}
			projectList = append(projectList, projectStats{
				ID:    p.ID,
				Slug:  p.Slug,
				Stats: statsBlock{Received: series},
			})
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"start":    start.Format(time.RFC3339),
			"end":      end.Format(time.RFC3339),
			"projects": projectList,
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/stats_v2/
// ---------------------------------------------------------------------------

// handleGetStatsV2 returns event counts with outcome grouping.
func handleGetStatsV2(db *sql.DB, _ *sqlite.OutcomeStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")

		now := time.Now().UTC()
		start := parseTimeParam(r, "start", now.Add(-24*time.Hour))
		end := parseTimeParam(r, "end", now)

		// Count accepted events from the events table.
		rows, err := db.QueryContext(r.Context(),
			`SELECT strftime('%Y-%m-%dT%H:00:00', e.timestamp) AS bucket, COUNT(*)
			 FROM events e
			 JOIN projects p ON p.id = e.project_id
			 JOIN organizations o ON o.id = p.organization_id
			 WHERE o.slug = ?
			   AND e.timestamp >= ?
			   AND e.timestamp <= ?
			 GROUP BY bucket
			 ORDER BY bucket`,
			orgSlug, start.Format(time.RFC3339), end.Format(time.RFC3339),
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to query stats_v2.")
			return
		}
		defer rows.Close()

		var intervals []string
		var acceptedSeries []int
		var total int
		for rows.Next() {
			var bucket string
			var count int
			if err := rows.Scan(&bucket, &count); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan stats_v2 row.")
				return
			}
			intervals = append(intervals, bucket)
			acceptedSeries = append(acceptedSeries, count)
			total += count
		}
		if intervals == nil {
			intervals = []string{}
		}
		if acceptedSeries == nil {
			acceptedSeries = []int{}
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"start":     start.Format(time.RFC3339),
			"end":       end.Format(time.RFC3339),
			"intervals": intervals,
			"groups": []map[string]any{
				{
					"by":     map[string]string{"outcome": "accepted"},
					"totals": map[string]int{"sum(quantity)": total},
					"series": map[string][]int{"sum(quantity)": acceptedSeries},
				},
			},
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/sessions/
// ---------------------------------------------------------------------------

// handleListOrgSessions returns release-health session stats for the org.
func handleListOrgSessions(db *sql.DB, _ *sqlite.ReleaseHealthStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")

		now := time.Now().UTC()
		start := parseTimeParam(r, "start", now.Add(-24*time.Hour))
		end := parseTimeParam(r, "end", now)

		// Query sessions bucketed by hour and status.
		rows, err := db.QueryContext(r.Context(),
			`SELECT strftime('%Y-%m-%dT%H:00:00', rs.created_at) AS bucket,
			        rs.status,
			        COALESCE(SUM(rs.quantity), 0)
			 FROM release_sessions rs
			 JOIN projects p ON p.id = rs.project_id
			 JOIN organizations o ON o.id = p.organization_id
			 WHERE o.slug = ?
			   AND rs.created_at >= ?
			   AND rs.created_at <= ?
			 GROUP BY bucket, rs.status
			 ORDER BY bucket`,
			orgSlug, start.Format(time.RFC3339), end.Format(time.RFC3339),
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to query sessions.")
			return
		}
		defer rows.Close()

		// Collect per-status totals and per-bucket counts.
		type statusEntry struct {
			total  int
			series map[string]int // bucket -> count
		}
		statuses := make(map[string]*statusEntry)
		var allBuckets []string
		bucketSeen := make(map[string]struct{})

		for rows.Next() {
			var bucket, status string
			var count int
			if err := rows.Scan(&bucket, &status, &count); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan session row.")
				return
			}
			if _, ok := bucketSeen[bucket]; !ok {
				bucketSeen[bucket] = struct{}{}
				allBuckets = append(allBuckets, bucket)
			}
			entry, ok := statuses[status]
			if !ok {
				entry = &statusEntry{series: make(map[string]int)}
				statuses[status] = entry
			}
			entry.total += count
			entry.series[bucket] += count
		}

		if allBuckets == nil {
			allBuckets = []string{}
		}

		// If no data, include a default "ok" group.
		if len(statuses) == 0 {
			statuses["ok"] = &statusEntry{series: make(map[string]int)}
		}

		groups := make([]map[string]any, 0, len(statuses))
		for status, entry := range statuses {
			seriesList := make([]int, len(allBuckets))
			for i, b := range allBuckets {
				seriesList[i] = entry.series[b]
			}
			groups = append(groups, map[string]any{
				"by":     map[string]string{"session.status": status},
				"totals": map[string]int{"sum(session)": entry.total},
				"series": map[string][]int{"sum(session)": seriesList},
			})
		}

		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"start":     start.Format(time.RFC3339),
			"end":       end.Format(time.RFC3339),
			"intervals": allBuckets,
			"groups":    groups,
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/0/organizations/{org_slug}/config/integrations/
// ---------------------------------------------------------------------------

// integrationProviderSummary is the response shape for config/integrations/.
type integrationProviderSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// handleListIntegrationConfigs returns available integration provider configs.
func handleListIntegrationConfigs(registry *integration.Registry, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		all := registry.All()
		providers := make([]integrationProviderSummary, 0, len(all))
		for _, impl := range all {
			providers = append(providers, integrationProviderSummary{
				Key:         impl.ID(),
				Name:        impl.Name(),
				Description: impl.Description(),
			})
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"providers": providers,
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseTimeParam parses an RFC3339 or Unix timestamp from the query string.
// Returns fallback if the parameter is missing or malformed.
func parseTimeParam(r *http.Request, key string, fallback time.Time) time.Time {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC()
	}
	// Try date-only format.
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC()
	}
	return fallback
}
