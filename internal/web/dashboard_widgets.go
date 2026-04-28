package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"urgentry/internal/analyticsreport"
	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

var errDashboardWidgetNotFound = errors.New("dashboard widget not found")

type dashboardWidgetContractView struct {
	SourceLabel string
	SourceURL   string
	Dataset     string
	Kind        string
	Filters     []string
}

type dashboardWidgetView struct {
	Widget         sqlite.DashboardWidget
	Contract       dashboardWidgetContractView
	Query          discover.Query
	Result         discoverResultView
	Explain        discoverExplainView
	DetailURL      string
	ExportCSVURL   string
	ExportJSONURL  string
	SnapshotAction string
	ThresholdClass string
	ThresholdText  string
	Error          string
	ErrorStatus    int
}

type dashboardWidgetDetailPageData struct {
	Title              string
	Nav                string
	Environment        string   // selected environment ("" = all)
	Environments       []string // available environments for global nav
	Dashboard          *sqlite.Dashboard
	DashboardURL       string
	WidgetView         dashboardWidgetView
	ReportCreateAction string
	ReportSchedules    []analyticsReportScheduleView
	DashboardConfig    dashboardPresentationConfig
}

func (h *Handler) dashboardWidgetDetailPage(w http.ResponseWriter, r *http.Request) {
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboard, view, cfg, err := h.loadDashboardWidgetView(r.Context(), r, scope, userID, r.PathValue("id"), r.PathValue("widget_id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrDashboardNotFound) || errors.Is(err, errDashboardWidgetNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, sqlite.ErrDashboardForbidden) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, "Failed to load dashboard widget.", http.StatusInternalServerError)
		return
	}
	data := dashboardWidgetDetailPageData{
		Title:              view.Widget.Title,
		Nav:                "dashboards",
		Environment:        readSelectedEnvironment(r),
		Environments:       h.loadEnvironments(r.Context()),
		Dashboard:          dashboard,
		DashboardURL:       "/dashboards/" + dashboard.ID + "/",
		WidgetView:         view,
		ReportCreateAction: "/dashboards/" + dashboard.ID + "/widgets/" + view.Widget.ID + "/reports",
		DashboardConfig:    cfg,
	}
	if h.reportSchedules != nil {
		schedules, err := h.reportSchedules.ListBySource(r.Context(), scope.OrganizationSlug, analyticsreport.SourceTypeDashboardWidget, view.Widget.ID, userID)
		if err != nil {
			http.Error(w, "Failed to load report schedules.", http.StatusInternalServerError)
			return
		}
		data.ReportSchedules = analyticsReportScheduleViews(schedules)
	}
	h.render(w, "dashboard-widget-detail.html", data)
}

func (h *Handler) loadDashboardWidgetView(ctx context.Context, r *http.Request, scope pageScope, userID, dashboardID, widgetID string) (*sqlite.Dashboard, dashboardWidgetView, dashboardPresentationConfig, error) {
	dashboard, err := h.dashboards.GetDashboard(ctx, scope.OrganizationSlug, dashboardID, userID)
	if err != nil {
		return nil, dashboardWidgetView{}, dashboardPresentationConfig{}, err
	}
	widget := findDashboardWidget(dashboard, widgetID)
	if widget == nil {
		return nil, dashboardWidgetView{}, dashboardPresentationConfig{}, errDashboardWidgetNotFound
	}
	cfg := decodeDashboardConfig(dashboard.Config)
	view := h.buildDashboardWidgetView(ctx, r, scope, dashboard, cfg, *widget)
	return dashboard, view, cfg, nil
}

func (h *Handler) buildDashboardWidgetView(ctx context.Context, r *http.Request, scope pageScope, dashboard *sqlite.Dashboard, cfg dashboardPresentationConfig, widget sqlite.DashboardWidget) dashboardWidgetView {
	view := dashboardWidgetView{
		Widget: widget,
		Contract: dashboardWidgetContractView{
			SourceLabel: "Dashboard widget query",
			Dataset:     string(widget.QueryDoc.Dataset),
			Kind:        string(widget.Kind),
			Filters:     dashboardFilterSummary(cfg),
		},
		DetailURL:      "/dashboards/" + dashboard.ID + "/widgets/" + widget.ID + "/",
		ExportCSVURL:   "/dashboards/" + dashboard.ID + "/widgets/" + widget.ID + "/export?format=csv",
		ExportJSONURL:  "/dashboards/" + dashboard.ID + "/widgets/" + widget.ID + "/export?format=json",
		SnapshotAction: "/dashboards/" + dashboard.ID + "/widgets/" + widget.ID + "/snapshot",
	}
	if strings.TrimSpace(widget.SavedSearchID) != "" {
		view.Contract.SourceLabel = "Saved query"
		view.Contract.SourceURL = "/discover/queries/" + widget.SavedSearchID + "/"
	}
	query := dashboardQueryWithFilters(widget.QueryDoc, cfg)
	view.Query = query
	view.Explain = buildDiscoverExplain(query)
	if err := h.guardDashboardWidgetQuery(ctx, r, scope, query); err != nil {
		view.Error = err.Error()
		view.ErrorStatus = http.StatusTooManyRequests
		return view
	}
	result, err := executeDiscoverResult(ctx, h.queries, query, string(widget.Kind))
	if err != nil {
		view.Error = "Failed to run widget query."
		view.ErrorStatus = http.StatusBadRequest
		return view
	}
	view.Result = result
	view.ThresholdClass, view.ThresholdText = widgetThresholdView(widget, result)
	return view
}

func findDashboardWidget(dashboard *sqlite.Dashboard, widgetID string) *sqlite.DashboardWidget {
	for i := range dashboard.Widgets {
		if dashboard.Widgets[i].ID == widgetID {
			return &dashboard.Widgets[i]
		}
	}
	return nil
}
