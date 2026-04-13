package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SamplingRule defines a server-side dynamic sampling configuration.
type SamplingRule struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	SampleRate  float64         `json:"sampleRate"` // 0.0 to 1.0
	Conditions  json.RawMessage `json:"conditions"`
	Active      bool            `json:"active"`
	DateCreated time.Time       `json:"dateCreated"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// SamplingConditions contains the parsed match criteria for a sampling rule.
type SamplingConditions struct {
	Environment string `json:"environment,omitempty"`
	Release     string `json:"release,omitempty"`
	Transaction string `json:"transaction,omitempty"`
}

// SamplingRuleStore persists dynamic sampling rules in SQLite.
type SamplingRuleStore struct {
	db *sql.DB
}

// NewSamplingRuleStore creates a new SamplingRuleStore.
func NewSamplingRuleStore(db *sql.DB) *SamplingRuleStore {
	return &SamplingRuleStore{db: db}
}

// CreateSamplingRule inserts a new sampling rule.
func (s *SamplingRuleStore) CreateSamplingRule(ctx context.Context, rule *SamplingRule) (*SamplingRule, error) {
	if rule == nil {
		return nil, nil
	}
	if strings.TrimSpace(rule.ProjectID) == "" {
		return nil, fmt.Errorf("sampling rule project_id is required")
	}
	if rule.SampleRate < 0.0 || rule.SampleRate > 1.0 {
		return nil, fmt.Errorf("sampling rule sample_rate must be between 0.0 and 1.0")
	}
	if rule.ID == "" {
		rule.ID = generateID()
	}
	now := time.Now().UTC()
	if rule.DateCreated.IsZero() {
		rule.DateCreated = now
	}
	rule.UpdatedAt = now

	conditionsJSON := "{}"
	if len(rule.Conditions) > 0 {
		conditionsJSON = string(rule.Conditions)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sampling_rules
			(id, project_id, sample_rate, conditions_json, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rule.ID,
		rule.ProjectID,
		rule.SampleRate,
		conditionsJSON,
		rule.Active,
		rule.DateCreated.UTC().Format(time.RFC3339),
		rule.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("insert sampling rule: %w", err)
	}
	return rule, nil
}

// GetSamplingRule returns a sampling rule by ID.
func (s *SamplingRuleStore) GetSamplingRule(ctx context.Context, id string) (*SamplingRule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, sample_rate, COALESCE(conditions_json, '{}'), active, created_at, updated_at
		 FROM sampling_rules WHERE id = ?`, id,
	)
	return scanSamplingRule(row)
}

// ListSamplingRules returns sampling rules for a project.
func (s *SamplingRuleStore) ListSamplingRules(ctx context.Context, projectID string) ([]SamplingRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, sample_rate, COALESCE(conditions_json, '{}'), active, created_at, updated_at
		 FROM sampling_rules
		 WHERE project_id = ?
		 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sampling rules: %w", err)
	}
	defer rows.Close()
	var out []SamplingRule
	for rows.Next() {
		rule, err := scanSamplingRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

// ListActiveSamplingRules returns all active rules for a project.
func (s *SamplingRuleStore) ListActiveSamplingRules(ctx context.Context, projectID string) ([]SamplingRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, sample_rate, COALESCE(conditions_json, '{}'), active, created_at, updated_at
		 FROM sampling_rules
		 WHERE project_id = ? AND active = 1
		 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list active sampling rules: %w", err)
	}
	defer rows.Close()
	var out []SamplingRule
	for rows.Next() {
		rule, err := scanSamplingRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

// DeleteSamplingRule removes a sampling rule.
func (s *SamplingRuleStore) DeleteSamplingRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sampling_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete sampling rule: %w", err)
	}
	return nil
}

// EvaluateSampling checks whether a transaction should be admitted based on
// active sampling rules. Returns true if the event should be kept.
func (s *SamplingRuleStore) EvaluateSampling(ctx context.Context, projectID, environment, release, transaction string) (bool, error) {
	rules, err := s.ListActiveSamplingRules(ctx, projectID)
	if err != nil {
		return true, err // fail open
	}
	if len(rules) == 0 {
		return true, nil // no rules = admit all
	}

	for _, rule := range rules {
		var cond SamplingConditions
		if len(rule.Conditions) > 0 && string(rule.Conditions) != "{}" {
			_ = json.Unmarshal(rule.Conditions, &cond)
		}
		if !conditionMatches(cond, environment, release, transaction) {
			continue
		}
		// Use a deterministic hash-based decision from the transaction name
		// to keep sampling consistent for the same transaction.
		if !sampleDecision(rule.SampleRate, projectID+"/"+transaction) {
			return false, nil
		}
	}
	return true, nil
}

// conditionMatches returns true if the given event matches the rule conditions.
func conditionMatches(cond SamplingConditions, environment, release, transaction string) bool {
	if cond.Environment != "" && !strings.EqualFold(cond.Environment, environment) {
		return false
	}
	if cond.Release != "" && cond.Release != release {
		return false
	}
	if cond.Transaction != "" && !strings.Contains(transaction, cond.Transaction) {
		return false
	}
	return true
}

// sampleDecision uses a simple FNV-like hash to deterministically decide
// whether to keep or drop a given key at the specified rate.
func sampleDecision(rate float64, key string) bool {
	if rate >= 1.0 {
		return true
	}
	if rate <= 0.0 {
		return false
	}
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	threshold := uint32(rate * float64(^uint32(0)))
	return h <= threshold
}

type samplingScanner interface {
	Scan(dest ...any) error
}

func scanSamplingRule(s samplingScanner) (*SamplingRule, error) {
	var rule SamplingRule
	var conditionsJSON, createdAt, updatedAt string
	if err := s.Scan(
		&rule.ID, &rule.ProjectID, &rule.SampleRate,
		&conditionsJSON, &rule.Active, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan sampling rule: %w", err)
	}
	rule.DateCreated = parseTime(createdAt)
	rule.UpdatedAt = parseTime(updatedAt)
	if strings.TrimSpace(conditionsJSON) != "" && conditionsJSON != "{}" {
		rule.Conditions = json.RawMessage(conditionsJSON)
	}
	return &rule, nil
}
