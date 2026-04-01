package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func handleListMonitors(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		items, err := monitors.ListMonitors(r.Context(), projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list monitors.")
			return
		}
		resp := make([]Monitor, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapMonitor(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

type monitorRequest struct {
	Slug        string               `json:"slug"`
	Status      string               `json:"status"`
	Environment string               `json:"environment"`
	Config      sqlite.MonitorConfig `json:"config"`
}

func handleCreateMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		monitor := &sqlite.Monitor{
			ProjectID:   projectID,
			Slug:        strings.TrimSpace(body.Slug),
			Status:      normalizeMonitorStatus(body.Status),
			Environment: strings.TrimSpace(body.Environment),
			Config:      body.Config,
			DateCreated: time.Now().UTC(),
		}
		if monitor.Slug == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Slug is required.")
			return
		}
		item, err := monitors.UpsertMonitor(r.Context(), monitor)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapMonitor(*item))
	}
}

func handleGetMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		item, err := monitors.GetMonitor(r.Context(), projectID, PathParam(r, "monitor_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item))
	}
}

func handleUpdateMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		existing, err := monitors.GetMonitor(r.Context(), projectID, PathParam(r, "monitor_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if strings.TrimSpace(body.Slug) != "" && strings.TrimSpace(body.Slug) != existing.Slug {
			httputil.WriteError(w, http.StatusBadRequest, "Slug changes are not supported.")
			return
		}
		existing.Status = normalizeMonitorStatus(body.Status)
		if strings.TrimSpace(body.Environment) != "" {
			existing.Environment = strings.TrimSpace(body.Environment)
		}
		existing.Config = body.Config
		item, err := monitors.UpsertMonitor(r.Context(), existing)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item))
	}
}

func handleDeleteMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		existing, err := monitors.GetMonitor(r.Context(), projectID, PathParam(r, "monitor_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
			return
		}
		if err := monitors.DeleteMonitor(r.Context(), projectID, PathParam(r, "monitor_slug")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete monitor.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListMonitorCheckIns(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		items, err := monitors.ListCheckIns(r.Context(), projectID, PathParam(r, "monitor_slug"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list check-ins.")
			return
		}
		resp := make([]MonitorCheckIn, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapMonitorCheckIn(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func mapMonitor(item sqlite.Monitor) Monitor {
	resp := Monitor{
		ID:            item.ID,
		ProjectID:     item.ProjectID,
		Slug:          item.Slug,
		Status:        item.Status,
		Environment:   item.Environment,
		LastCheckInID: item.LastCheckInID,
		LastStatus:    item.LastStatus,
		Config: MonitorConfig{
			Schedule: MonitorSchedule{
				Type:    item.Config.Schedule.Type,
				Value:   item.Config.Schedule.Value,
				Unit:    item.Config.Schedule.Unit,
				Crontab: item.Config.Schedule.Crontab,
			},
			CheckInMargin: item.Config.CheckInMargin,
			MaxRuntime:    item.Config.MaxRuntime,
			Timezone:      item.Config.Timezone,
		},
		DateCreated: item.DateCreated,
		DateUpdated: item.UpdatedAt,
	}
	if !item.LastCheckInAt.IsZero() {
		resp.LastCheckInAt = &item.LastCheckInAt
	}
	if !item.NextCheckInAt.IsZero() {
		resp.NextCheckInAt = &item.NextCheckInAt
	}
	return resp
}

func normalizeMonitorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active":
		return "active"
	case "disabled", "paused":
		return "disabled"
	default:
		return "active"
	}
}

func mapMonitorCheckIn(item sqlite.MonitorCheckIn) MonitorCheckIn {
	resp := MonitorCheckIn{
		ID:          item.ID,
		MonitorID:   item.MonitorID,
		ProjectID:   item.ProjectID,
		CheckInID:   item.CheckInID,
		MonitorSlug: item.MonitorSlug,
		Status:      item.Status,
		Duration:    item.Duration,
		Release:     item.Release,
		Environment: item.Environment,
		Payload:     item.PayloadJSON,
		DateCreated: item.DateCreated,
	}
	if !item.ScheduledFor.IsZero() {
		resp.ScheduledFor = &item.ScheduledFor
	}
	return resp
}
