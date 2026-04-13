package web

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"urgentry/internal/analyticsreport"
	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type discoverPageData struct {
	Title         string
	Nav           string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Action        string
	CurrentURL    string
	ExportCSVURL  string
	ExportJSONURL string
	Guide         analyticsGuide
	StarterViews  []analyticsStarterViewCard
	State         discoverBuilderState
	SavedQueries  []discoverSavedQuery
	Explain       discoverExplainView
	Result        discoverResultView
	Error         string
	ErrorDetails  []string
}

type discoverQueryDetailData struct {
	Title              string
	Nav                string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	CurrentURL         string
	Saved              discoverSavedQuery
	CanManage          bool
	TagCSV             string
	OpenURL            string
	ExportCSVURL       string
	ExportJSONURL      string
	SnapshotAction     string
	CloneName          string
	CloneAction        string
	FavoriteAction     string
	UpdateAction       string
	DeleteAction       string
	ReportCreateAction string
	ReportSchedules    []analyticsReportScheduleView
	Explain            discoverExplainView
	Result             discoverResultView
	Error              string
	ErrorDetails       []string
}

func (h *Handler) discoverPage(w http.ResponseWriter, r *http.Request) {
	h.renderDiscoverPage(w, r, "Discover", "discover", string(discover.DatasetIssues))
}

func (h *Handler) logsPage(w http.ResponseWriter, r *http.Request) {
	h.renderDiscoverPage(w, r, "Logs", "logs", string(discover.DatasetLogs))
}

func (h *Handler) discoverStarterPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	starter, ok := lookupAnalyticsStarterView(r.PathValue("slug"))
	if !ok {
		writeWebNotFound(w, r, "Starter view not found")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeAnyMembership(r, auth.ScopeOrgQueryRead); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	queryDoc, err := buildAnalyticsStarterQuery(scope, starter, 50)
	if err != nil {
		writeWebBadRequest(w, r, "Failed to build starter view.")
		return
	}
	data := discoverPageData{
		Title:        starter.Name,
		Nav:          starter.Nav,
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Action:        discoverStarterAction(starter),
		CurrentURL:    r.URL.RequestURI(),
		ExportCSVURL:  exportURL(r.URL.RequestURI(), "csv"),
		ExportJSONURL: exportURL(r.URL.RequestURI(), "json"),
		Guide:         discoverGuide(starterSurfaceTitle(starter.Nav), starter.State.Dataset),
		StarterViews:  analyticsStarterViewCards(starter.Nav),
		State:         starter.State,
		Explain:       buildDiscoverExplain(queryDoc),
	}
	if h.searches != nil {
		principal := auth.PrincipalFromContext(r.Context())
		if principal != nil && principal.User != nil {
			saved, err := h.searches.List(r.Context(), principal.User.ID, scope.OrganizationSlug)
			if err != nil {
				writeWebInternal(w, r, "Failed to load saved queries.")
				return
			}
			data.SavedQueries = discoverSavedQueries(saved)
		}
	}
	if h.authz != nil && h.queryGuard != nil {
		workload := workloadForDataset(starter.State.Dataset)
		if starter.Nav == "discover" {
			workload = sqlite.QueryWorkloadDiscover
		}
		decision, err := h.queryGuard.CheckAndRecord(r.Context(), sqlite.QueryGuardRequest{
			Principal:      auth.PrincipalFromContext(r.Context()),
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			RequestPath:    r.URL.Path,
			RequestMethod:  r.Method,
			IPAddress:      r.RemoteAddr,
			UserAgent:      r.UserAgent(),
			Estimate: sqlite.QueryEstimate{
				Workload: workload,
				Limit:    50,
				Query:    starter.State.Query,
				Scope:    starter.State.Dataset,
			},
		})
		if err != nil {
			writeWebInternal(w, r, "Failed to apply query guardrails.")
			return
		}
		if !decision.Allowed {
			if decision.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))))
			}
			data.Error, data.ErrorDetails = discoverGuardFeedback(decision)
			h.renderStatus(w, decision.StatusCode, "discover.html", data)
			return
		}
	}
	result, err := executeDiscoverResult(r.Context(), h.queries, queryDoc, starter.State.Visualization)
	if err != nil {
		if normalizedExportFormat(r.URL.Query().Get("export")) != "" {
			message, _ := discoverErrorFeedback(err)
			writeWebBadRequest(w, r, message)
			return
		}
		data.Error, data.ErrorDetails = discoverErrorFeedback(err)
		h.render(w, "discover.html", data)
		return
	}
	data.Result = result
	if format := normalizedExportFormat(r.URL.Query().Get("export")); format != "" {
		writeAnalyticsExport(w, starter.Name, format, result)
		return
	}
	h.render(w, "discover.html", data)
}

func (h *Handler) discoverQueryDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	saved, err := h.discoverSavedQuery(r.Context(), scope.OrganizationSlug, r.PathValue("id"))
	if err != nil {
		writeWebInternal(w, r, "Failed to load saved query.")
		return
	}
	if saved == nil {
		writeWebNotFound(w, r, "Saved query not found")
		return
	}
	nav := "discover"
	if savedQueryDataset(*saved) == string(discover.DatasetLogs) {
		nav = "logs"
	}
	state := discoverStateFromSaved(*saved, savedQueryDataset(*saved), r.URL.Path)
	principal := auth.PrincipalFromContext(r.Context())
	canManage := principal != nil && principal.User != nil && principal.User.ID == saved.UserID
	data := discoverQueryDetailData{
		Title:              saved.Name,
		Nav:                nav,
		CurrentURL:         r.URL.RequestURI(),
		Saved:              discoverSavedQueries([]sqlite.SavedSearch{*saved})[0],
		CanManage:          canManage,
		TagCSV:             strings.Join(saved.Tags, ", "),
		OpenURL:            discoverSavedQueryURL(savedQueryPath(savedQueryDataset(*saved)), *saved),
		ExportCSVURL:       exportURL(r.URL.RequestURI(), "csv"),
		ExportJSONURL:      exportURL(r.URL.RequestURI(), "json"),
		SnapshotAction:     "/discover/queries/" + saved.ID + "/snapshot",
		CloneName:          saved.Name + " copy",
		CloneAction:        "/discover/queries/" + saved.ID + "/clone",
		FavoriteAction:     "/discover/queries/" + saved.ID + "/favorite",
		UpdateAction:       "/discover/queries/" + saved.ID + "/update",
		DeleteAction:       "/discover/queries/" + saved.ID + "/delete",
		ReportCreateAction: "/discover/queries/" + saved.ID + "/reports",
		Explain:            buildDiscoverExplain(saved.QueryDoc),
	}
	if h.reportSchedules != nil && principal != nil && principal.User != nil {
		schedules, err := h.reportSchedules.ListBySource(r.Context(), scope.OrganizationSlug, analyticsreport.SourceTypeSavedQuery, saved.ID, principal.User.ID)
		if err != nil {
			writeWebInternal(w, r, "Failed to load report schedules.")
			return
		}
		data.ReportSchedules = analyticsReportScheduleViews(schedules)
	}
	result, err := executeDiscoverResult(r.Context(), h.queries, saved.QueryDoc, state.Visualization)
	if err != nil {
		if normalizedExportFormat(r.URL.Query().Get("export")) != "" {
			message, _ := discoverErrorFeedback(err)
			writeWebBadRequest(w, r, message)
			return
		}
		data.Error, data.ErrorDetails = discoverErrorFeedback(err)
		h.render(w, "discover-query-detail.html", data)
		return
	}
	data.Result = result
	if format := normalizedExportFormat(r.URL.Query().Get("export")); format != "" {
		writeAnalyticsExport(w, saved.Name, format, result)
		return
	}
	h.render(w, "discover-query-detail.html", data)
}

func discoverStarterAction(starter analyticsStarterView) string {
	if starter.Nav == "logs" {
		return "/logs/"
	}
	return "/discover/"
}

func starterSurfaceTitle(nav string) string {
	if nav == "logs" {
		return "Logs"
	}
	return "Discover"
}

func (h *Handler) renderDiscoverPage(w http.ResponseWriter, r *http.Request, title, nav, defaultDataset string) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	state := discoverStateFromRequest(r, defaultDataset)
	initialScope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if saved, err := h.discoverSavedQuery(r.Context(), initialScope.OrganizationSlug, state.SavedID); err != nil {
		writeWebInternal(w, r, "Failed to load saved query.")
		return
	} else if saved != nil {
		state = mergeDiscoverSavedState(r.URL.Query(), state, discoverStateFromSaved(*saved, defaultDataset, r.URL.Path))
	}
	workload := workloadForDataset(state.Dataset)
	if nav == "discover" {
		workload = sqlite.QueryWorkloadDiscover
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeAnyMembership(r, auth.ScopeOrgQueryRead); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if strings.TrimSpace(initialScope.OrganizationSlug) == "" {
		writeWebBadRequest(w, r, "No organization scope available.")
		return
	}
	data := discoverPageData{
		Title:         title,
		Nav:           nav,
		Action:        r.URL.Path,
		CurrentURL:    r.URL.RequestURI(),
		ExportCSVURL:  exportURL(r.URL.RequestURI(), "csv"),
		ExportJSONURL: exportURL(r.URL.RequestURI(), "json"),
		Guide:         discoverGuide(title, state.Dataset),
		StarterViews:  analyticsStarterViewCards(nav),
		State:         state,
	}
	queryDoc, queryErr := buildDiscoverQuery(initialScope.OrganizationSlug, state, 50)
	if queryErr == nil {
		data.Explain = buildDiscoverExplain(queryDoc)
	}
	if principal != nil && principal.User != nil {
		saved, err := h.searches.List(r.Context(), principal.User.ID, initialScope.OrganizationSlug)
		if err != nil {
			data.Error = "Failed to load saved queries."
			h.render(w, "discover.html", data)
			return
		}
		data.SavedQueries = discoverSavedQueries(saved)
	}
	if queryErr != nil {
		if normalizedExportFormat(r.URL.Query().Get("export")) != "" {
			message, _ := discoverErrorFeedback(queryErr)
			writeWebBadRequest(w, r, message)
			return
		}
		data.Error, data.ErrorDetails = discoverErrorFeedback(queryErr)
		h.render(w, "discover.html", data)
		return
	}
	if h.authz != nil && h.queryGuard != nil {
		decision, err := h.queryGuard.CheckAndRecord(r.Context(), sqlite.QueryGuardRequest{
			Principal:      auth.PrincipalFromContext(r.Context()),
			OrganizationID: initialScope.OrganizationID,
			ProjectID:      initialScope.ProjectID,
			RequestPath:    r.URL.Path,
			RequestMethod:  r.Method,
			IPAddress:      r.RemoteAddr,
			UserAgent:      r.UserAgent(),
			Estimate: sqlite.QueryEstimate{
				Workload: workload,
				Limit:    50,
				Query:    state.Query,
				Scope:    state.Dataset,
			},
		})
		if err != nil {
			writeWebInternal(w, r, "Failed to apply query guardrails.")
			return
		}
		if !decision.Allowed {
			if decision.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))))
			}
			data.Error, data.ErrorDetails = discoverGuardFeedback(decision)
			h.renderStatus(w, decision.StatusCode, "discover.html", data)
			return
		}
	}
	if discoverUsesIssueSearchFastPath(state) {
		rows, err := h.queries.SearchDiscoverIssues(r.Context(), initialScope.OrganizationSlug, state.Filter, discoverIssueSearchQuery(state), 50)
		if err != nil {
			if normalizedExportFormat(r.URL.Query().Get("export")) != "" {
				message, _ := discoverErrorFeedback(err)
				writeWebBadRequest(w, r, message)
				return
			}
			data.Error, data.ErrorDetails = discoverErrorFeedback(err)
			h.render(w, "discover.html", data)
			return
		}
		data.Result = renderDiscoverIssueRows(rows)
		if format := normalizedExportFormat(r.URL.Query().Get("export")); format != "" {
			writeAnalyticsExport(w, title, format, data.Result)
			return
		}
		h.render(w, "discover.html", data)
		return
	}
	result, err := executeDiscoverResult(r.Context(), h.queries, queryDoc, state.Visualization)
	if err != nil {
		if normalizedExportFormat(r.URL.Query().Get("export")) != "" {
			message, _ := discoverErrorFeedback(err)
			writeWebBadRequest(w, r, message)
			return
		}
		data.Error, data.ErrorDetails = discoverErrorFeedback(err)
		h.render(w, "discover.html", data)
		return
	}
	data.Result = result
	if format := normalizedExportFormat(r.URL.Query().Get("export")); format != "" {
		writeAnalyticsExport(w, title, format, result)
		return
	}
	h.render(w, "discover.html", data)
}

func (h *Handler) saveDiscoverQuery(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		writeWebUnavailable(w, r, "Saved queries not available")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebForbidden(w, r)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeAnyMembership(r, auth.ScopeOrgQueryRead); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	if scope.OrganizationSlug == "" {
		writeWebBadRequest(w, r, "No organization scope available.")
		return
	}
	state := discoverStateFromValues(r.PostForm, string(discover.DatasetIssues))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" {
		writeWebBadRequest(w, r, "Name is required")
		return
	}
	queryDoc, err := buildDiscoverQuery(scope.OrganizationSlug, state, 50)
	if err != nil {
		var validationErrs discover.ValidationErrors
		if errors.As(err, &validationErrs) {
			writeWebBadRequest(w, r, validationErrs.Error())
			return
		}
		writeWebBadRequest(w, r, err.Error())
		return
	}
	saved, err := h.searches.SaveQuery(
		r.Context(),
		principal.User.ID,
		scope.OrganizationSlug,
		sqlite.SavedSearchVisibility(r.PostForm.Get("visibility")),
		name,
		strings.TrimSpace(r.PostForm.Get("description")),
		state.Query,
		state.Filter,
		state.Environment,
		"last_seen",
		discoverBool(r.PostForm.Get("favorite")),
		queryDoc,
	)
	if err != nil {
		var validationErrs discover.ValidationErrors
		if errors.As(err, &validationErrs) {
			writeWebBadRequest(w, r, validationErrs.Error())
			return
		}
		writeWebInternal(w, r, "Save failed: "+err.Error())
		return
	}
	http.Redirect(w, r, discoverSavedQueryURL(savedQueryPath(string(queryDoc.Dataset)), *saved), http.StatusSeeOther)
}

func (h *Handler) updateDiscoverQueryFavorite(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		writeWebUnavailable(w, r, "Saved queries not available")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebForbidden(w, r)
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeAnyMembership(r, auth.ScopeOrgQueryRead); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	if err := h.searches.SetFavorite(r.Context(), principal.User.ID, scope.OrganizationSlug, strings.TrimSpace(r.PathValue("id")), discoverBool(r.PostForm.Get("favorite"))); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeWebNotFound(w, r, "Saved query not found")
			return
		}
		writeWebInternal(w, r, "Favorite update failed")
		return
	}
	target := strings.TrimSpace(r.PostForm.Get("return_to"))
	if !strings.HasPrefix(target, "/") {
		target = "/discover/"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) cloneDiscoverQuery(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		writeWebUnavailable(w, r, "Saved queries not available")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	cloned, err := h.searches.Clone(
		r.Context(),
		principal.User.ID,
		scope.OrganizationSlug,
		strings.TrimSpace(r.PathValue("id")),
		strings.TrimSpace(r.PostForm.Get("name")),
		sqlite.SavedSearchVisibility(r.PostForm.Get("visibility")),
		discoverBool(r.PostForm.Get("favorite")),
	)
	if err != nil {
		writeWebInternal(w, r, "Clone failed")
		return
	}
	if cloned == nil {
		writeWebNotFound(w, r, "Saved query not found")
		return
	}
	http.Redirect(w, r, discoverSavedQueryDetailURL(*cloned), http.StatusSeeOther)
}

func (h *Handler) updateDiscoverQuery(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		writeWebUnavailable(w, r, "Saved queries not available")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}
	updated, err := h.searches.UpdateMetadata(
		r.Context(),
		principal.User.ID,
		scope.OrganizationSlug,
		strings.TrimSpace(r.PathValue("id")),
		r.PostForm.Get("name"),
		r.PostForm.Get("description"),
		sqlite.SavedSearchVisibility(r.PostForm.Get("visibility")),
		strings.Split(r.PostForm.Get("tags"), ","),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeWebNotFound(w, r, "Saved query not found")
			return
		}
		writeWebInternal(w, r, "Update failed")
		return
	}
	if err := h.searches.SetFavorite(r.Context(), principal.User.ID, scope.OrganizationSlug, updated.ID, discoverBool(r.PostForm.Get("favorite"))); err != nil {
		writeWebInternal(w, r, "Favorite update failed")
		return
	}
	http.Redirect(w, r, discoverSavedQueryDetailURL(*updated), http.StatusSeeOther)
}

func (h *Handler) deleteDiscoverQuery(w http.ResponseWriter, r *http.Request) {
	if h.searches == nil {
		writeWebUnavailable(w, r, "Saved queries not available")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		writeWebForbidden(w, r)
		return
	}
	if err := h.searches.Delete(r.Context(), principal.User.ID, strings.TrimSpace(r.PathValue("id"))); err != nil {
		writeWebInternal(w, r, "Delete failed")
		return
	}
	http.Redirect(w, r, "/discover/", http.StatusSeeOther)
}

func mergeDiscoverSavedState(values url.Values, state, saved discoverBuilderState) discoverBuilderState {
	if values.Get("dataset") == "" && values.Get("scope") == "" {
		state.Dataset = saved.Dataset
	}
	if values.Get("query") == "" {
		state.Query = saved.Query
	}
	if values.Get("filter") == "" {
		state.Filter = saved.Filter
	}
	if values.Get("environment") == "" {
		state.Environment = saved.Environment
	}
	if values.Get("visualization") == "" {
		state.Visualization = saved.Visualization
	}
	if values.Get("columns") == "" {
		state.Columns = saved.Columns
	}
	if values.Get("aggregate") == "" {
		state.Aggregate = saved.Aggregate
	}
	if values.Get("group_by") == "" {
		state.GroupBy = saved.GroupBy
	}
	if values.Get("order_by") == "" {
		state.OrderBy = saved.OrderBy
	}
	if values.Get("time_range") == "" {
		state.TimeRange = saved.TimeRange
	}
	if values.Get("rollup") == "" {
		state.Rollup = saved.Rollup
	}
	state.SaveName = saved.SaveName
	if values.Get("visibility") == "" {
		state.SaveVisibility = saved.SaveVisibility
	}
	return state
}

func discoverSavedQueries(saved []sqlite.SavedSearch) []discoverSavedQuery {
	items := make([]discoverSavedQuery, 0, len(saved))
	for _, item := range saved {
		items = append(items, discoverSavedQuery{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			Tags:        append([]string(nil), item.Tags...),
			Favorite:    item.Favorite,
			Dataset:     savedQueryDataset(item),
			Visibility:  string(item.Visibility),
			Query:       item.Query,
			Environment: item.Environment,
			URL:         discoverSavedQueryDetailURL(item),
			OpenURL:     discoverSavedQueryURL(savedQueryPath(savedQueryDataset(item)), item),
		})
	}
	return items
}

func (h *Handler) discoverSavedQuery(ctx context.Context, organizationSlug, id string) (*sqlite.SavedSearch, error) {
	if h.searches == nil || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil || principal.User == nil {
		return nil, nil
	}
	return h.searches.Get(ctx, principal.User.ID, organizationSlug, strings.TrimSpace(id))
}
