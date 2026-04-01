package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"urgentry/internal/alert"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// DefaultProjectID returns the oldest project ID for alert/settings defaults.
func (s *WebStore) DefaultProjectID(ctx context.Context) (string, error) {
	var projectID sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM projects ORDER BY created_at LIMIT 1`).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return sqlutil.NullStr(projectID), nil
}

// ListAlertRules returns recent alert rules with decoded trigger/action summaries.
func (s *WebStore) ListAlertRules(ctx context.Context, limit int) ([]store.AlertRuleSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.project_id, r.name, r.status, r.config_json, r.created_at,
		        COALESCE((SELECT COUNT(*) FROM alert_history h WHERE h.rule_id = r.id), 0) AS fire_count
		 FROM alert_rules r
		 ORDER BY r.created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []store.AlertRuleSummary
	for rows.Next() {
		var item store.AlertRuleSummary
		var projectID, name, status, configJSON, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &projectID, &name, &status, &configJSON, &createdAt, &item.FireCount); err != nil {
			return nil, err
		}
		item.ProjectID = sqlutil.NullStr(projectID)
		item.Name = sqlutil.NullStr(name)
		item.Status = sqlutil.NullStr(status)
		item.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		item.Trigger = "every_event"
		item.TriggerLabel = "Every event"
		if cfg := sqlutil.NullStr(configJSON); cfg != "" && cfg != "{}" {
			var parsed alert.Rule
			if err := json.Unmarshal([]byte(cfg), &parsed); err == nil {
				item.Trigger, item.TriggerLabel, item.ThresholdMS = alertTriggerSummary(&parsed)
				item.EmailTargets, item.WebhookURL, item.SlackURL = alertActionSummary(&parsed)
			}
		}
		rules = append(rules, item)
	}
	return rules, rows.Err()
}

// ListAlertHistory returns recent alert firings with joined rule names.
func (s *WebStore) ListAlertHistory(ctx context.Context, limit int) ([]store.AlertHistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.id, h.rule_id, COALESCE(r.name, ''), h.group_id, h.event_id, h.fired_at
		 FROM alert_history h
		 LEFT JOIN alert_rules r ON r.id = h.rule_id
		 ORDER BY h.fired_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []store.AlertHistoryEntry
	for rows.Next() {
		var item store.AlertHistoryEntry
		var ruleName, groupID, eventID, firedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.RuleID, &ruleName, &groupID, &eventID, &firedAt); err != nil {
			return nil, err
		}
		item.RuleName = sqlutil.NullStr(ruleName)
		item.GroupID = sqlutil.NullStr(groupID)
		item.EventID = sqlutil.NullStr(eventID)
		item.FiredAt = sqlutil.ParseDBTime(sqlutil.NullStr(firedAt))
		history = append(history, item)
	}
	return history, rows.Err()
}

// ListAlertDeliveries returns recent notification deliveries across projects.
func (s *WebStore) ListAlertDeliveries(ctx context.Context, limit int) ([]store.AlertDeliveryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, created_at, last_attempt_at, delivered_at
		 FROM notification_deliveries
		 ORDER BY created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []store.AlertDeliveryEntry
	for rows.Next() {
		var item store.AlertDeliveryEntry
		var projectID, ruleID, groupID, eventID, kind, target, status, errMsg, createdAt, lastAttemptAt, deliveredAt sql.NullString
		var responseStatus sql.NullInt64
		if err := rows.Scan(&item.ID, &projectID, &ruleID, &groupID, &eventID, &kind, &target, &status, &item.Attempts, &responseStatus, &errMsg, &createdAt, &lastAttemptAt, &deliveredAt); err != nil {
			return nil, err
		}
		item.ProjectID = sqlutil.NullStr(projectID)
		item.RuleID = sqlutil.NullStr(ruleID)
		item.GroupID = sqlutil.NullStr(groupID)
		item.EventID = sqlutil.NullStr(eventID)
		item.Kind = sqlutil.NullStr(kind)
		item.Target = sqlutil.NullStr(target)
		item.Status = sqlutil.NullStr(status)
		if responseStatus.Valid {
			value := int(responseStatus.Int64)
			item.ResponseStatus = &value
		}
		item.Error = sqlutil.NullStr(errMsg)
		item.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		item.LastAttemptAt = parseOptionalTime(lastAttemptAt)
		item.DeliveredAt = parseOptionalTime(deliveredAt)
		deliveries = append(deliveries, item)
	}
	return deliveries, rows.Err()
}

// SettingsOverview returns the settings-page read model as one store call.
func (s *WebStore) SettingsOverview(ctx context.Context, auditLimit int) (store.SettingsOverview, error) {
	var overview store.SettingsOverview

	projects, err := ListProjects(ctx, s.db, "")
	if err != nil {
		return overview, err
	}
	if len(projects) > 0 {
		project := projects[0]
		overview.Project = &project
	}

	overview.ProjectKeys, err = ListAllProjectKeys(ctx, s.db)
	if err != nil {
		return overview, err
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT
			(SELECT COUNT(*) FROM events),
			(SELECT COUNT(*) FROM groups)`,
	).Scan(&overview.EventCount, &overview.GroupCount)
	if err != nil {
		return overview, err
	}

	if overview.Project != nil && overview.Project.OrgSlug != "" {
		overview.AuditLogs, err = NewAuditStore(s.db).ListOrganizationAuditLogs(ctx, overview.Project.OrgSlug, auditLimit)
		if err != nil {
			return overview, err
		}
		overview.TelemetryPolicies, err = listProjectTelemetryPolicies(ctx, s.db, *overview.Project)
		if err != nil {
			return overview, err
		}
	}

	return overview, nil
}

// AlertsOverview returns the alerts-page read model as one store call.
func (s *WebStore) AlertsOverview(ctx context.Context, ruleLimit, historyLimit, deliveryLimit int) (store.AlertsOverview, error) {
	var overview store.AlertsOverview
	var err error

	overview.DefaultProjectID, err = s.DefaultProjectID(ctx)
	if err != nil {
		return overview, err
	}
	overview.Rules, err = s.ListAlertRules(ctx, ruleLimit)
	if err != nil {
		return overview, err
	}
	overview.History, err = s.ListAlertHistory(ctx, historyLimit)
	if err != nil {
		return overview, err
	}
	overview.Deliveries, err = s.ListAlertDeliveries(ctx, deliveryLimit)
	if err != nil {
		return overview, err
	}
	return overview, nil
}

func alertTriggerSummary(rule *alert.Rule) (string, string, string) {
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

func alertActionSummary(rule *alert.Rule) (emailTargets, webhookURL, slackURL string) {
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
