package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/alert"
)

// AlertStore is a SQLite-backed implementation of alert.RuleStore.
type AlertStore struct {
	db *sql.DB
}

// NewAlertStore creates an AlertStore backed by the given database.
func NewAlertStore(db *sql.DB) *AlertStore {
	return &AlertStore{db: db}
}

// CreateRule persists a new alert rule.
func (s *AlertStore) CreateRule(ctx context.Context, r *alert.Rule) error {
	if r.ID == "" {
		r.ID = generateID()
	}
	configJSON, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO alert_rules (id, project_id, name, status, config_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Name, r.Status, string(configJSON),
		r.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetRule retrieves a rule by ID.
func (s *AlertStore) GetRule(ctx context.Context, id string) (*alert.Rule, error) {
	var configJSON sql.NullString
	var name, projectID, status sql.NullString
	var createdAt sql.NullString
	var ruleID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, status, config_json, created_at
		 FROM alert_rules WHERE id = ?`, id,
	).Scan(&ruleID, &projectID, &name, &status, &configJSON, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return parseAlertRule(ruleID, nullStr(projectID), nullStr(name), nullStr(status), nullStr(configJSON), nullStr(createdAt))
}

// ListRules returns all rules for a project.
func (s *AlertStore) ListRules(ctx context.Context, projectID string) ([]*alert.Rule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, status, config_json, created_at
		 FROM alert_rules WHERE project_id = ?
		 ORDER BY created_at DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*alert.Rule
	for rows.Next() {
		var ruleID string
		var pID, name, status, configJSON, createdAt sql.NullString
		if err := rows.Scan(&ruleID, &pID, &name, &status, &configJSON, &createdAt); err != nil {
			return nil, err
		}
		r, err := parseAlertRule(ruleID, nullStr(pID), nullStr(name), nullStr(status), nullStr(configJSON), nullStr(createdAt))
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateRule updates an existing rule.
func (s *AlertStore) UpdateRule(ctx context.Context, r *alert.Rule) error {
	configJSON, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE alert_rules SET name = ?, status = ?, config_json = ? WHERE id = ?`,
		r.Name, r.Status, string(configJSON), r.ID,
	)
	return err
}

// DeleteRule removes a rule by ID.
func (s *AlertStore) DeleteRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	return err
}

// parseAlertRule constructs a Rule from DB columns.
// If config_json contains a full serialized Rule, it is used to populate
// Conditions and Actions. Otherwise, only the top-level fields are set.
func parseAlertRule(id, projectID, name, status, configJSON, createdAt string) (*alert.Rule, error) {
	r := &alert.Rule{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		Status:    status,
		CreatedAt: parseTime(createdAt),
		UpdatedAt: parseTime(createdAt),
	}
	if configJSON != "" && configJSON != "{}" {
		// Try to unmarshal the full rule from config_json.
		var full alert.Rule
		if err := json.Unmarshal([]byte(configJSON), &full); err == nil {
			r.RuleType = full.RuleType
			r.Conditions = full.Conditions
			r.Actions = full.Actions
			r.Config = full.Config
		}
	}
	return r, nil
}
