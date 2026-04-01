package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/sqlutil"
)

// MetricAlertStore is a SQLite-backed implementation of alert.MetricAlertRuleStore.
type MetricAlertStore struct {
	db *sql.DB
}

// NewMetricAlertStore creates a MetricAlertStore backed by the given database.
func NewMetricAlertStore(db *sql.DB) *MetricAlertStore {
	return &MetricAlertStore{db: db}
}

// CreateMetricAlertRule persists a new metric alert rule.
func (s *MetricAlertStore) CreateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error {
	if r.ID == "" {
		r.ID = generateID()
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	if r.State == "" {
		r.State = "ok"
	}
	if r.Status == "" {
		r.Status = "active"
	}
	actionsJSON, err := json.Marshal(r.TriggerActions)
	if err != nil {
		return err
	}
	var lastTriggered string
	if r.LastTriggeredAt != nil {
		lastTriggered = r.LastTriggeredAt.UTC().Format(time.RFC3339)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO metric_alert_rules
			(id, project_id, name, metric, threshold, threshold_type,
			 time_window_secs, resolve_threshold, environment, status,
			 trigger_actions_json, state, last_triggered_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Name, r.Metric, r.Threshold, r.ThresholdType,
		r.TimeWindowSecs, r.ResolveThreshold, r.Environment, r.Status,
		string(actionsJSON), r.State, lastTriggered,
		r.CreatedAt.UTC().Format(time.RFC3339),
		r.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetMetricAlertRule retrieves a metric alert rule by ID.
func (s *MetricAlertStore) GetMetricAlertRule(ctx context.Context, id string) (*alert.MetricAlertRule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, metric, threshold, threshold_type,
		        time_window_secs, resolve_threshold, environment, status,
		        trigger_actions_json, state, last_triggered_at, created_at, updated_at
		   FROM metric_alert_rules WHERE id = ?`, id)
	return scanMetricAlertRule(row)
}

// ListMetricAlertRules returns all metric alert rules for a project.
func (s *MetricAlertStore) ListMetricAlertRules(ctx context.Context, projectID string) ([]*alert.MetricAlertRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, metric, threshold, threshold_type,
		        time_window_secs, resolve_threshold, environment, status,
		        trigger_actions_json, state, last_triggered_at, created_at, updated_at
		   FROM metric_alert_rules WHERE project_id = ?
		  ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*alert.MetricAlertRule
	for rows.Next() {
		r, err := scanMetricAlertRuleRows(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ListAllActiveMetricAlertRules returns all metric alert rules with status "active" across all projects.
func (s *MetricAlertStore) ListAllActiveMetricAlertRules(ctx context.Context) ([]*alert.MetricAlertRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, metric, threshold, threshold_type,
		        time_window_secs, resolve_threshold, environment, status,
		        trigger_actions_json, state, last_triggered_at, created_at, updated_at
		   FROM metric_alert_rules WHERE status = 'active'
		  ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*alert.MetricAlertRule
	for rows.Next() {
		r, err := scanMetricAlertRuleRows(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateMetricAlertRule updates an existing metric alert rule.
func (s *MetricAlertStore) UpdateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error {
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	actionsJSON, err := json.Marshal(r.TriggerActions)
	if err != nil {
		return err
	}
	var lastTriggered string
	if r.LastTriggeredAt != nil {
		lastTriggered = r.LastTriggeredAt.UTC().Format(time.RFC3339)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE metric_alert_rules
		    SET name = ?, metric = ?, threshold = ?, threshold_type = ?,
		        time_window_secs = ?, resolve_threshold = ?, environment = ?,
		        status = ?, trigger_actions_json = ?, state = ?,
		        last_triggered_at = ?, updated_at = ?
		  WHERE id = ?`,
		r.Name, r.Metric, r.Threshold, r.ThresholdType,
		r.TimeWindowSecs, r.ResolveThreshold, r.Environment,
		r.Status, string(actionsJSON), r.State,
		lastTriggered, r.UpdatedAt.UTC().Format(time.RFC3339),
		r.ID,
	)
	return err
}

// DeleteMetricAlertRule removes a metric alert rule by ID.
func (s *MetricAlertStore) DeleteMetricAlertRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM metric_alert_rules WHERE id = ?`, id)
	return err
}

// scanMetricAlertRule scans a single row from a QueryRow call.
func scanMetricAlertRule(row *sql.Row) (*alert.MetricAlertRule, error) {
	var (
		id, projectID, name, metric        string
		thresholdType, environment, status  string
		actionsJSON, state                  string
		lastTriggeredAt, createdAt, updatedAt string
		threshold, resolveThreshold        float64
		timeWindowSecs                     int
	)
	err := row.Scan(
		&id, &projectID, &name, &metric, &threshold, &thresholdType,
		&timeWindowSecs, &resolveThreshold, &environment, &status,
		&actionsJSON, &state, &lastTriggeredAt, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return buildMetricAlertRule(id, projectID, name, metric, threshold, thresholdType,
		timeWindowSecs, resolveThreshold, environment, status, actionsJSON, state,
		lastTriggeredAt, createdAt, updatedAt)
}

// scanMetricAlertRuleRows scans one row from a Rows iterator.
func scanMetricAlertRuleRows(rows *sql.Rows) (*alert.MetricAlertRule, error) {
	var (
		id, projectID, name, metric        string
		thresholdType, environment, status  string
		actionsJSON, state                  string
		lastTriggeredAt, createdAt, updatedAt string
		threshold, resolveThreshold        float64
		timeWindowSecs                     int
	)
	err := rows.Scan(
		&id, &projectID, &name, &metric, &threshold, &thresholdType,
		&timeWindowSecs, &resolveThreshold, &environment, &status,
		&actionsJSON, &state, &lastTriggeredAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	return buildMetricAlertRule(id, projectID, name, metric, threshold, thresholdType,
		timeWindowSecs, resolveThreshold, environment, status, actionsJSON, state,
		lastTriggeredAt, createdAt, updatedAt)
}

func buildMetricAlertRule(
	id, projectID, name, metric string,
	threshold float64, thresholdType string,
	timeWindowSecs int, resolveThreshold float64,
	environment, status, actionsJSON, state string,
	lastTriggeredAt, createdAt, updatedAt string,
) (*alert.MetricAlertRule, error) {
	r := &alert.MetricAlertRule{
		ID:               id,
		ProjectID:        projectID,
		Name:             name,
		Metric:           metric,
		Threshold:        threshold,
		ThresholdType:    thresholdType,
		TimeWindowSecs:   timeWindowSecs,
		ResolveThreshold: resolveThreshold,
		Environment:      environment,
		Status:           status,
		State:            state,
		CreatedAt:        sqlutil.ParseDBTime(createdAt),
		UpdatedAt:        sqlutil.ParseDBTime(updatedAt),
	}
	if lastTriggeredAt != "" {
		t := sqlutil.ParseDBTime(lastTriggeredAt)
		if !t.IsZero() {
			r.LastTriggeredAt = &t
		}
	}
	if actionsJSON != "" && actionsJSON != "[]" {
		var actions []string
		if err := json.Unmarshal([]byte(actionsJSON), &actions); err == nil {
			r.TriggerActions = actions
		}
	}
	if r.TriggerActions == nil {
		r.TriggerActions = []string{}
	}
	if r.Status == "" {
		r.Status = "active"
	}
	if r.State == "" {
		r.State = "ok"
	}
	return r, nil
}
