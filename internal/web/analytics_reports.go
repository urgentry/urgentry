package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/analyticsreport"
	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
)

type analyticsReportScheduleView struct {
	ID              string
	Recipient       string
	Cadence         string
	NextRunAt       string
	LastRunAt       string
	LastAttemptAt   string
	LastError       string
	LastSnapshotURL string
}

func analyticsReportScheduleViews(items []sqlite.AnalyticsReportSchedule) []analyticsReportScheduleView {
	out := make([]analyticsReportScheduleView, 0, len(items))
	for _, item := range items {
		view := analyticsReportScheduleView{
			ID:        item.ID,
			Recipient: item.Recipient,
			Cadence:   titleCase(string(item.Cadence)),
			NextRunAt: item.NextRunAt.UTC().Format(time.RFC3339),
			LastError: item.LastError,
		}
		if item.LastRunAt != nil {
			view.LastRunAt = item.LastRunAt.UTC().Format(time.RFC3339)
		}
		if item.LastAttemptAt != nil {
			view.LastAttemptAt = item.LastAttemptAt.UTC().Format(time.RFC3339)
		}
		if item.LastSnapshotToken != "" {
			view.LastSnapshotURL = "/analytics/snapshots/" + item.LastSnapshotToken + "/"
		}
		out = append(out, view)
	}
	return out
}

func (h *Handler) createDiscoverQueryReport(w http.ResponseWriter, r *http.Request) {
	if h.reportSchedules == nil || h.searches == nil {
		http.Error(w, "analytics report schedules unavailable", http.StatusServiceUnavailable)
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
	if _, err := h.reportSchedules.Create(
		r.Context(),
		scope.OrganizationSlug,
		analyticsreport.SourceTypeSavedQuery,
		saved.ID,
		principal.User.ID,
		r.FormValue("recipient"),
		sqlite.AnalyticsReportCadence(r.FormValue("cadence")),
	); err != nil {
		http.Error(w, "Failed to create report schedule.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/discover/queries/"+saved.ID+"/", http.StatusSeeOther)
}

func (h *Handler) deleteDiscoverQueryReport(w http.ResponseWriter, r *http.Request) {
	if h.reportSchedules == nil {
		http.Error(w, "analytics report schedules unavailable", http.StatusServiceUnavailable)
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
	if err := h.reportSchedules.Delete(r.Context(), scope.OrganizationSlug, principal.User.ID, r.PathValue("report_id")); err != nil {
		http.Error(w, "Failed to delete report schedule.", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/discover/queries/"+r.PathValue("id")+"/", http.StatusSeeOther)
}

func (h *Handler) createDashboardWidgetReport(w http.ResponseWriter, r *http.Request) {
	if h.reportSchedules == nil {
		http.Error(w, "analytics report schedules unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboard, widget, err := h.dashboards.GetDashboardWidget(r.Context(), scope.OrganizationSlug, r.PathValue("widget_id"), userID)
	if err != nil {
		h.writeDashboardWidgetError(w, r, err)
		return
	}
	if _, err := h.reportSchedules.Create(
		r.Context(),
		scope.OrganizationSlug,
		analyticsreport.SourceTypeDashboardWidget,
		widget.ID,
		userID,
		r.FormValue("recipient"),
		sqlite.AnalyticsReportCadence(r.FormValue("cadence")),
	); err != nil {
		http.Error(w, "Failed to create report schedule.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/widgets/"+widget.ID+"/", http.StatusSeeOther)
}

func (h *Handler) deleteDashboardWidgetReport(w http.ResponseWriter, r *http.Request) {
	if h.reportSchedules == nil {
		http.Error(w, "analytics report schedules unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.requireDashboardCSRF(w, r) {
		return
	}
	userID, scope, ok := h.dashboardScope(w, r, auth.ScopeOrgQueryRead)
	if !ok {
		return
	}
	dashboard, widget, err := h.dashboards.GetDashboardWidget(r.Context(), scope.OrganizationSlug, r.PathValue("widget_id"), userID)
	if err != nil {
		h.writeDashboardWidgetError(w, r, err)
		return
	}
	if err := h.reportSchedules.Delete(r.Context(), scope.OrganizationSlug, userID, r.PathValue("report_id")); err != nil {
		http.Error(w, "Failed to delete report schedule.", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/dashboards/"+dashboard.ID+"/widgets/"+widget.ID+"/", http.StatusSeeOther)
}

func (h *Handler) writeDashboardWidgetError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sqlite.ErrDashboardForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Is(err, sqlite.ErrDashboardNotFound):
		http.NotFound(w, r)
	default:
		http.Error(w, "Failed to load dashboard widget.", http.StatusInternalServerError)
	}
}

func titleCase(raw string) string {
	if raw == "" {
		return ""
	}
	return strings.ToUpper(raw[:1]) + raw[1:]
}
