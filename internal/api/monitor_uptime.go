package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/outboundhttp"
	"urgentry/internal/sqlite"
)

// UptimeMonitorResponse is the JSON representation of an uptime monitor.
type UptimeMonitorResponse struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"projectId"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	IntervalSeconds int        `json:"intervalSeconds"`
	TimeoutSeconds  int        `json:"timeoutSeconds"`
	ExpectedStatus  int        `json:"expectedStatus"`
	Environment     string     `json:"environment,omitempty"`
	Status          string     `json:"status"`
	LastCheckAt     *time.Time `json:"lastCheckAt,omitempty"`
	LastStatusCode  int        `json:"lastStatusCode,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	LastLatencyMS   float64    `json:"lastLatencyMs,omitempty"`
	ConsecutiveFail int        `json:"consecutiveFail"`
	DateCreated     time.Time  `json:"dateCreated"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// UptimeCheckResultResponse is the JSON representation of an uptime check result.
type UptimeCheckResultResponse struct {
	ID              string    `json:"id"`
	UptimeMonitorID string    `json:"uptimeMonitorId"`
	ProjectID       string    `json:"projectId"`
	StatusCode      int       `json:"statusCode"`
	LatencyMS       float64   `json:"latencyMs"`
	Error           string    `json:"error,omitempty"`
	Status          string    `json:"status"`
	DateCreated     time.Time `json:"dateCreated"`
}

type uptimeMonitorRequest struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	ExpectedStatus  int    `json:"expected_status"`
	Environment     string `json:"environment"`
	Status          string `json:"status"`
}

func handleListUptimeMonitors(catalog controlplane.CatalogStore, uptime *sqlite.UptimeMonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		items, err := uptime.ListUptimeMonitors(r.Context(), projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list uptime monitors.")
			return
		}
		resp := make([]UptimeMonitorResponse, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapUptimeMonitor(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateUptimeMonitor(catalog controlplane.CatalogStore, uptime *sqlite.UptimeMonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		var body uptimeMonitorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "name is required.")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "url is required.")
			return
		}
		url := strings.TrimSpace(body.URL)
		if _, err := outboundhttp.ValidateTargetURL(url); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		monitor := &sqlite.UptimeMonitor{
			ProjectID:       projectID,
			Name:            strings.TrimSpace(body.Name),
			URL:             url,
			IntervalSeconds: body.IntervalSeconds,
			TimeoutSeconds:  body.TimeoutSeconds,
			ExpectedStatus:  body.ExpectedStatus,
			Environment:     strings.TrimSpace(body.Environment),
			Status:          normalizeUptimeMonitorStatus(body.Status),
		}
		created, err := uptime.CreateUptimeMonitor(r.Context(), monitor)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create uptime monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapUptimeMonitor(*created))
	}
}

func handleGetUptimeMonitor(uptime *sqlite.UptimeMonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		monitorID := PathParam(r, "monitor_id")
		if strings.TrimSpace(monitorID) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Monitor ID is required.")
			return
		}
		item, err := uptime.GetUptimeMonitor(r.Context(), monitorID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load uptime monitor.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Uptime monitor not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapUptimeMonitor(*item))
	}
}

func handleDeleteUptimeMonitor(uptime *sqlite.UptimeMonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		monitorID := PathParam(r, "monitor_id")
		if strings.TrimSpace(monitorID) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Monitor ID is required.")
			return
		}
		existing, err := uptime.GetUptimeMonitor(r.Context(), monitorID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load uptime monitor.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Uptime monitor not found.")
			return
		}
		if err := uptime.DeleteUptimeMonitor(r.Context(), monitorID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete uptime monitor.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListUptimeCheckResults(uptime *sqlite.UptimeMonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		monitorID := PathParam(r, "monitor_id")
		if strings.TrimSpace(monitorID) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Monitor ID is required.")
			return
		}
		items, err := uptime.ListCheckResults(r.Context(), monitorID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list check results.")
			return
		}
		resp := make([]UptimeCheckResultResponse, 0, len(items))
		for _, item := range items {
			resp = append(resp, UptimeCheckResultResponse{
				ID:              item.ID,
				UptimeMonitorID: item.UptimeMonitorID,
				ProjectID:       item.ProjectID,
				StatusCode:      item.StatusCode,
				LatencyMS:       item.LatencyMS,
				Error:           item.Error,
				Status:          item.Status,
				DateCreated:     item.DateCreated,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func normalizeUptimeMonitorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active":
		return "active"
	case "disabled", "paused":
		return "disabled"
	default:
		return "active"
	}
}

func mapUptimeMonitor(m sqlite.UptimeMonitor) UptimeMonitorResponse {
	resp := UptimeMonitorResponse{
		ID:              m.ID,
		ProjectID:       m.ProjectID,
		Name:            m.Name,
		URL:             m.URL,
		IntervalSeconds: m.IntervalSeconds,
		TimeoutSeconds:  m.TimeoutSeconds,
		ExpectedStatus:  m.ExpectedStatus,
		Environment:     m.Environment,
		Status:          m.Status,
		LastStatusCode:  m.LastStatusCode,
		LastError:       m.LastError,
		LastLatencyMS:   m.LastLatencyMS,
		ConsecutiveFail: m.ConsecutiveFail,
		DateCreated:     m.DateCreated,
		UpdatedAt:       m.UpdatedAt,
	}
	if !m.LastCheckAt.IsZero() {
		resp.LastCheckAt = &m.LastCheckAt
	}
	return resp
}
