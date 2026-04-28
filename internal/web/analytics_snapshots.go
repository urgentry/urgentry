package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"urgentry/internal/analyticssnapshot"
	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/requestmeta"
	"urgentry/internal/sqlite"
)

type analyticsSnapshotPageData struct {
	Title        string
	Nav          string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Snapshot     *sqlite.AnalyticsSnapshot
	ShareURL     string
	ExportCSV    string
	ExportJSON   string
	Result       discoverResultView
}

func (h *Handler) createDiscoverQuerySnapshot(w http.ResponseWriter, r *http.Request) {
	if h.snapshots == nil || h.searches == nil {
		http.Error(w, "analytics snapshots unavailable", http.StatusServiceUnavailable)
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		http.Error(w, "Failed to resolve default organization scope.", http.StatusInternalServerError)
		return
	}
	saved, err := h.discoverSavedQuery(r.Context(), scope.OrganizationSlug, r.PathValue("id"))
	if err != nil {
		http.Error(w, "Failed to load saved query.", http.StatusInternalServerError)
		return
	}
	if saved == nil {
		http.NotFound(w, r)
		return
	}
	result, err := executeDiscoverResult(r.Context(), h.queries, saved.QueryDoc, discoverStateFromSaved(*saved, savedQueryDataset(*saved), r.URL.Path).Visualization)
	if err != nil {
		http.Error(w, "Failed to run query.", http.StatusBadRequest)
		return
	}
	visualization := discoverStateFromSaved(*saved, savedQueryDataset(*saved), r.URL.Path).Visualization
	snapshot, err := h.snapshots.Create(r.Context(), scope.OrganizationSlug, principal.User.ID, "saved_query", saved.ID, saved.Name, snapshotBodyFromResult(result, saved.QueryDoc, "Saved query", string(saved.QueryDoc.Dataset), visualization, nil))
	if err != nil {
		http.Error(w, "Failed to create snapshot.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/analytics/snapshots/"+snapshot.ShareToken+"/", http.StatusSeeOther)
}

func (h *Handler) createDashboardWidgetSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.snapshots == nil {
		http.Error(w, "analytics snapshots unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboard, view, _, err := h.loadDashboardWidgetView(r.Context(), r, scope, userID, r.PathValue("id"), r.PathValue("widget_id"))
	if err != nil {
		switch {
		case errors.Is(err, sqlite.ErrDashboardNotFound), errors.Is(err, errDashboardWidgetNotFound):
			http.NotFound(w, r)
		case errors.Is(err, sqlite.ErrDashboardForbidden):
			http.Error(w, "Forbidden", http.StatusForbidden)
		default:
			http.Error(w, "Failed to load dashboard widget.", http.StatusInternalServerError)
		}
		return
	}
	if strings.TrimSpace(view.Error) != "" {
		status := view.ErrorStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		http.Error(w, view.Error, status)
		return
	}
	snapshot, err := h.snapshots.Create(r.Context(), scope.OrganizationSlug, userID, "dashboard_widget", view.Widget.ID, dashboard.Title+" - "+view.Widget.Title, snapshotBodyFromResult(view.Result, view.Query, view.Contract.SourceLabel, view.Contract.Dataset, view.Contract.Kind, view.Contract.Filters))
	if err != nil {
		http.Error(w, "Failed to create snapshot.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/analytics/snapshots/"+snapshot.ShareToken+"/", http.StatusSeeOther)
}

func (h *Handler) analyticsSnapshotPage(w http.ResponseWriter, r *http.Request) {
	if h.snapshots == nil {
		http.NotFound(w, r)
		return
	}
	snapshot, err := h.snapshots.GetByShareToken(r.Context(), strings.TrimSpace(r.PathValue("token")))
	if err != nil {
		if errors.Is(err, sqlite.ErrAnalyticsSnapshotNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Failed to load snapshot.", http.StatusInternalServerError)
		return
	}
	result := resultViewFromSnapshotBody(snapshot.Body)
	if format := normalizedExportFormat(r.URL.Query().Get("format")); format != "" {
		writeAnalyticsExport(w, snapshot.Title, format, result)
		return
	}
	h.render(w, "analytics-snapshot.html", analyticsSnapshotPageData{
		Title:        snapshot.Title,
		Nav:          "dashboards",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
		Snapshot:     snapshot,
		ShareURL:     absoluteSnapshotURL(r, snapshot.ShareToken),
		ExportCSV:    exportURL(r.URL.RequestURI(), "csv"),
		ExportJSON:   exportURL(r.URL.RequestURI(), "json"),
		Result:       result,
	})
}

func snapshotBodyFromResult(result discoverResultView, query discover.Query, sourceLabel, dataset, visualization string, filters []string) sqlite.SnapshotBody {
	body := analyticssnapshot.Result{
		Type:      result.Type,
		Columns:   append([]string(nil), result.Columns...),
		StatLabel: result.StatLabel,
		StatValue: result.StatValue,
	}
	for _, row := range result.Rows {
		flat := make([]string, 0, len(row))
		for _, cell := range row {
			flat = append(flat, cell.Text)
		}
		body.Rows = append(body.Rows, flat)
	}
	return analyticssnapshot.BodyFromResult(body, query, sourceLabel, dataset, visualization, filters)
}

func resultViewFromSnapshotBody(body sqlite.SnapshotBody) discoverResultView {
	view := discoverResultView{
		Type:      body.ViewType,
		Columns:   append([]string(nil), body.Columns...),
		StatLabel: body.StatLabel,
		StatValue: body.StatValue,
	}
	for _, row := range body.Rows {
		cells := make([]discoverCell, 0, len(row))
		for _, cell := range row {
			cells = append(cells, discoverCell{Text: cell})
		}
		view.Rows = append(view.Rows, cells)
	}
	return view
}

func absoluteSnapshotURL(r *http.Request, token string) string {
	if r == nil {
		return "/analytics/snapshots/" + strings.TrimSpace(token) + "/"
	}
	item := &url.URL{
		Scheme: requestmeta.Scheme(r),
		Host:   requestmeta.Host(r),
		Path:   "/analytics/snapshots/" + strings.TrimSpace(token) + "/",
	}
	if item.Host == "" {
		item.Host = "localhost:8080"
	}
	return item.String()
}
