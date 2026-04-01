package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/alert"
	"urgentry/pkg/id"
)

// MetricAlertStore persists metric alert rules in PostgreSQL.
type MetricAlertStore struct {
	db *sql.DB
}

// NewMetricAlertStore creates a PostgreSQL-backed metric alert rule store.
func NewMetricAlertStore(db *sql.DB) *MetricAlertStore {
	return &MetricAlertStore{db: db}
}

// CreateMetricAlertRule persists a new metric alert rule.
func (s *MetricAlertStore) CreateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error {
	if r == nil {
		return nil
	}
	if r.ID == "" {
		r.ID = id.New()
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
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO metric_alert_rules
			(id, project_id, name, metric, threshold, threshold_type,
			 time_window_secs, resolve_threshold, environment, status,
			 trigger_actions_json, state, last_triggered_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14, $15)`,
		r.ID, r.ProjectID, r.Name, r.Metric, r.Threshold, r.ThresholdType,
		r.TimeWindowSecs, r.ResolveThreshold, r.Environment, r.Status,
		string(actionsJSON), r.State, optionalTime(r.LastTriggeredAt),
		r.CreatedAt.UTC(), r.UpdatedAt.UTC(),
	)
	return err
}

// GetMetricAlertRule retrieves a metric alert rule by ID.
func (s *MetricAlertStore) GetMetricAlertRule(ctx context.Context, ruleID string) (*alert.MetricAlertRule, error) {
	var (
		rID                                    string
		projectID, name, metric                sql.NullString
		thresholdType, environment, status     sql.NullString
		state                                  sql.NullString
		actionsJSON                            []byte
		threshold, resolveThreshold            float64
		timeWindowSecs                         int
		lastTriggeredAt, createdAt, updatedAt  sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, metric, threshold, threshold_type,
		        time_window_secs, resolve_threshold, environment, status,
		        trigger_actions_json, state, last_triggered_at, created_at, updated_at
		   FROM metric_alert_rules WHERE id = $1`, ruleID,
	).Scan(
		&rID, &projectID, &name, &metric, &threshold, &thresholdType,
		&timeWindowSecs, &resolveThreshold, &environment, &status,
		&actionsJSON, &state, &lastTriggeredAt, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return buildPgMetricAlertRule(rID, nullString(projectID), nullString(name), nullString(metric),
		threshold, nullString(thresholdType), timeWindowSecs, resolveThreshold,
		nullString(environment), nullString(status), actionsJSON, nullString(state),
		lastTriggeredAt, createdAt, updatedAt), nil
}

// ListMetricAlertRules returns all metric alert rules for a project.
func (s *MetricAlertStore) ListMetricAlertRules(ctx context.Context, projectID string) ([]*alert.MetricAlertRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, metric, threshold, threshold_type,
		        time_window_secs, resolve_threshold, environment, status,
		        trigger_actions_json, state, last_triggered_at, created_at, updated_at
		   FROM metric_alert_rules WHERE project_id = $1
		  ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*alert.MetricAlertRule
	for rows.Next() {
		var (
			rID                                    string
			pID, name, metric                      sql.NullString
			thresholdType, environment, status      sql.NullString
			state                                  sql.NullString
			actionsJSON                            []byte
			threshold, resolveThreshold            float64
			timeWindowSecs                         int
			lastTriggeredAt, createdAt, updatedAt  sql.NullTime
		)
		if err := rows.Scan(
			&rID, &pID, &name, &metric, &threshold, &thresholdType,
			&timeWindowSecs, &resolveThreshold, &environment, &status,
			&actionsJSON, &state, &lastTriggeredAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		r := buildPgMetricAlertRule(rID, nullString(pID), nullString(name), nullString(metric),
			threshold, nullString(thresholdType), timeWindowSecs, resolveThreshold,
			nullString(environment), nullString(status), actionsJSON, nullString(state),
			lastTriggeredAt, createdAt, updatedAt)
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
		var (
			rID                                    string
			pID, name, metric                      sql.NullString
			thresholdType, environment, status      sql.NullString
			state                                  sql.NullString
			actionsJSON                            []byte
			threshold, resolveThreshold            float64
			timeWindowSecs                         int
			lastTriggeredAt, createdAt, updatedAt  sql.NullTime
		)
		if err := rows.Scan(
			&rID, &pID, &name, &metric, &threshold, &thresholdType,
			&timeWindowSecs, &resolveThreshold, &environment, &status,
			&actionsJSON, &state, &lastTriggeredAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		r := buildPgMetricAlertRule(rID, nullString(pID), nullString(name), nullString(metric),
			threshold, nullString(thresholdType), timeWindowSecs, resolveThreshold,
			nullString(environment), nullString(status), actionsJSON, nullString(state),
			lastTriggeredAt, createdAt, updatedAt)
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateMetricAlertRule updates an existing metric alert rule.
func (s *MetricAlertStore) UpdateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error {
	if r == nil {
		return nil
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	actionsJSON, err := json.Marshal(r.TriggerActions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE metric_alert_rules
		    SET name = $1, metric = $2, threshold = $3, threshold_type = $4,
		        time_window_secs = $5, resolve_threshold = $6, environment = $7,
		        status = $8, trigger_actions_json = $9::jsonb, state = $10,
		        last_triggered_at = $11, updated_at = $12
		  WHERE id = $13`,
		r.Name, r.Metric, r.Threshold, r.ThresholdType,
		r.TimeWindowSecs, r.ResolveThreshold, r.Environment,
		r.Status, string(actionsJSON), r.State,
		optionalTime(r.LastTriggeredAt), r.UpdatedAt.UTC(),
		r.ID,
	)
	return err
}

// DeleteMetricAlertRule removes a metric alert rule by ID.
func (s *MetricAlertStore) DeleteMetricAlertRule(ctx context.Context, ruleID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM metric_alert_rules WHERE id = $1`, ruleID)
	return err
}

func buildPgMetricAlertRule(
	id, projectID, name, metric string,
	threshold float64, thresholdType string,
	timeWindowSecs int, resolveThreshold float64,
	environment, status string, actionsJSON []byte, state string,
	lastTriggeredAt, createdAt, updatedAt sql.NullTime,
) *alert.MetricAlertRule {
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
		LastTriggeredAt:  nullTimePtr(lastTriggeredAt),
		CreatedAt:        nullTime(createdAt),
		UpdatedAt:        nullTime(updatedAt),
	}
	if len(actionsJSON) > 0 && string(actionsJSON) != "[]" {
		var actions []string
		if err := json.Unmarshal(actionsJSON, &actions); err == nil {
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
	return r
}
