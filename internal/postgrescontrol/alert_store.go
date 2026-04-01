package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/alert"
	"urgentry/pkg/id"
)

// AlertStore persists alert rules in PostgreSQL.
type AlertStore struct {
	db *sql.DB
}

// NewAlertStore creates a PostgreSQL-backed alert rule store.
func NewAlertStore(db *sql.DB) *AlertStore {
	return &AlertStore{db: db}
}

// CreateRule persists a new alert rule.
func (s *AlertStore) CreateRule(ctx context.Context, rule *alert.Rule) error {
	if rule == nil {
		return nil
	}
	if rule.ID == "" {
		rule.ID = id.New()
	}
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = rule.CreatedAt
	}
	configJSON, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO alert_rules
			(id, project_id, name, status, rule_type, config_json, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)`,
		rule.ID, rule.ProjectID, rule.Name, rule.Status, rule.RuleType, string(configJSON), rule.CreatedAt.UTC(), rule.UpdatedAt.UTC(),
	)
	return err
}

// GetRule retrieves a rule by ID.
func (s *AlertStore) GetRule(ctx context.Context, id string) (*alert.Rule, error) {
	var ruleID string
	var projectID, name, status, ruleType sql.NullString
	var configJSON []byte
	var createdAt, updatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, status, rule_type, config_json, created_at, updated_at
		   FROM alert_rules
		  WHERE id = $1`,
		id,
	).Scan(&ruleID, &projectID, &name, &status, &ruleType, &configJSON, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return parseAlertRule(ruleID, nullString(projectID), nullString(name), nullString(status), nullString(ruleType), configJSON, createdAt, updatedAt)
}

// ListRules lists rules for one project.
func (s *AlertStore) ListRules(ctx context.Context, projectID string) ([]*alert.Rule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, status, rule_type, config_json, created_at, updated_at
		   FROM alert_rules
		  WHERE project_id = $1
		  ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*alert.Rule
	for rows.Next() {
		var ruleID string
		var projectIDValue, name, status, ruleType sql.NullString
		var configJSON []byte
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&ruleID, &projectIDValue, &name, &status, &ruleType, &configJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		rule, err := parseAlertRule(ruleID, nullString(projectIDValue), nullString(name), nullString(status), nullString(ruleType), configJSON, createdAt, updatedAt)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// UpdateRule updates one alert rule in place.
func (s *AlertStore) UpdateRule(ctx context.Context, rule *alert.Rule) error {
	if rule == nil {
		return nil
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = time.Now().UTC()
	}
	configJSON, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE alert_rules
		    SET name = $1,
		        status = $2,
		        rule_type = $3,
		        config_json = $4::jsonb,
		        updated_at = $5
		  WHERE id = $6`,
		rule.Name, rule.Status, rule.RuleType, string(configJSON), rule.UpdatedAt.UTC(), rule.ID,
	)
	return err
}

// DeleteRule removes a rule by ID.
func (s *AlertStore) DeleteRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	return err
}

func parseAlertRule(ruleID, projectID, name, status, ruleType string, configJSON []byte, createdAt, updatedAt sql.NullTime) (*alert.Rule, error) {
	rule := &alert.Rule{
		ID:        ruleID,
		ProjectID: projectID,
		Name:      name,
		Status:    status,
		RuleType:  ruleType,
		CreatedAt: nullTime(createdAt),
		UpdatedAt: nullTime(updatedAt),
	}
	if len(configJSON) > 0 && string(configJSON) != "{}" {
		var full alert.Rule
		if err := json.Unmarshal(configJSON, &full); err == nil {
			rule.RuleType = full.RuleType
			rule.FilterMatch = full.FilterMatch
			rule.Conditions = full.Conditions
			rule.Actions = full.Actions
			rule.Filters = full.Filters
			rule.Frequency = full.Frequency
			rule.Environment = full.Environment
			rule.Config = full.Config
		}
	}
	if rule.Status == "" {
		rule.Status = "active"
	}
	if rule.RuleType == "" {
		rule.RuleType = "all"
	}
	return rule, nil
}
