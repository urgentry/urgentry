package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/notify"
	"urgentry/pkg/id"
)

// handleListAlertRules handles GET /api/0/projects/{org_slug}/{proj_slug}/alerts/.
func handleListAlertRules(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rules, err := alerts.ListRules(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list alert rules.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rules)
	}
}

// handleGetAlertRule handles GET /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/.
func handleGetAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rule, err := alerts.GetRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load alert rule.")
			return
		}
		if rule == nil || rule.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Alert rule not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, rule)
	}
}

type alertRuleRequest struct {
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	RuleType   string            `json:"actionMatch"`
	Conditions []alert.Condition `json:"conditions"`
	Actions    []alert.Action    `json:"actions"`
}

// handleCreateAlertRule handles POST /api/0/projects/{org_slug}/{proj_slug}/alerts/.
func handleCreateAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body alertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		rule := &alert.Rule{
			ID:         id.New(),
			ProjectID:  projectID,
			Name:       strings.TrimSpace(body.Name),
			Status:     normalizeAlertStatus(body.Status),
			RuleType:   body.RuleType,
			Conditions: body.Conditions,
			Actions:    body.Actions,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		if len(rule.Conditions) == 0 {
			rule.Conditions = []alert.Condition{{
				ID:   "sentry.rules.conditions.every_event.EveryEventCondition",
				Name: "Every event",
			}}
		}
		if err := alerts.CreateRule(r.Context(), rule); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, rule)
	}
}

// handleUpdateAlertRule handles PUT /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/.
func handleUpdateAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		existing, err := alerts.GetRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load alert rule.")
			return
		}
		if existing == nil || existing.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Alert rule not found.")
			return
		}

		var body alertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		existing.Name = strings.TrimSpace(body.Name)
		existing.Status = normalizeAlertStatus(body.Status)
		if body.RuleType != "" {
			existing.RuleType = body.RuleType
		}
		if body.Conditions != nil {
			existing.Conditions = body.Conditions
		}
		if body.Actions != nil {
			existing.Actions = body.Actions
		}
		existing.UpdatedAt = time.Now().UTC()

		if err := alerts.UpdateRule(r.Context(), existing); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, existing)
	}
}

// handleDeleteAlertRule handles DELETE /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/.
func handleDeleteAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return deleteAlertRuleHandler(catalog, alerts, auth)
}

// deleteAlertRuleHandler is the shared implementation for deleting an alert rule
// by project and rule ID. Used by both the alerts and issue-alert-rules endpoints.
func deleteAlertRuleHandler(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc { //nolint:dupl
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		existing, err := alerts.GetRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load alert rule.")
			return
		}
		if existing == nil || existing.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Alert rule not found.")
			return
		}
		if err := alerts.DeleteRule(r.Context(), existing.ID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete alert rule.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListAlertOutbox handles GET /api/0/projects/{org_slug}/{proj_slug}/alerts/outbox/.
func handleListAlertOutbox(catalog controlplane.CatalogStore, outbox controlplane.NotificationOutboxStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rows, err := outbox.ListRecent(r.Context(), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list email notifications.")
			return
		}
		filtered := make([]notify.EmailNotification, 0, len(rows))
		for _, row := range rows {
			if row.ProjectID == projectID {
				filtered = append(filtered, row)
			}
		}
		httputil.WriteJSON(w, http.StatusOK, filtered)
	}
}

// handleListAlertDeliveries handles GET /api/0/projects/{org_slug}/{proj_slug}/alerts/deliveries/.
func handleListAlertDeliveries(catalog controlplane.CatalogStore, deliveries controlplane.NotificationDeliveryStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rows, err := deliveries.ListRecent(r.Context(), projectID, 50)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list alert deliveries.")
			return
		}
		if rows == nil {
			rows = []notify.DeliveryRecord{}
		}
		httputil.WriteJSON(w, http.StatusOK, rows)
	}
}

type webhookTestRequest struct {
	URL string `json:"url"`
}

// handleTestAlertWebhook handles POST /api/0/projects/{org_slug}/{proj_slug}/alerts/test-webhook/.
func handleTestAlertWebhook(catalog controlplane.CatalogStore, deliveries controlplane.NotificationDeliveryStore, authz *auth.Authorizer, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if authz == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Webhook test unavailable.")
			return
		}
		if !authz.ValidateCSRF(r) {
			httputil.WriteError(w, http.StatusForbidden, "CSRF validation failed.")
			return
		}
		if requireSessionPrincipal(w, r) == nil {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body webhookTestRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		url := strings.TrimSpace(body.URL)
		if url == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Webhook URL is required.")
			return
		}

		notifier := notify.NewNotifier(nil, deliveries)
		if err := notifier.SendTestWebhook(r.Context(), projectID, url); err != nil {
			httputil.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	}
}

func normalizeAlertStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "disabled", "inactive":
		return "disabled"
	default:
		return "active"
	}
}
