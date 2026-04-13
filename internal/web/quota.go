package web

import (
	"net/http"
	"strconv"
	"time"

	"urgentry/internal/sqlite"
)

type quotaPageData struct {
	Title      string
	Nav        string
	Environment  string
	Environments []string
	Hours      int
	Usage      []sqlite.QuotaUsage
	RateLimits []sqlite.QuotaRateLimit
	Projects   []projectRef
}

type projectRef struct {
	ID   string
	Slug string
}

func (h *Handler) quotaPage(w http.ResponseWriter, r *http.Request) {
	if h.quotaStore == nil {
		http.Error(w, "quota store not configured", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	hours := 24

	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	usage, err := h.quotaStore.GetAllProjectUsage(ctx, since)
	if err != nil {
		http.Error(w, "failed to load usage", http.StatusInternalServerError)
		return
	}

	limits, err := h.quotaStore.ListRateLimits(ctx)
	if err != nil {
		http.Error(w, "failed to load rate limits", http.StatusInternalServerError)
		return
	}

	projects, err := h.catalog.ListProjects(ctx, "")
	if err != nil {
		projects = nil
	}
	var projRefs []projectRef
	for _, p := range projects {
		projRefs = append(projRefs, projectRef{ID: p.ID, Slug: p.Slug})
	}

	data := quotaPageData{
		Title:        "Quota & Usage",
		Nav:          "quota",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
		Hours:        hours,
		Usage:        usage,
		RateLimits:   limits,
		Projects:     projRefs,
	}
	h.render(w, "quota.html", data)
}

func (h *Handler) upsertQuotaRateLimit(w http.ResponseWriter, r *http.Request) {
	if h.quotaStore == nil {
		http.Error(w, "quota store not configured", http.StatusServiceUnavailable)
		return
	}
	projectID := r.FormValue("project_id")
	if projectID == "" {
		http.Error(w, "project_id is required", http.StatusBadRequest)
		return
	}
	maxEvents, _ := strconv.Atoi(r.FormValue("max_events_per_hour"))
	maxTrans, _ := strconv.Atoi(r.FormValue("max_transactions_per_hour"))

	_, err := h.quotaStore.UpsertRateLimit(r.Context(), &sqlite.QuotaRateLimit{
		ProjectID:        projectID,
		MaxEventsPerHour: maxEvents,
		MaxTransPerHour:  maxTrans,
	})
	if err != nil {
		http.Error(w, "failed to save rate limit", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/quota/", http.StatusSeeOther)
}

func (h *Handler) deleteQuotaRateLimit(w http.ResponseWriter, r *http.Request) {
	if h.quotaStore == nil {
		http.Error(w, "quota store not configured", http.StatusServiceUnavailable)
		return
	}
	projectID := r.PathValue("project_id")
	if projectID == "" {
		http.Error(w, "project_id is required", http.StatusBadRequest)
		return
	}
	if err := h.quotaStore.DeleteRateLimit(r.Context(), projectID); err != nil {
		http.Error(w, "failed to delete rate limit", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/quota/", http.StatusSeeOther)
}
