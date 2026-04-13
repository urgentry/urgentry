package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

func handleProfileTopDown(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return handleProfileTreeQuery(db, queries, guard, auth, "top_down")
}

func handleProfileBottomUp(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return handleProfileTreeQuery(db, queries, guard, auth, "bottom_up")
}

func handleProfileFlamegraph(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return handleProfileTreeQuery(db, queries, guard, auth, "flamegraph")
}

func handleProfileHotPath(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, projectID, ok := resolveProfileQueryScope(w, r, db, auth)
		if !ok {
			return
		}
		filter, ok := parseProfileQueryFilter(w, r)
		if !ok {
			return
		}
		if !enforceProfileQueryGuard(w, r, guard, orgID, projectID, profileTreeQueryEstimate("hot_path", filter)) {
			return
		}
		item, err := queries.QueryHotPath(r.Context(), projectID, filter)
		if !writeProfileQueryError(w, err) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, item)
	}
}

func handleCompareProfiles(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, projectID, ok := resolveProfileQueryScope(w, r, db, auth)
		if !ok {
			return
		}
		filter := sharedstore.ProfileComparisonFilter{
			BaselineProfileID:  strings.TrimSpace(r.URL.Query().Get("baseline")),
			CandidateProfileID: strings.TrimSpace(r.URL.Query().Get("candidate")),
			ThreadID:           strings.TrimSpace(r.URL.Query().Get("thread")),
		}
		if filter.BaselineProfileID == "" || filter.CandidateProfileID == "" {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "profile_compare_targets_required",
				Detail: "baseline and candidate are required.",
			})
			return
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("max_functions")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				httputil.WriteAPIError(w, httputil.APIError{
					Status: http.StatusBadRequest,
					Code:   "invalid_max_functions",
					Detail: "max_functions must be a positive integer.",
				})
				return
			}
			filter.MaxFunctions = value
		}
		if !enforceProfileQueryGuard(w, r, guard, orgID, projectID, profileComparisonQueryEstimate(filter)) {
			return
		}
		item, err := queries.CompareProfiles(r.Context(), projectID, filter)
		if !writeProfileQueryError(w, err) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, item)
	}
}

func handleProfileTreeQuery(db *sql.DB, queries telemetryquery.Service, guard sqlite.QueryGuard, auth authFunc, mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, projectID, ok := resolveProfileQueryScope(w, r, db, auth)
		if !ok {
			return
		}
		filter, ok := parseProfileQueryFilter(w, r)
		if !ok {
			return
		}
		if !enforceProfileQueryGuard(w, r, guard, orgID, projectID, profileTreeQueryEstimate(mode, filter)) {
			return
		}
		var (
			item *sharedstore.ProfileTree
			err  error
		)
		switch mode {
		case "bottom_up":
			item, err = queries.QueryBottomUp(r.Context(), projectID, filter)
		case "flamegraph":
			item, err = queries.QueryFlamegraph(r.Context(), projectID, filter)
		default:
			item, err = queries.QueryTopDown(r.Context(), projectID, filter)
		}
		if !writeProfileQueryError(w, err) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, item)
	}
}

func resolveProfileQueryScope(w http.ResponseWriter, r *http.Request, db *sql.DB, auth authFunc) (string, string, bool) {
	if !auth(w, r) {
		return "", "", false
	}
	projectID, ok := resolveProjectID(w, r, db)
	if !ok {
		return "", "", false
	}
	org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
		return "", "", false
	}
	if org == nil {
		httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
		return "", "", false
	}
	return org.ID, projectID, true
}

func enforceProfileQueryGuard(w http.ResponseWriter, r *http.Request, guard sqlite.QueryGuard, orgID, projectID string, estimate sqlite.QueryEstimate) bool {
	return enforceQueryGuard(w, r, guard, orgID, projectID, estimate)
}

func parseProfileQueryFilter(w http.ResponseWriter, r *http.Request) (sharedstore.ProfileQueryFilter, bool) {
	filter := sharedstore.ProfileQueryFilter{
		ProfileID:   strings.TrimSpace(r.URL.Query().Get("profile_id")),
		ThreadID:    strings.TrimSpace(r.URL.Query().Get("thread")),
		FrameFilter: strings.TrimSpace(r.URL.Query().Get("frame")),
		Transaction: strings.TrimSpace(r.URL.Query().Get("transaction")),
		Release:     strings.TrimSpace(r.URL.Query().Get("release")),
		Environment: strings.TrimSpace(r.URL.Query().Get("environment")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_depth")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_max_depth",
				Detail: "max_depth must be a positive integer.",
			})
			return sharedstore.ProfileQueryFilter{}, false
		}
		filter.MaxDepth = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_nodes")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_max_nodes",
				Detail: "max_nodes must be a positive integer.",
			})
			return sharedstore.ProfileQueryFilter{}, false
		}
		filter.MaxNodes = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("start")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_profile_query_start",
				Detail: "start must be RFC3339.",
			})
			return sharedstore.ProfileQueryFilter{}, false
		}
		filter.StartedAfter = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("end")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Code:   "invalid_profile_query_end",
				Detail: "end must be RFC3339.",
			})
			return sharedstore.ProfileQueryFilter{}, false
		}
		filter.EndedBefore = value
	}
	return filter, true
}

func profileTreeQueryEstimate(mode string, filter sharedstore.ProfileQueryFilter) sqlite.QueryEstimate {
	limit := filter.MaxNodes
	if limit <= 0 {
		limit = 512
	}
	queryParts := []string{mode}
	for _, value := range []string{filter.ProfileID, filter.ThreadID, filter.FrameFilter, filter.Transaction, filter.Release, filter.Environment} {
		if strings.TrimSpace(value) != "" {
			queryParts = append(queryParts, value)
		}
	}
	if !filter.StartedAfter.IsZero() {
		queryParts = append(queryParts, "start")
	}
	if !filter.EndedBefore.IsZero() {
		queryParts = append(queryParts, "end")
	}
	if filter.MaxDepth > 0 {
		queryParts = append(queryParts, "depth")
	}
	return sqlite.QueryEstimate{
		Workload: sqlite.QueryWorkloadProfiles,
		Limit:    max(1, limit/16),
		Query:    strings.Join(queryParts, " "),
		Scope:    mode,
	}
}

func profileComparisonQueryEstimate(filter sharedstore.ProfileComparisonFilter) sqlite.QueryEstimate {
	limit := filter.MaxFunctions
	if limit <= 0 {
		limit = 10
	}
	queryParts := []string{"compare", filter.BaselineProfileID, filter.CandidateProfileID}
	if strings.TrimSpace(filter.ThreadID) != "" {
		queryParts = append(queryParts, filter.ThreadID)
	}
	return sqlite.QueryEstimate{
		Workload: sqlite.QueryWorkloadProfiles,
		Limit:    max(2, limit*4),
		Query:    strings.Join(queryParts, " "),
		Scope:    "compare",
	}
}

func writeProfileQueryError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, sharedstore.ErrNotFound):
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusNotFound,
			Code:   "profile_query_not_found",
			Detail: "Profile query target not found.",
		})
	case errors.Is(err, sharedstore.ErrQueryTooLarge):
		w.Header().Set("Retry-After", "60")
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusTooManyRequests,
			Code:   "profile_query_too_large",
			Detail: "Profile query exceeds resource limits. Try a narrower time range or fewer functions.",
		})
	case strings.Contains(err.Error(), "not query-ready"):
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusConflict,
			Code:   "profile_not_query_ready",
			Detail: "Profile is not query-ready.",
		})
	default:
		httputil.WriteAPIError(w, httputil.APIError{
			Status: http.StatusInternalServerError,
			Code:   "profile_query_failed",
			Detail: "Failed to query profile.",
		})
	}
	return false
}
