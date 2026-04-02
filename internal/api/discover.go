package api

import (
	"net/http"
	"strconv"
	"strings"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/telemetryquery"
)

func handleListOrganizationIssues(catalog controlplane.CatalogStore, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		limit := discoverLimit(r, 50)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadOrgIssues,
			Limit:    limit,
			Query:    query,
			Scope:    filter,
		}) {
			return
		}

		rows, err := queries.SearchDiscoverIssues(r.Context(), orgSlug, filter, query, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization issues.")
			return
		}
		resp := make([]Issue, 0, len(rows))
		for _, row := range rows {
			resp = append(resp, apiIssueFromDiscoverIssue(row))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleDiscover(catalog controlplane.CatalogStore, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		limit := discoverLimit(r, 25)
		if scope == "" {
			scope = "all"
		}
		workload := sqlite.QueryWorkloadDiscover
		switch scope {
		case "issues":
			workload = sqlite.QueryWorkloadOrgIssues
		case "logs":
			workload = sqlite.QueryWorkloadLogs
		case "transactions":
			workload = sqlite.QueryWorkloadTransactions
		}
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: workload,
			Limit:    limit,
			Query:    query,
			Scope:    scope,
		}) {
			return
		}

		resp := DiscoverResponse{Query: query, Scope: scope}

		if scope != "" && scope != "all" && scope != "issues" && scope != "logs" && scope != "transactions" {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid discover scope.")
			return
		}

		if scope == "" || scope == "all" || scope == "issues" {
			issues, err := queries.SearchDiscoverIssues(r.Context(), orgSlug, filter, query, limit)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list discover issues.")
				return
			}
			resp.Issues = issues
		}
		if scope == "" || scope == "all" || scope == "logs" {
			logs, err := queries.SearchLogs(r.Context(), orgSlug, query, limit)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list discover logs.")
				return
			}
			resp.Logs = logs
		}
		if scope == "" || scope == "all" || scope == "transactions" {
			txns, err := queries.SearchTransactions(r.Context(), orgSlug, query, limit)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list discover transactions.")
				return
			}
			resp.Transactions = txns
		}

		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleListOrganizationLogs(catalog controlplane.CatalogStore, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		limit := discoverLimit(r, 50)
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if !enforceQueryGuard(w, r, guard, org.ID, "", sqlite.QueryEstimate{
			Workload: sqlite.QueryWorkloadLogs,
			Limit:    limit,
			Query:    query,
		}) {
			return
		}

		var rows []DiscoverLog
		var logErr error
		if query == "" {
			rows, logErr = queries.ListRecentLogs(r.Context(), orgSlug, limit)
		} else {
			rows, logErr = queries.SearchLogs(r.Context(), orgSlug, query, limit)
		}
		if logErr != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization logs.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rows)
	}
}

func discoverLimit(r *http.Request, fallback int) int {
	limit := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	return limit
}

func apiIssueFromDiscoverIssue(row DiscoverIssue) Issue {
	shortID := row.ID
	if row.ShortID > 0 {
		shortID = "GENTRY-" + strconv.Itoa(row.ShortID)
	}
	extras := issueResponseExtras{
		AssignedTo:   apiIssueAssignee(row.Assignee),
		HasSeen:      true,
		IsBookmarked: false,
		IsPublic:     false,
		IsSubscribed: false,
		Priority:     row.Priority,
		Metadata:     issueMetadataFromParts(row.Title, row.Culprit),
		Type:         "error",
		NumComments:  0,
		UserCount:    0,
		Stats:        IssueStats{Last24Hours: []int{}},
	}
	return Issue{
		ID:           row.ID,
		ShortID:      shortID,
		Title:        row.Title,
		Culprit:      row.Culprit,
		Level:        row.Level,
		Status:       row.Status,
		Type:         extras.Type,
		AssignedTo:   extras.AssignedTo,
		HasSeen:      extras.HasSeen,
		IsBookmarked: extras.IsBookmarked,
		IsPublic:     extras.IsPublic,
		IsSubscribed: extras.IsSubscribed,
		Priority:     extras.Priority,
		Metadata:     extras.Metadata,
		NumComments:  extras.NumComments,
		UserCount:    extras.UserCount,
		Stats:        extras.Stats,
		FirstSeen:    row.FirstSeen,
		LastSeen:     row.LastSeen,
		Count:        int(row.Count),
		ProjectRef: ProjectRef{
			ID:   row.ProjectID,
			Slug: row.ProjectSlug,
		},
	}
}
