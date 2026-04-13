package api

import (
	"net/http"
	"strconv"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// QuotaUsageResponse is the JSON representation of project quota usage.
type QuotaUsageResponse struct {
	Projects []QuotaProjectUsage `json:"projects"`
}

// QuotaProjectUsage describes usage for a single project.
type QuotaProjectUsage struct {
	ProjectID         string `json:"projectId"`
	ProjectSlug       string `json:"projectSlug,omitempty"`
	EventsIngested    int64  `json:"eventsIngested"`
	TransactionsCount int64  `json:"transactionsIngested"`
	EventsRejected    int64  `json:"eventsRejected"`
}

// QuotaRateLimitResponse is the JSON representation of a per-project rate limit.
type QuotaRateLimitResponse struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	MaxEventsPerHour int       `json:"maxEventsPerHour"`
	MaxTransPerHour  int       `json:"maxTransactionsPerHour"`
	DateCreated      time.Time `json:"dateCreated"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type quotaRateLimitRequest struct {
	ProjectID        string `json:"project_id"`
	MaxEventsPerHour int    `json:"max_events_per_hour"`
	MaxTransPerHour  int    `json:"max_transactions_per_hour"`
}

func handleGetQuotaUsage(_ controlplane.CatalogStore, quota *sqlite.QuotaStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		hoursStr := r.URL.Query().Get("hours")
		hours := 24
		if hoursStr != "" {
			if h, err := strconv.Atoi(hoursStr); err == nil && h > 0 {
				hours = h
			}
		}
		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

		usage, err := quota.GetAllProjectUsage(r.Context(), since)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get quota usage.")
			return
		}
		resp := QuotaUsageResponse{
			Projects: make([]QuotaProjectUsage, 0, len(usage)),
		}
		for _, u := range usage {
			resp.Projects = append(resp.Projects, QuotaProjectUsage{
				ProjectID:         u.ProjectID,
				ProjectSlug:       u.ProjectSlug,
				EventsIngested:    u.EventsIngested,
				TransactionsCount: u.TransactionsCount,
				EventsRejected:    u.EventsRejected,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleListQuotaRateLimits(quota *sqlite.QuotaStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		limits, err := quota.ListRateLimits(r.Context())
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list rate limits.")
			return
		}
		resp := make([]QuotaRateLimitResponse, 0, len(limits))
		for _, limit := range limits {
			resp = append(resp, mapQuotaRateLimit(limit))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleUpsertQuotaRateLimit(quota *sqlite.QuotaStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body quotaRateLimitRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.ProjectID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "project_id is required.")
			return
		}
		if body.MaxEventsPerHour < 0 || body.MaxTransPerHour < 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Rate limits must be non-negative.")
			return
		}
		limit := &sqlite.QuotaRateLimit{
			ProjectID:        body.ProjectID,
			MaxEventsPerHour: body.MaxEventsPerHour,
			MaxTransPerHour:  body.MaxTransPerHour,
		}
		created, err := quota.UpsertRateLimit(r.Context(), limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save rate limit.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapQuotaRateLimit(*created))
	}
}

func handleDeleteQuotaRateLimit(quota *sqlite.QuotaStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID := PathParam(r, "project_id")
		if projectID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "project_id is required.")
			return
		}
		if err := quota.DeleteRateLimit(r.Context(), projectID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete rate limit.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func mapQuotaRateLimit(limit sqlite.QuotaRateLimit) QuotaRateLimitResponse {
	return QuotaRateLimitResponse{
		ID:               limit.ID,
		ProjectID:        limit.ProjectID,
		MaxEventsPerHour: limit.MaxEventsPerHour,
		MaxTransPerHour:  limit.MaxTransPerHour,
		DateCreated:      limit.DateCreated,
		UpdatedAt:        limit.UpdatedAt,
	}
}
