package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/pkg/id"
)

type metricAlertRuleRequest struct {
	Name             string   `json:"name"`
	Metric           string   `json:"metric"`
	CustomMetricName string   `json:"customMetricName"`
	Threshold        float64  `json:"threshold"`
	ThresholdType    string   `json:"thresholdType"`
	TimeWindowSecs   int      `json:"timeWindowSecs"`
	ResolveThreshold float64  `json:"resolveThreshold"`
	Environment      string   `json:"environment"`
	Status           string   `json:"status"`
	TriggerActions   []string `json:"triggerActions"`
}

var validMetrics = map[string]bool{
	"error_count":       true,
	"transaction_count": true,
	"p95_latency":       true,
	"failure_rate":      true,
	"apdex":             true,
	"custom_metric":     true,
}

var validTimeWindows = map[int]bool{
	60: true, 300: true, 600: true, 900: true, 1800: true, 3600: true,
}

// handleListMetricAlertRules handles GET /api/0/projects/{org}/{proj}/metric-alerts/.
func handleListMetricAlertRules(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rules, err := store.ListMetricAlertRules(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list metric alert rules.")
			return
		}
		if rules == nil {
			rules = []*alert.MetricAlertRule{}
		}
		httputil.WriteJSON(w, http.StatusOK, rules)
	}
}

// handleGetMetricAlertRule handles GET /api/0/projects/{org}/{proj}/metric-alerts/{rule_id}/.
func handleGetMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rule, err := store.GetMetricAlertRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load metric alert rule.")
			return
		}
		if rule == nil || rule.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Metric alert rule not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rule)
	}
}

// handleCreateMetricAlertRule handles POST /api/0/projects/{org}/{proj}/metric-alerts/.
func handleCreateMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body metricAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		if !validMetrics[body.Metric] {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid metric. Must be one of: error_count, transaction_count, p95_latency, failure_rate, apdex, custom_metric.")
			return
		}
		if body.Metric == "custom_metric" && strings.TrimSpace(body.CustomMetricName) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "customMetricName is required when metric is custom_metric.")
			return
		}
		thresholdType := normalizeThresholdType(body.ThresholdType)
		timeWindow := body.TimeWindowSecs
		if !validTimeWindows[timeWindow] {
			timeWindow = 300
		}

		now := time.Now().UTC()
		rule := &alert.MetricAlertRule{
			ID:               id.New(),
			ProjectID:        projectID,
			Name:             strings.TrimSpace(body.Name),
			Metric:           body.Metric,
			CustomMetricName: strings.TrimSpace(body.CustomMetricName),
			Threshold:        body.Threshold,
			ThresholdType:    thresholdType,
			TimeWindowSecs:   timeWindow,
			ResolveThreshold: body.ResolveThreshold,
			Environment:      strings.TrimSpace(body.Environment),
			Status:           normalizeAlertStatus(body.Status),
			TriggerActions:   body.TriggerActions,
			State:            "ok",
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if rule.TriggerActions == nil {
			rule.TriggerActions = []string{}
		}
		if err := store.CreateMetricAlertRule(r.Context(), rule); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create metric alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, rule)
	}
}

// handleUpdateMetricAlertRule handles PUT /api/0/projects/{org}/{proj}/metric-alerts/{rule_id}/.
func handleUpdateMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		existing, err := store.GetMetricAlertRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load metric alert rule.")
			return
		}
		if existing == nil || existing.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Metric alert rule not found.")
			return
		}

		var body metricAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		existing.Name = strings.TrimSpace(body.Name)
		existing.Status = normalizeAlertStatus(body.Status)
		existing.UpdatedAt = time.Now().UTC()

		if body.Metric != "" {
			if !validMetrics[body.Metric] {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid metric. Must be one of: error_count, transaction_count, p95_latency, failure_rate, apdex, custom_metric.")
				return
			}
			existing.Metric = body.Metric
		}
		if body.Metric == "custom_metric" {
			if strings.TrimSpace(body.CustomMetricName) == "" {
				httputil.WriteError(w, http.StatusBadRequest, "customMetricName is required when metric is custom_metric.")
				return
			}
			existing.CustomMetricName = strings.TrimSpace(body.CustomMetricName)
		} else if body.CustomMetricName != "" {
			existing.CustomMetricName = strings.TrimSpace(body.CustomMetricName)
		}
		if body.ThresholdType != "" {
			existing.ThresholdType = normalizeThresholdType(body.ThresholdType)
		}
		if body.TimeWindowSecs != 0 {
			if validTimeWindows[body.TimeWindowSecs] {
				existing.TimeWindowSecs = body.TimeWindowSecs
			}
		}
		existing.Threshold = body.Threshold
		existing.ResolveThreshold = body.ResolveThreshold
		existing.Environment = strings.TrimSpace(body.Environment)
		if body.TriggerActions != nil {
			existing.TriggerActions = body.TriggerActions
		}

		if err := store.UpdateMetricAlertRule(r.Context(), existing); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update metric alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, existing)
	}
}

// handleDeleteMetricAlertRule handles DELETE /api/0/projects/{org}/{proj}/metric-alerts/{rule_id}/.
func handleDeleteMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		existing, err := store.GetMetricAlertRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load metric alert rule.")
			return
		}
		if existing == nil || existing.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Metric alert rule not found.")
			return
		}
		if err := store.DeleteMetricAlertRule(r.Context(), existing.ID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete metric alert rule.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func normalizeThresholdType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "below":
		return "below"
	default:
		return "above"
	}
}
