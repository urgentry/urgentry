package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
)

type monitorsData struct {
	Title          string
	Nav            string
	Environment    string   // selected environment ("" = all)
	Environments   []string // available environments for global nav
	DefaultProject string
	Monitors       []monitorRow
}

type monitorRow struct {
	ID            string
	ProjectID     string
	Slug          string
	Status        string
	Environment   string
	Schedule      string
	ScheduleType  string
	ScheduleValue int
	ScheduleUnit  string
	ScheduleCron  string
	Threshold     string
	CheckInMargin int
	MaxRuntime    int
	Timezone      string
	LastStatus    string
	LastCheckInAt string
	NextCheckInAt string
	CreatedAt     string
	UpdatedAt     string
}

type monitorDetailData struct {
	Title        string
	Nav          string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	Monitor      monitorRow
	Timeline     []monitorTimelineRow
	CheckIns     []monitorCheckInRow
}

type monitorCheckInRow struct {
	CheckInID    string
	Status       string
	StatusTone   string
	Environment  string
	Release      string
	Duration     string
	ScheduledFor string
	CreatedAt    string
}

type monitorTimelineRow struct {
	Tone    string
	Title   string
	Detail  string
	TimeAgo string
}

func (h *Handler) monitorsPage(w http.ResponseWriter, r *http.Request) {
	if h.monitors == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	ctx := r.Context()
	items, err := h.monitors.ListAllMonitors(ctx, 200)
	if err != nil {
		writeWebInternal(w, r, "Failed to load monitors.")
		return
	}
	defaultProject := ""
	if h.webStore != nil {
		if id, err := h.webStore.DefaultProjectID(ctx); err == nil {
			defaultProject = id
		}
	}

	rows := make([]monitorRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, monitorRow{
			ID:            item.ID,
			ProjectID:     item.ProjectID,
			Slug:          item.Slug,
			Status:        item.Status,
			Environment:   item.Environment,
			Schedule:      monitorScheduleLabel(item.Config.Schedule),
			ScheduleType:  item.Config.Schedule.Type,
			ScheduleValue: item.Config.Schedule.Value,
			ScheduleUnit:  item.Config.Schedule.Unit,
			ScheduleCron:  item.Config.Schedule.Crontab,
			Threshold:     monitorThresholdLabel(item.Config),
			CheckInMargin: item.Config.CheckInMargin,
			MaxRuntime:    item.Config.MaxRuntime,
			Timezone:      item.Config.Timezone,
			LastStatus:    item.LastStatus,
			LastCheckInAt: formatOptionalMonitorTime(item.LastCheckInAt),
			NextCheckInAt: formatNextMonitorTime(item.NextCheckInAt),
			CreatedAt:     timeAgo(item.DateCreated),
			UpdatedAt:     timeAgo(item.UpdatedAt),
		})
	}

	h.render(w, "monitors.html", monitorsData{
		Title:          "Monitors",
		Nav:            "monitors",
		Environment:    readSelectedEnvironment(r),
		Environments:   h.loadEnvironments(ctx),
		DefaultProject: defaultProject,
		Monitors:       rows,
	})
}

func (h *Handler) monitorDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.monitors == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	if projectID == "" || slug == "" {
		writeWebNotFound(w, r, "Monitor not found")
		return
	}
	monitor, err := h.monitors.GetMonitor(r.Context(), projectID, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load monitor.")
		return
	}
	if monitor == nil {
		writeWebNotFound(w, r, "Monitor not found")
		return
	}
	checkIns, err := h.monitors.ListCheckIns(r.Context(), monitor.ProjectID, monitor.Slug, 50)
	if err != nil {
		writeWebInternal(w, r, "Failed to load monitor check-ins.")
		return
	}
	data := monitorDetailData{
		Title: monitor.Slug,
		Nav:   "monitors",
		Monitor: monitorRow{
			ID:            monitor.ID,
			ProjectID:     monitor.ProjectID,
			Slug:          monitor.Slug,
			Status:        monitor.Status,
			Environment:   monitor.Environment,
			Schedule:      monitorScheduleLabel(monitor.Config.Schedule),
			ScheduleType:  monitor.Config.Schedule.Type,
			ScheduleValue: monitor.Config.Schedule.Value,
			ScheduleUnit:  monitor.Config.Schedule.Unit,
			ScheduleCron:  monitor.Config.Schedule.Crontab,
			Threshold:     monitorThresholdLabel(monitor.Config),
			CheckInMargin: monitor.Config.CheckInMargin,
			MaxRuntime:    monitor.Config.MaxRuntime,
			Timezone:      monitor.Config.Timezone,
			LastStatus:    monitor.LastStatus,
			LastCheckInAt: formatOptionalMonitorTime(monitor.LastCheckInAt),
			NextCheckInAt: formatNextMonitorTime(monitor.NextCheckInAt),
			CreatedAt:     timeAgo(monitor.DateCreated),
			UpdatedAt:     timeAgo(monitor.UpdatedAt),
		},
		Timeline: buildMonitorTimeline(*monitor, checkIns),
		CheckIns: make([]monitorCheckInRow, 0, len(checkIns)),
	}
	for _, item := range checkIns {
		data.CheckIns = append(data.CheckIns, monitorCheckInRow{
			CheckInID:    item.CheckInID,
			Status:       item.Status,
			StatusTone:   monitorCheckInTone(item.Status),
			Environment:  item.Environment,
			Release:      item.Release,
			Duration:     formatMonitorDuration(item.Duration),
			ScheduledFor: formatMonitorCheckInTime(item.ScheduledFor),
			CreatedAt:    timeAgo(item.DateCreated),
		})
	}
	h.render(w, "monitor-detail.html", data)
}

func (h *Handler) createMonitor(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if h.authz != nil {
		projectID := strings.TrimSpace(r.FormValue("project_id"))
		if projectID == "" {
			writeWebBadRequest(w, r, "Project ID is required")
			return
		}
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	monitor, err := monitorFromForm(r, "")
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	if monitor.ProjectID == "" {
		writeWebBadRequest(w, r, "Project ID is required")
		return
	}
	if monitor.Slug == "" {
		writeWebBadRequest(w, r, "Slug is required")
		return
	}
	item, err := h.monitors.UpsertMonitor(r.Context(), monitor)
	if err != nil {
		writeWebInternal(w, r, "Failed to create monitor")
		return
	}
	_ = item
	redirectAfterMonitorMutation(w, r, "/monitors/")
}

func (h *Handler) updateMonitor(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	if projectID == "" || slug == "" {
		writeWebBadRequest(w, r, "Project ID and slug are required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	monitor, err := monitorFromForm(r, slug)
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	monitor.ProjectID = projectID
	monitor.Slug = slug
	if strings.TrimSpace(r.FormValue("status")) != "" {
		monitor.Status = strings.TrimSpace(r.FormValue("status"))
	}
	if _, err := h.monitors.UpsertMonitor(r.Context(), monitor); err != nil {
		writeWebInternal(w, r, "Failed to update monitor")
		return
	}
	redirectAfterMonitorMutation(w, r, "/monitors/")
}

func (h *Handler) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	if projectID == "" || slug == "" {
		writeWebBadRequest(w, r, "Project ID and slug are required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := h.monitors.DeleteMonitor(r.Context(), projectID, slug); err != nil {
		writeWebInternal(w, r, "Failed to delete monitor")
		return
	}
	redirectAfterMonitorMutation(w, r, "/monitors/")
}

func monitorFromForm(r *http.Request, fallbackSlug string) (*sqlite.Monitor, error) {
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	if slug == "" {
		slug = strings.TrimSpace(fallbackSlug)
	}
	status := strings.TrimSpace(r.FormValue("status"))
	environment := strings.TrimSpace(r.FormValue("environment"))
	scheduleType := strings.TrimSpace(r.FormValue("schedule_type"))
	value, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("schedule_value")))
	unit := strings.TrimSpace(r.FormValue("schedule_unit"))
	crontab := strings.TrimSpace(r.FormValue("schedule_crontab"))
	checkinMargin, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("checkin_margin")))
	maxRuntime, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("max_runtime")))
	timezone := strings.TrimSpace(r.FormValue("timezone"))
	if timezone == "" {
		timezone = "UTC"
	}
	return &sqlite.Monitor{
		ProjectID:   projectID,
		Slug:        slug,
		Status:      status,
		Environment: environment,
		Config: sqlite.MonitorConfig{
			Schedule: sqlite.MonitorSchedule{
				Type:    scheduleType,
				Value:   value,
				Unit:    unit,
				Crontab: crontab,
			},
			CheckInMargin: checkinMargin,
			MaxRuntime:    maxRuntime,
			Timezone:      timezone,
		},
	}, nil
}

func redirectAfterMonitorMutation(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func monitorScheduleLabel(schedule sqlite.MonitorSchedule) string {
	switch schedule.Type {
	case "interval":
		unit := strings.TrimSpace(schedule.Unit)
		if unit == "" {
			unit = "minute"
		}
		return fmt.Sprintf("every %d %s(s)", schedule.Value, unit)
	case "crontab":
		if schedule.Crontab == "" {
			return "crontab"
		}
		return "cron " + schedule.Crontab
	default:
		return "not configured"
	}
}

func monitorThresholdLabel(cfg sqlite.MonitorConfig) string {
	if cfg.CheckInMargin <= 0 && cfg.MaxRuntime <= 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	if cfg.CheckInMargin > 0 {
		parts = append(parts, fmt.Sprintf("margin %dm", cfg.CheckInMargin))
	}
	if cfg.MaxRuntime > 0 {
		parts = append(parts, fmt.Sprintf("runtime %ds", cfg.MaxRuntime))
	}
	return strings.Join(parts, ", ")
}

func formatOptionalMonitorTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return timeAgo(value)
}

func formatNextMonitorTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return monitorDueDescription(value)
}

func formatMonitorCheckInTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Format("2006-01-02 15:04 MST")
}

func buildMonitorTimeline(monitor sqlite.Monitor, checkIns []sqlite.MonitorCheckIn) []monitorTimelineRow {
	timeline := []monitorTimelineRow{
		{
			Tone:    "info",
			Title:   "Monitor created",
			Detail:  monitorScheduleLabel(monitor.Config.Schedule),
			TimeAgo: timeAgo(monitor.DateCreated),
		},
	}

	if len(checkIns) > 0 {
		latest := checkIns[0]
		timeline = append(timeline, monitorTimelineRow{
			Tone:    monitorCheckInTone(latest.Status),
			Title:   monitorCheckInHeadline(latest.Status),
			Detail:  monitorCheckInDetail(latest),
			TimeAgo: timeAgo(latest.DateCreated),
		})
	}

	if next := monitor.NextCheckInAt; !next.IsZero() {
		title := "Next check-in due"
		tone := "info"
		if time.Until(next) < 0 {
			title = "Next check-in overdue"
			tone = "warning"
		}
		timeline = append(timeline, monitorTimelineRow{
			Tone:    tone,
			Title:   title,
			Detail:  "scheduled " + next.Format("2006-01-02 15:04 MST"),
			TimeAgo: monitorDueDescription(next),
		})
	}

	for i := 0; i < len(checkIns) && i < 5; i++ {
		item := checkIns[i]
		timeline = append(timeline, monitorTimelineRow{
			Tone:    monitorCheckInTone(item.Status),
			Title:   monitorCheckInHeadline(item.Status),
			Detail:  monitorCheckInDetail(item),
			TimeAgo: timeAgo(item.DateCreated),
		})
	}
	return timeline
}

func monitorCheckInHeadline(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "ok":
		return "Check-in received"
	case "missed":
		return "Run missed"
	case "failed", "error":
		return "Check-in failed"
	default:
		return "Check-in update"
	}
}

func monitorCheckInDetail(item sqlite.MonitorCheckIn) string {
	parts := make([]string, 0, 4)
	if item.Release != "" {
		parts = append(parts, "release "+item.Release)
	}
	if item.Environment != "" {
		parts = append(parts, item.Environment)
	}
	if item.Duration > 0 {
		parts = append(parts, "duration "+formatMonitorDuration(item.Duration))
	}
	if !item.ScheduledFor.IsZero() {
		parts = append(parts, "scheduled "+formatMonitorCheckInTime(item.ScheduledFor))
	}
	return strings.Join(parts, " · ")
}

func monitorDueDescription(next time.Time) string {
	delta := time.Until(next)
	if delta < 0 {
		return fmt.Sprintf("overdue by %s", formatCompactDuration(-delta))
	}
	return fmt.Sprintf("due in %s", formatCompactDuration(delta))
}

func formatCompactDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func formatMonitorDuration(duration float64) string {
	if duration <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f ms", duration)
}

func monitorCheckInTone(status string) string {
	switch strings.TrimSpace(status) {
	case "ok":
		return "success"
	case "missed", "error", "failed":
		return "error"
	default:
		return "muted"
	}
}
