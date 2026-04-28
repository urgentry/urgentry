package web

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/auth"
	"urgentry/internal/notify"
	sharedstore "urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Alerts Page
// ---------------------------------------------------------------------------

type alertsData struct {
	Title            string
	Nav              string
	Environment      string   // selected environment ("" = all)
	Environments     []string // available environments for global nav
	DefaultProjectID string
	Rules            []alertRuleRow
	History          []alertHistoryRow
	Deliveries       []alertDeliveryRow
}

type alertRuleRow struct {
	ID           string
	ProjectID    string
	Name         string
	Status       string
	CreatedAt    string
	Trigger      string
	TriggerLabel string
	ThresholdMS  string
	EmailTargets string
	WebhookURL   string
	SlackURL     string
	FireCount    int
}

type alertHistoryRow struct {
	ID       string
	RuleID   string
	RuleName string
	GroupID  string
	EventID  string
	FiredAt  string
}

type alertDeliveryRow struct {
	ID             string
	ProjectID      string
	RuleID         string
	GroupID        string
	EventID        string
	Kind           string
	Target         string
	Status         string
	Attempts       int
	ResponseStatus string
	Error          string
	CreatedAt      string
	LastAttemptAt  string
	DeliveredAt    string
}

func (h *Handler) alertsPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	ctx := r.Context()
	history, err := h.webStore.ListAlertHistory(ctx, 50)
	if err != nil {
		writeWebInternal(w, r, "Failed to load alerts.")
		return
	}
	defaultProjectID := ""
	if id, err := h.webStore.DefaultProjectID(ctx); err == nil {
		defaultProjectID = id
	}

	var ruleSource []alert.Rule
	var deliverySource []notify.DeliveryRecord
	if h.catalog != nil && h.alerts != nil {
		projects, err := h.catalog.ListProjects(ctx, "")
		if err != nil {
			writeWebInternal(w, r, "Failed to load alerts.")
			return
		}
		if defaultProjectID == "" && len(projects) > 0 {
			defaultProjectID = projects[0].ID
		}
		for _, project := range projects {
			projectRules, err := h.alerts.ListRules(ctx, project.ID)
			if err != nil {
				writeWebInternal(w, r, "Failed to load alerts.")
				return
			}
			for _, item := range projectRules {
				if item != nil {
					ruleSource = append(ruleSource, *item)
				}
			}
			if h.deliveries != nil {
				rows, err := h.deliveries.ListRecent(ctx, project.ID, 50)
				if err != nil {
					writeWebInternal(w, r, "Failed to load alerts.")
					return
				}
				deliverySource = append(deliverySource, rows...)
			}
		}
	} else {
		overview, err := h.webStore.AlertsOverview(ctx, 100, 50, 50)
		if err != nil {
			writeWebInternal(w, r, "Failed to load alerts.")
			return
		}
		defaultProjectID = overview.DefaultProjectID
		for _, item := range overview.Rules {
			ruleSource = append(ruleSource, alertRuleSummaryToRule(item))
		}
		history = overview.History
		for _, item := range overview.Deliveries {
			deliverySource = append(deliverySource, notify.DeliveryRecord{
				ID:             item.ID,
				ProjectID:      item.ProjectID,
				RuleID:         item.RuleID,
				GroupID:        item.GroupID,
				EventID:        item.EventID,
				Kind:           item.Kind,
				Target:         item.Target,
				Status:         item.Status,
				Attempts:       item.Attempts,
				ResponseStatus: item.ResponseStatus,
				Error:          item.Error,
				CreatedAt:      item.CreatedAt,
				LastAttemptAt:  item.LastAttemptAt,
				DeliveredAt:    item.DeliveredAt,
			})
		}
	}
	if defaultProjectID == "" {
		defaultProjectID = "default-project"
	}
	slices.SortFunc(ruleSource, func(a, b alert.Rule) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	rules := make([]alertRuleRow, 0, len(ruleSource))
	for _, item := range ruleSource {
		rules = append(rules, alertRuleRow{
			ID:           item.ID,
			ProjectID:    item.ProjectID,
			Name:         item.Name,
			Status:       item.Status,
			CreatedAt:    timeAgo(item.CreatedAt),
			Trigger:      alertRuleTrigger(item),
			TriggerLabel: alertRuleTriggerLabel(item),
			ThresholdMS:  alertRuleThresholdMS(item),
			EmailTargets: alertRuleEmailTargets(item),
			WebhookURL:   alertRuleWebhookURL(item),
			SlackURL:     alertRuleSlackURL(item),
		})
	}

	historyRows := make([]alertHistoryRow, 0, len(history))
	for _, item := range history {
		historyRows = append(historyRows, alertHistoryRow{
			ID:       item.ID,
			RuleID:   item.RuleID,
			RuleName: item.RuleName,
			GroupID:  item.GroupID,
			EventID:  item.EventID,
			FiredAt:  timeAgo(item.FiredAt),
		})
	}
	slices.SortFunc(deliverySource, func(a, b notify.DeliveryRecord) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	deliveries := make([]alertDeliveryRow, 0, len(deliverySource))
	for _, item := range deliverySource {
		responseStatus := ""
		if item.ResponseStatus != nil {
			responseStatus = fmt.Sprintf("%d", *item.ResponseStatus)
		}
		deliveries = append(deliveries, alertDeliveryRow{
			ID:             item.ID,
			ProjectID:      item.ProjectID,
			RuleID:         item.RuleID,
			GroupID:        item.GroupID,
			EventID:        item.EventID,
			Kind:           item.Kind,
			Target:         item.Target,
			Status:         item.Status,
			Attempts:       item.Attempts,
			ResponseStatus: responseStatus,
			Error:          item.Error,
			CreatedAt:      timeAgo(item.CreatedAt),
			LastAttemptAt:  formatOptionalTime(item.LastAttemptAt),
			DeliveredAt:    formatOptionalTime(item.DeliveredAt),
		})
	}

	data := alertsData{
		Title:            "Alerts",
		Nav:              "alerts",
		Environment:      readSelectedEnvironment(r),
		Environments:     h.loadEnvironments(ctx),
		DefaultProjectID: defaultProjectID,
		Rules:            rules,
		History:          historyRows,
		Deliveries:       deliveries,
	}

	h.render(w, "alerts.html", data)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return timeAgo(*value)
}

func (h *Handler) createAlertRule(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	rule, err := alertRuleFromForm(r, "")
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, rule.ProjectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := h.alerts.CreateRule(r.Context(), rule); err != nil {
		writeWebInternal(w, r, "Failed to create alert rule")
		return
	}
	redirectAfterAlertMutation(w, r, "/alerts/")
}

func (h *Handler) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	ruleID := r.PathValue("id")
	existing, err := h.alerts.GetRule(r.Context(), ruleID)
	if err != nil {
		writeWebInternal(w, r, "Failed to load alert rule")
		return
	}
	if existing == nil {
		writeWebNotFound(w, r, "Alert rule not found")
		return
	}

	rule, err := alertRuleFromForm(r, existing.ProjectID)
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, existing.ProjectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	rule.ID = existing.ID
	rule.ProjectID = existing.ProjectID
	rule.CreatedAt = existing.CreatedAt
	rule.UpdatedAt = time.Now().UTC()

	if err := h.alerts.UpdateRule(r.Context(), rule); err != nil {
		writeWebInternal(w, r, "Failed to update alert rule")
		return
	}
	redirectAfterAlertMutation(w, r, "/alerts/")
}

func (h *Handler) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	ruleID := r.PathValue("id")
	existing, err := h.alerts.GetRule(r.Context(), ruleID)
	if err != nil {
		writeWebInternal(w, r, "Failed to load alert rule")
		return
	}
	if existing == nil {
		writeWebNotFound(w, r, "Alert rule not found")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, existing.ProjectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := h.alerts.DeleteRule(r.Context(), existing.ID); err != nil {
		writeWebInternal(w, r, "Failed to delete alert rule")
		return
	}
	redirectAfterAlertMutation(w, r, "/alerts/")
}

func redirectAfterAlertMutation(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func alertRuleSummaryToRule(item sharedstore.AlertRuleSummary) alert.Rule {
	rule := alert.Rule{
		ID:        item.ID,
		ProjectID: item.ProjectID,
		Name:      item.Name,
		Status:    item.Status,
		CreatedAt: item.CreatedAt,
		Conditions: []alert.Condition{{
			ID:   alert.ConditionEveryEvent,
			Name: "Every event",
		}},
	}
	switch item.Trigger {
	case "first_seen":
		rule.Conditions = []alert.Condition{{ID: alert.ConditionFirstSeen, Name: "First seen"}}
	case "regression":
		rule.Conditions = []alert.Condition{{ID: alert.ConditionRegression, Name: "Regression"}}
	case "slow_transaction":
		if threshold, err := strconv.ParseFloat(strings.TrimSpace(item.ThresholdMS), 64); err == nil && threshold > 0 {
			rule.Conditions = []alert.Condition{alert.SlowTransactionCondition(threshold)}
		}
	case "monitor_missed":
		rule.Conditions = []alert.Condition{alert.MonitorMissedCondition()}
	case "release_crash_free":
		if threshold, err := strconv.ParseFloat(strings.TrimSpace(item.ThresholdMS), 64); err == nil && threshold > 0 {
			rule.Conditions = []alert.Condition{alert.ReleaseCrashFreeBelowCondition(threshold)}
		}
	}
	for _, target := range splitAlertTargets(item.EmailTargets) {
		rule.Actions = append(rule.Actions, alert.Action{Type: alert.ActionTypeEmail, Target: target})
	}
	if item.WebhookURL != "" {
		rule.Actions = append(rule.Actions, alert.Action{Type: alert.ActionTypeWebhook, Target: item.WebhookURL})
	}
	if item.SlackURL != "" {
		rule.Actions = append(rule.Actions, alert.Action{Type: alert.ActionTypeSlack, Target: item.SlackURL})
	}
	return rule
}

func alertRuleTrigger(rule alert.Rule) string {
	trigger, _, _ := alertRuleTriggerSummary(&rule)
	return trigger
}

func alertRuleTriggerLabel(rule alert.Rule) string {
	_, label, _ := alertRuleTriggerSummary(&rule)
	return label
}

func alertRuleThresholdMS(rule alert.Rule) string {
	_, _, threshold := alertRuleTriggerSummary(&rule)
	return threshold
}

func alertRuleEmailTargets(rule alert.Rule) string {
	emailTargets, _, _ := alertRuleActionSummary(&rule)
	return emailTargets
}

func alertRuleWebhookURL(rule alert.Rule) string {
	_, webhookURL, _ := alertRuleActionSummary(&rule)
	return webhookURL
}

func alertRuleSlackURL(rule alert.Rule) string {
	_, _, slackURL := alertRuleActionSummary(&rule)
	return slackURL
}

func alertRuleTriggerSummary(rule *alert.Rule) (string, string, string) {
	for _, condition := range rule.Conditions {
		switch condition.ID {
		case alert.ConditionFirstSeen:
			return "first_seen", "First seen", ""
		case alert.ConditionRegression:
			return "regression", "Regression", ""
		case alert.ConditionEveryEvent:
			return "every_event", "Every event", ""
		case alert.ConditionSlowTransaction:
			threshold, ok := alertThresholdValue(condition)
			if ok {
				label := fmt.Sprintf("Slow transaction >= %.0f ms", threshold)
				return "slow_transaction", label, fmt.Sprintf("%.0f", threshold)
			}
			return "slow_transaction", "Slow transaction", ""
		case alert.ConditionMonitorMissed:
			return "monitor_missed", "Monitor missed check-in", ""
		case alert.ConditionReleaseCrashFree:
			threshold, ok := alertThresholdValue(condition)
			if ok {
				label := fmt.Sprintf("Crash-free below %.0f%%", threshold)
				return "release_crash_free", label, fmt.Sprintf("%.0f", threshold)
			}
			return "release_crash_free", "Crash-free below threshold", ""
		}
	}
	return "every_event", "Every event", ""
}

func alertRuleActionSummary(rule *alert.Rule) (emailTargets, webhookURL, slackURL string) {
	emails := make([]string, 0, len(rule.Actions))
	for _, action := range rule.Actions {
		switch action.Type {
		case alert.ActionTypeEmail:
			if strings.TrimSpace(action.Target) != "" {
				emails = append(emails, strings.TrimSpace(action.Target))
			}
		case alert.ActionTypeWebhook:
			if webhookURL == "" && strings.TrimSpace(action.Target) != "" {
				webhookURL = strings.TrimSpace(action.Target)
			}
		case alert.ActionTypeSlack:
			if slackURL == "" && strings.TrimSpace(action.Target) != "" {
				slackURL = strings.TrimSpace(action.Target)
			}
		}
	}
	return strings.Join(emails, ", "), webhookURL, slackURL
}

func alertThresholdValue(condition alert.Condition) (float64, bool) {
	switch value := condition.Value.(type) {
	case float64:
		return value, value > 0
	case int:
		return float64(value), value > 0
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed, err == nil && parsed > 0
	case map[string]any:
		raw, ok := value["threshold_ms"]
		if !ok {
			return 0, false
		}
		return alertThresholdValue(alert.Condition{Value: raw})
	default:
		return 0, false
	}
}

func alertRuleFromForm(r *http.Request, fallbackProjectID string) (*alert.Rule, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		projectID = fallbackProjectID
	}
	if projectID == "" {
		projectID = "default-project"
	}
	status := normalizeAlertStatus(r.FormValue("status"))
	trigger := strings.TrimSpace(r.FormValue("trigger"))
	if trigger == "" {
		trigger = "every_event"
	}

	rule := &alert.Rule{
		ProjectID: projectID,
		Name:      name,
		Status:    status,
		RuleType:  "any",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "slow_transaction":
		thresholdMS, err := parseThresholdMS(r.FormValue("threshold_ms"))
		if err != nil {
			return nil, err
		}
		rule.Conditions = []alert.Condition{alert.SlowTransactionCondition(thresholdMS)}
	case "monitor_missed":
		rule.Conditions = []alert.Condition{alert.MonitorMissedCondition()}
	case "release_crash_free":
		threshold, err := parseThresholdMS(r.FormValue("threshold_ms"))
		if err != nil {
			return nil, err
		}
		rule.Conditions = []alert.Condition{alert.ReleaseCrashFreeBelowCondition(threshold)}
	default:
		rule.Conditions = []alert.Condition{{
			ID:   triggerConditionID(trigger),
			Name: triggerLabel(trigger),
		}}
	}

	for _, recipient := range splitAlertTargets(r.FormValue("email_targets")) {
		rule.Actions = append(rule.Actions, alert.Action{
			Type:   alert.ActionTypeEmail,
			Target: recipient,
		})
	}
	if webhookURL := strings.TrimSpace(r.FormValue("webhook_url")); webhookURL != "" {
		rule.Actions = append(rule.Actions, alert.Action{
			Type:   alert.ActionTypeWebhook,
			Target: webhookURL,
		})
	}
	if slackURL := strings.TrimSpace(r.FormValue("slack_url")); slackURL != "" {
		rule.Actions = append(rule.Actions, alert.Action{
			Type:   alert.ActionTypeSlack,
			Target: slackURL,
		})
	}
	return rule, nil
}

func normalizeAlertStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "disabled", "inactive":
		return "disabled"
	default:
		return "active"
	}
}

func splitAlertTargets(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func triggerConditionID(trigger string) string {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "first_seen", "first-seen":
		return alert.ConditionFirstSeen
	case "regression":
		return alert.ConditionRegression
	case "monitor_missed":
		return alert.ConditionMonitorMissed
	case "release_crash_free":
		return alert.ConditionReleaseCrashFree
	default:
		return alert.ConditionEveryEvent
	}
}

func triggerLabel(trigger string) string {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "first_seen", "first-seen":
		return "First seen"
	case "regression":
		return "Regression"
	case "slow_transaction":
		return "Slow transaction"
	case "monitor_missed":
		return "Monitor missed"
	case "release_crash_free":
		return "Release crash-free below threshold"
	default:
		return "Every event"
	}
}

// formatDBTime converts a DB timestamp string to a human-readable format.
func formatDBTime(s string) string {
	t := parseDBTime(s)
	if t.IsZero() {
		return s
	}
	return timeAgo(t)
}

func parseThresholdMS(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("threshold is required")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("threshold_ms must be a positive number")
	}
	return value, nil
}

// ---------------------------------------------------------------------------
// Alert Detail Page
// ---------------------------------------------------------------------------

type alertDetailData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Rule         alertRuleRow
	History      []alertHistoryRow
	Deliveries   []alertDeliveryRow
}

func (h *Handler) alertDetailPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil || h.alerts == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	ctx := r.Context()
	ruleID := r.PathValue("id")

	rule, err := h.alerts.GetRule(ctx, ruleID)
	if err != nil {
		writeWebNotFound(w, r, "Alert rule not found.")
		return
	}

	history, err := h.webStore.ListAlertHistory(ctx, 100)
	if err != nil {
		history = nil
	}
	// Filter history to this rule.
	var ruleHistory []alertHistoryRow
	for _, item := range history {
		if item.RuleID == ruleID {
			ruleHistory = append(ruleHistory, alertHistoryRow{
				ID:       item.ID,
				RuleID:   item.RuleID,
				RuleName: item.RuleName,
				GroupID:  item.GroupID,
				EventID:  item.EventID,
				FiredAt:  item.FiredAt.Format(time.RFC3339),
			})
		}
	}

	deliveries, _ := h.webStore.ListAlertDeliveries(ctx, 100)
	var ruleDeliveries []alertDeliveryRow
	for _, d := range deliveries {
		if d.RuleID == ruleID {
			ruleDeliveries = append(ruleDeliveries, alertDeliveryRow{
				ID:        d.ID,
				ProjectID: d.ProjectID,
				RuleID:    d.RuleID,
				GroupID:   d.GroupID,
				EventID:   d.EventID,
				Kind:      d.Kind,
				Target:    d.Target,
				Status:    d.Status,
				Attempts:  d.Attempts,
			})
		}
	}

	summary, trigLabel, threshMS := alertRuleTriggerSummary(rule)
	emailTargets, webhookURL, slackURL := alertRuleActionSummary(rule)
	ruleRow := alertRuleRow{
		ID:           rule.ID,
		ProjectID:    rule.ProjectID,
		Name:         rule.Name,
		Status:       rule.Status,
		CreatedAt:    rule.CreatedAt.Format(time.RFC3339),
		Trigger:      summary,
		TriggerLabel: trigLabel,
		ThresholdMS:  threshMS,
		EmailTargets: emailTargets,
		WebhookURL:   webhookURL,
		SlackURL:     slackURL,
	}

	h.render(w, "alert-detail.html", alertDetailData{
		Title:        "Alert: " + rule.Name,
		Nav:          "alerts",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
		Rule:         ruleRow,
		History:      ruleHistory,
		Deliveries:   ruleDeliveries,
	})
}
