package api

import (
	"encoding/json"
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

type orgMetricAlertRuleRequest struct {
	Name             string                  `json:"name"`
	Projects         []string                `json:"projects"`
	Aggregate        string                  `json:"aggregate"`
	Query            string                  `json:"query"`
	ThresholdType    int                     `json:"thresholdType"`
	TimeWindow       int                     `json:"timeWindow"`
	ResolveThreshold float64                 `json:"resolveThreshold"`
	Environment      string                  `json:"environment"`
	Status           string                  `json:"status"`
	Triggers         []orgMetricAlertTrigger `json:"triggers"`
}

type orgMetricAlertTrigger struct {
	Label          string `json:"label"`
	AlertThreshold float64 `json:"alertThreshold"`
	Actions        []any  `json:"actions"`
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

func handleListOrgMetricAlertRules(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projects, ok := orgProjectsByID(w, r, catalog)
		if !ok {
			return
		}
		rules := make([]*alert.MetricAlertRule, 0)
		for projectID := range projects {
			items, err := store.ListMetricAlertRules(r.Context(), projectID)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list metric alert rules.")
				return
			}
			rules = append(rules, items...)
		}
		if rules == nil {
			rules = []*alert.MetricAlertRule{}
		}
		httputil.WriteJSON(w, http.StatusOK, rules)
	}
}

func handleCreateOrgMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body orgMetricAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		project, ok := orgMetricAlertProjectFromBody(w, r, catalog, body.Projects)
		if !ok {
			return
		}
		rule, ok := newOrgMetricAlertRule(w, project.ID, body)
		if !ok {
			return
		}
		if err := store.CreateMetricAlertRule(r.Context(), rule); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create metric alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, rule)
	}
}

func handleGetOrgMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		rule, ok := findOrgMetricAlertRule(w, r, catalog, store)
		if !ok {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rule)
	}
}

func handleUpdateOrgMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		existing, ok := findOrgMetricAlertRule(w, r, catalog, store)
		if !ok {
			return
		}
		var body orgMetricAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		updated, ok := newOrgMetricAlertRule(w, existing.ProjectID, body)
		if !ok {
			return
		}
		updated.ID = existing.ID
		updated.State = existing.State
		updated.LastTriggeredAt = existing.LastTriggeredAt
		updated.CreatedAt = existing.CreatedAt
		if err := store.UpdateMetricAlertRule(r.Context(), updated); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update metric alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, updated)
	}
}

func handleDeleteOrgMetricAlertRule(catalog controlplane.CatalogStore, store controlplane.MetricAlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		existing, ok := findOrgMetricAlertRule(w, r, catalog, store)
		if !ok {
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

func orgMetricAlertProjectFromBody(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, projects []string) (*Project, bool) {
	if len(projects) == 0 || strings.TrimSpace(projects[0]) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "projects must contain one project slug.")
		return nil, false
	}
	project, err := catalog.GetProject(r.Context(), PathParam(r, "org_slug"), strings.TrimSpace(projects[0]))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project.")
		return nil, false
	}
	if project == nil {
		httputil.WriteError(w, http.StatusNotFound, "Project not found.")
		return nil, false
	}
	return project, true
}

func newOrgMetricAlertRule(w http.ResponseWriter, projectID string, body orgMetricAlertRuleRequest) (*alert.MetricAlertRule, bool) {
	if strings.TrimSpace(body.Name) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
		return nil, false
	}
	metric := metricFromAggregate(body.Aggregate)
	if !validMetrics[metric] {
		httputil.WriteError(w, http.StatusBadRequest, "Aggregate is not supported by this compatibility layer.")
		return nil, false
	}
	threshold, actions := orgMetricAlertTriggerPayload(body.Triggers)
	if threshold == 0 && len(body.Triggers) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "At least one trigger is required.")
		return nil, false
	}
	window := body.TimeWindow * 60
	if !validTimeWindows[window] {
		window = 300
	}
	thresholdType := "above"
	if body.ThresholdType == 1 {
		thresholdType = "below"
	}
	now := time.Now().UTC()
	rule := &alert.MetricAlertRule{
		ID:               id.New(),
		ProjectID:        projectID,
		Name:             strings.TrimSpace(body.Name),
		Metric:           metric,
		Threshold:        threshold,
		ThresholdType:    thresholdType,
		TimeWindowSecs:   window,
		ResolveThreshold: body.ResolveThreshold,
		Environment:      strings.TrimSpace(body.Environment),
		Status:           normalizeAlertStatus(body.Status),
		TriggerActions:   actions,
		State:            "ok",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return rule, true
}

func findOrgMetricAlertRule(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, store controlplane.MetricAlertStore) (*alert.MetricAlertRule, bool) {
	projects, ok := orgProjectsByID(w, r, catalog)
	if !ok {
		return nil, false
	}
	rule, err := store.GetMetricAlertRule(r.Context(), PathParam(r, "rule_id"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load metric alert rule.")
		return nil, false
	}
	if rule == nil {
		httputil.WriteError(w, http.StatusNotFound, "Metric alert rule not found.")
		return nil, false
	}
	if _, ok := projects[rule.ProjectID]; !ok {
		httputil.WriteError(w, http.StatusNotFound, "Metric alert rule not found.")
		return nil, false
	}
	return rule, true
}

func metricFromAggregate(aggregate string) string {
	value := strings.ToLower(strings.TrimSpace(aggregate))
	switch {
	case strings.Contains(value, "p50"), strings.Contains(value, "p75"),
		strings.Contains(value, "p95"), strings.Contains(value, "p99"),
		strings.Contains(value, "percentile"):
		return "p95_latency"
	case strings.Contains(value, "failure_rate"):
		return "failure_rate"
	case strings.Contains(value, "apdex"):
		return "apdex"
	case strings.Contains(value, "count_unique"), strings.Contains(value, "count("):
		return "error_count"
	case strings.Contains(value, "transaction"):
		return "transaction_count"
	case strings.Contains(value, "avg"), strings.Contains(value, "sum"),
		strings.Contains(value, "max"), strings.Contains(value, "min"):
		return "custom_metric"
	default:
		return "error_count"
	}
}

func orgMetricAlertTriggerPayload(triggers []orgMetricAlertTrigger) (float64, []string) {
	if len(triggers) == 0 {
		return 0, []string{}
	}
	threshold := triggers[0].AlertThreshold
	actions := make([]string, 0)
	for _, trigger := range triggers {
		for _, action := range trigger.Actions {
			data, err := json.Marshal(action)
			if err != nil {
				continue
			}
			actions = append(actions, string(data))
		}
	}
	return threshold, actions
}
