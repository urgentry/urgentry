package web

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type dashboardsPageData struct {
	Title        string
	Nav          string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Guide        analyticsGuide
	Dashboards   []sqlite.Dashboard
	Templates    []dashboardTemplateCard
	Error        string
}

type dashboardDetailPageData struct {
	Title                   string
	Nav                     string
	Environment             string   // selected environment ("" = all)
	Environments            []string // available environments for global nav
	Dashboard               *sqlite.Dashboard
	DashboardConfig         dashboardPresentationConfig
	DashboardFilters        []string
	DashboardRefreshLabel   string
	DashboardRefreshSeconds int
	DashboardAnnotations    string
	WidgetViews             []dashboardWidgetView
	SavedQueries            []discoverSavedQuery
	Error                   string
}

func (h *Handler) dashboardsPage(w http.ResponseWriter, r *http.Request) {
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboards, err := h.dashboards.ListDashboards(r.Context(), scope.OrganizationSlug, userID)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboards.")
		return
	}
	h.render(w, "dashboards.html", dashboardsPageData{
		Title:        "Dashboards",
		Nav:          "dashboards",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Guide:        dashboardsGuide(),
		Dashboards:   dashboards,
		Templates:    dashboardTemplateCards(),
	})
}

func (h *Handler) dashboardDetailPage(w http.ResponseWriter, r *http.Request) {
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboard, err := h.dashboards.GetDashboard(r.Context(), scope.OrganizationSlug, r.PathValue("id"), userID)
	if err != nil {
		if errors.Is(err, sqlite.ErrDashboardNotFound) {
			writeWebNotFound(w, r, "Dashboard not found")
			return
		}
		if errors.Is(err, sqlite.ErrDashboardForbidden) {
			writeWebForbidden(w, r)
			return
		}
		writeWebInternal(w, r, "Failed to load dashboard.")
		return
	}
	data := dashboardDetailPageData{
		Title:     dashboard.Title,
		Nav:       "dashboards",
		Dashboard: dashboard,
	}
	data.DashboardConfig = decodeDashboardConfig(dashboard.Config)
	data.DashboardAnnotations = dashboardAnnotationsText(data.DashboardConfig.Annotations)
	data.DashboardRefreshSeconds = data.DashboardConfig.RefreshSeconds
	data.DashboardFilters = dashboardFilterSummary(data.DashboardConfig)
	data.DashboardRefreshLabel = dashboardRefreshLabel(data.DashboardConfig.RefreshSeconds)
	if h.searches != nil {
		saved, err := h.searches.List(r.Context(), userID, scope.OrganizationSlug)
		if err == nil {
			data.SavedQueries = discoverSavedQueries(saved)
		}
	}
	for _, widget := range dashboard.Widgets {
		data.WidgetViews = append(data.WidgetViews, h.buildDashboardWidgetView(r.Context(), r, scope, dashboard, data.DashboardConfig, widget))
	}
	h.render(w, "dashboard-detail.html", data)
}

func (h *Handler) exportDashboardWidget(w http.ResponseWriter, r *http.Request) {
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	format := normalizedExportFormat(r.URL.Query().Get("format"))
	if format == "" {
		writeWebBadRequest(w, r, "Unsupported export format")
		return
	}
	_, view, _, err := h.loadDashboardWidgetView(r.Context(), r, scope, userID, r.PathValue("id"), r.PathValue("widget_id"))
	if err != nil {
		switch {
		case errors.Is(err, sqlite.ErrDashboardNotFound), errors.Is(err, errDashboardWidgetNotFound):
			writeWebNotFound(w, r, "Dashboard widget not found")
		case errors.Is(err, sqlite.ErrDashboardForbidden):
			writeWebForbidden(w, r)
		default:
			writeWebInternal(w, r, "Failed to load dashboard widget.")
		}
		return
	}
	if strings.TrimSpace(view.Error) != "" {
		status := view.ErrorStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeWebError(w, r, status, view.Error)
		return
	}
	writeAnalyticsExport(w, view.Widget.Title, format, view.Result)
}

func (h *Handler) createDashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if title == "" {
		writeWebBadRequest(w, r, "Dashboard name is required.")
		return
	}
	dashboard, err := h.dashboards.CreateDashboard(r.Context(), scope.OrganizationSlug, userID, sqlite.DashboardInput{
		Title:       title,
		Description: strings.TrimSpace(r.PostForm.Get("description")),
		Visibility:  sqlite.DashboardVisibility(strings.TrimSpace(r.PostForm.Get("visibility"))),
		Config:      encodeDashboardConfigFromForm(r.PostForm),
	})
	if err != nil {
		writeWebBadRequest(w, r, "Failed to create dashboard.")
		return
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/", http.StatusSeeOther)
}

func (h *Handler) createDashboardTemplate(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	item, found := lookupDashboardTemplate(r.PathValue("slug"))
	if !found {
		writeWebNotFound(w, r, "Starter dashboard not found")
		return
	}
	inputs, err := buildDashboardTemplateInputs(scope.OrganizationSlug, item)
	if err != nil {
		writeWebBadRequest(w, r, "Failed to build starter dashboard.")
		return
	}
	dashboard, err := h.dashboards.CreateDashboard(r.Context(), scope.OrganizationSlug, userID, sqlite.DashboardInput{
		Title:       item.Name,
		Description: item.Description,
		Visibility:  sqlite.DashboardVisibilityPrivate,
	})
	if err != nil {
		writeWebBadRequest(w, r, "Failed to create starter dashboard.")
		return
	}
	for _, input := range inputs {
		if _, err := h.dashboards.CreateWidget(r.Context(), scope.OrganizationSlug, dashboard.ID, userID, input); err != nil {
			_ = h.dashboards.DeleteDashboard(r.Context(), scope.OrganizationSlug, dashboard.ID, userID)
			writeWebBadRequest(w, r, "Failed to create starter dashboard.")
			return
		}
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/", http.StatusSeeOther)
}

func (h *Handler) updateDashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	_, err := h.dashboards.UpdateDashboard(r.Context(), scope.OrganizationSlug, r.PathValue("id"), userID, sqlite.DashboardInput{
		Title:       strings.TrimSpace(r.PostForm.Get("title")),
		Description: strings.TrimSpace(r.PostForm.Get("description")),
		Visibility:  sqlite.DashboardVisibility(strings.TrimSpace(r.PostForm.Get("visibility"))),
		Config:      encodeDashboardConfigFromForm(r.PostForm),
	})
	if err != nil {
		writeWebBadRequest(w, r, "Failed to update dashboard.")
		return
	}
	http.Redirect(w, r, "/dashboards/"+r.PathValue("id")+"/", http.StatusSeeOther)
}

func (h *Handler) duplicateDashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	dashboard, err := h.dashboards.DuplicateDashboard(r.Context(), scope.OrganizationSlug, r.PathValue("id"), userID, strings.TrimSpace(r.PostForm.Get("title")))
	if err != nil {
		writeWebBadRequest(w, r, "Failed to duplicate dashboard.")
		return
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/", http.StatusSeeOther)
}

func (h *Handler) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := h.dashboards.DeleteDashboard(r.Context(), scope.OrganizationSlug, r.PathValue("id"), userID); err != nil {
		writeWebBadRequest(w, r, "Failed to delete dashboard.")
		return
	}
	http.Redirect(w, r, "/dashboards/", http.StatusSeeOther)
}

func (h *Handler) createDashboardWidget(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	dashboard, err := h.dashboards.GetDashboard(r.Context(), scope.OrganizationSlug, r.PathValue("id"), userID)
	if err != nil {
		writeWebBadRequest(w, r, "Failed to load dashboard.")
		return
	}
	input, err := h.dashboardWidgetInput(r.Context(), scope.OrganizationSlug, userID, dashboard, r.PostForm)
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	if _, err := h.dashboards.CreateWidget(r.Context(), scope.OrganizationSlug, dashboard.ID, userID, input); err != nil {
		writeWebBadRequest(w, r, "Failed to create widget.")
		return
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/", http.StatusSeeOther)
}

func (h *Handler) deleteDashboardWidget(w http.ResponseWriter, r *http.Request) {
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryWrite)
	if !ok {
		return
	}
	if err := h.dashboards.DeleteWidget(r.Context(), scope.OrganizationSlug, r.PathValue("id"), r.PathValue("widget_id"), userID); err != nil {
		writeWebBadRequest(w, r, "Failed to delete widget.")
		return
	}
	http.Redirect(w, r, "/dashboards/"+r.PathValue("id")+"/", http.StatusSeeOther)
}

func (h *Handler) dashboardScope(w http.ResponseWriter, r *http.Request, scopeName string) (string, pageScope, bool) {
	if h.db == nil || h.dashboards == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return "", pageScope{}, false
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeAnyMembership(r, scopeName); err != nil {
			writeWebForbidden(w, r)
			return "", pageScope{}, false
		}
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil || principal.User.ID == "" {
		writeWebForbidden(w, r)
		return "", pageScope{}, false
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return "", pageScope{}, false
	}
	if scope.OrganizationSlug == "" {
		writeWebBadRequest(w, r, "No organization scope available.")
		return "", pageScope{}, false
	}
	return principal.User.ID, scope, true
}

func (h *Handler) requireDashboardCSRF(w http.ResponseWriter, r *http.Request) bool {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return false
	}
	return true
}

func (h *Handler) dashboardWidgetInput(ctx context.Context, orgSlug, userID string, dashboard *sqlite.Dashboard, form url.Values) (sqlite.DashboardWidgetInput, error) {
	kind, err := normalizeDashboardWidgetKind(form.Get("kind"))
	if err != nil {
		return sqlite.DashboardWidgetInput{}, err
	}
	input := sqlite.DashboardWidgetInput{
		Title:       strings.TrimSpace(form.Get("title")),
		Description: strings.TrimSpace(form.Get("description")),
		Kind:        kind,
		Position:    len(dashboard.Widgets) + 1,
		Width:       max(1, parsePositiveInt(form.Get("width"), 1)),
		Height:      max(1, parsePositiveInt(form.Get("height"), 1)),
		Config:      encodeDashboardWidgetConfigFromForm(form),
	}
	if savedID := strings.TrimSpace(form.Get("saved_search_id")); savedID != "" {
		saved, err := h.searches.Get(ctx, userID, orgSlug, savedID)
		if err != nil {
			return sqlite.DashboardWidgetInput{}, err
		}
		if saved == nil {
			return sqlite.DashboardWidgetInput{}, errors.New("saved search not found")
		}
		input.SavedSearchID = saved.ID
		input.QueryDoc = saved.QueryDoc
		if strings.TrimSpace(form.Get("kind")) == "" {
			input.Kind = inferDashboardWidgetKind(saved.QueryDoc)
		}
		return input, nil
	}
	state := discoverStateFromValues(form, string(discover.DatasetIssues))
	if strings.TrimSpace(form.Get("visualization")) == "" && strings.TrimSpace(form.Get("kind")) != "" {
		state.Visualization = strings.ToLower(strings.TrimSpace(form.Get("kind")))
	}
	queryDoc, err := buildDiscoverQuery(orgSlug, state, 50)
	if err != nil {
		return sqlite.DashboardWidgetInput{}, err
	}
	if strings.TrimSpace(form.Get("kind")) == "" {
		input.Kind = inferDashboardWidgetKind(queryDoc)
	}
	input.QueryDoc = queryDoc
	return input, nil
}

func inferDashboardWidgetKind(query discover.Query) sqlite.DashboardWidgetKind {
	if query.Rollup != nil {
		return sqlite.DashboardWidgetKindSeries
	}
	if hasDiscoverAggregate(query) && len(query.GroupBy) == 0 {
		return sqlite.DashboardWidgetKindStat
	}
	return sqlite.DashboardWidgetKindTable
}

func normalizeDashboardWidgetKind(raw string) (sqlite.DashboardWidgetKind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "table":
		return sqlite.DashboardWidgetKindTable, nil
	case "stat":
		return sqlite.DashboardWidgetKindStat, nil
	case "series":
		return sqlite.DashboardWidgetKindSeries, nil
	case "custom_metric":
		return sqlite.DashboardWidgetKindCustomMetric, nil
	default:
		return "", errors.New("unsupported widget kind")
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (h *Handler) guardDashboardWidgetQuery(ctx context.Context, r *http.Request, scope pageScope, query discover.Query) error {
	if h.authz == nil {
		return nil
	}
	decision, err := h.queryGuard.CheckAndRecord(ctx, sqlite.QueryGuardRequest{
		Principal:      auth.PrincipalFromContext(ctx),
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
		Estimate: sqlite.QueryEstimate{
			Workload: workloadForDataset(string(query.Dataset)),
			Limit:    query.Limit,
			Query:    dashboardQueryEstimateText(query),
			Scope:    string(query.Dataset),
		},
	})
	if err != nil {
		return errors.New("failed to apply query guardrails")
	}
	if decision.Allowed {
		return nil
	}
	if decision.RetryAfter > 0 {
		return errors.New(decision.Reason + " Retry after " + strconv.Itoa(int(math.Ceil(decision.RetryAfter.Seconds()))) + "s.")
	}
	return errors.New(decision.Reason)
}

func dashboardQueryEstimateText(query discover.Query) string {
	parts := []string{string(query.Dataset)}
	for _, item := range query.Select {
		if strings.TrimSpace(item.Alias) != "" {
			parts = append(parts, item.Alias)
			continue
		}
		if strings.TrimSpace(item.Expr.Call) != "" {
			parts = append(parts, item.Expr.Call)
			continue
		}
		if strings.TrimSpace(item.Expr.Field) != "" {
			parts = append(parts, item.Expr.Field)
		}
	}
	for _, expr := range query.GroupBy {
		if strings.TrimSpace(expr.Field) != "" {
			parts = append(parts, expr.Field)
		}
	}
	if query.TimeRange != nil && strings.TrimSpace(query.TimeRange.Value) != "" {
		parts = append(parts, query.TimeRange.Value)
	}
	if query.Rollup != nil && strings.TrimSpace(query.Rollup.Interval) != "" {
		parts = append(parts, query.Rollup.Interval)
	}
	return strings.Join(parts, " ")
}
