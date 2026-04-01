package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UptimeMonitor is an HTTP-polling uptime monitor definition.
type UptimeMonitor struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	IntervalSeconds int       `json:"intervalSeconds"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	ExpectedStatus  int       `json:"expectedStatus"`
	Environment     string    `json:"environment,omitempty"`
	Status          string    `json:"status"` // active, disabled
	LastCheckAt     time.Time `json:"lastCheckAt,omitempty"`
	LastStatusCode  int       `json:"lastStatusCode,omitempty"`
	LastError       string    `json:"lastError,omitempty"`
	LastLatencyMS   float64   `json:"lastLatencyMs,omitempty"`
	ConsecutiveFail int       `json:"consecutiveFail"`
	DateCreated     time.Time `json:"dateCreated"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// UptimeCheckResult records one HTTP poll result.
type UptimeCheckResult struct {
	ID              string    `json:"id"`
	UptimeMonitorID string    `json:"uptimeMonitorId"`
	ProjectID       string    `json:"projectId"`
	StatusCode      int       `json:"statusCode"`
	LatencyMS       float64   `json:"latencyMs"`
	Error           string    `json:"error,omitempty"`
	Status          string    `json:"status"` // ok, error
	DateCreated     time.Time `json:"dateCreated"`
}

// UptimeMonitorStore persists uptime monitors and their check results.
type UptimeMonitorStore struct {
	db *sql.DB
}

// NewUptimeMonitorStore creates a new UptimeMonitorStore.
func NewUptimeMonitorStore(db *sql.DB) *UptimeMonitorStore {
	return &UptimeMonitorStore{db: db}
}

// CreateUptimeMonitor inserts a new uptime monitor.
func (s *UptimeMonitorStore) CreateUptimeMonitor(ctx context.Context, m *UptimeMonitor) (*UptimeMonitor, error) {
	if m == nil {
		return nil, nil
	}
	if strings.TrimSpace(m.ProjectID) == "" {
		return nil, fmt.Errorf("uptime monitor project_id is required")
	}
	if strings.TrimSpace(m.URL) == "" {
		return nil, fmt.Errorf("uptime monitor url is required")
	}
	if strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("uptime monitor name is required")
	}
	if m.ID == "" {
		m.ID = generateID()
	}
	if m.IntervalSeconds <= 0 {
		m.IntervalSeconds = 60
	}
	if m.TimeoutSeconds <= 0 {
		m.TimeoutSeconds = 10
	}
	if m.ExpectedStatus <= 0 {
		m.ExpectedStatus = 200
	}
	if m.Status == "" {
		m.Status = "active"
	}
	if m.Environment == "" {
		m.Environment = "production"
	}
	now := time.Now().UTC()
	if m.DateCreated.IsZero() {
		m.DateCreated = now
	}
	m.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO uptime_monitors
			(id, project_id, name, url, interval_seconds, timeout_seconds, expected_status, environment, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID,
		m.ProjectID,
		m.Name,
		m.URL,
		m.IntervalSeconds,
		m.TimeoutSeconds,
		m.ExpectedStatus,
		nullIfEmpty(m.Environment),
		m.Status,
		m.DateCreated.UTC().Format(time.RFC3339),
		m.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("insert uptime monitor: %w", err)
	}
	return m, nil
}

// GetUptimeMonitor returns a single uptime monitor by ID.
func (s *UptimeMonitorStore) GetUptimeMonitor(ctx context.Context, id string) (*UptimeMonitor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, name, url, interval_seconds, timeout_seconds, expected_status,
		        COALESCE(environment, ''), status,
		        COALESCE(last_check_at, ''), last_status_code, COALESCE(last_error, ''),
		        last_latency_ms, consecutive_fail, created_at, updated_at
		 FROM uptime_monitors WHERE id = ?`, id,
	)
	return scanUptimeMonitor(row)
}

// ListUptimeMonitors returns uptime monitors for a project.
func (s *UptimeMonitorStore) ListUptimeMonitors(ctx context.Context, projectID string, limit int) ([]UptimeMonitor, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, url, interval_seconds, timeout_seconds, expected_status,
		        COALESCE(environment, ''), status,
		        COALESCE(last_check_at, ''), last_status_code, COALESCE(last_error, ''),
		        last_latency_ms, consecutive_fail, created_at, updated_at
		 FROM uptime_monitors
		 WHERE project_id = ?
		 ORDER BY updated_at DESC
		 LIMIT ?`, projectID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list uptime monitors: %w", err)
	}
	defer rows.Close()
	var out []UptimeMonitor
	for rows.Next() {
		m, err := scanUptimeMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ListDueUptimeMonitors returns active uptime monitors that are due for a check.
func (s *UptimeMonitorStore) ListDueUptimeMonitors(ctx context.Context, now time.Time) ([]UptimeMonitor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, url, interval_seconds, timeout_seconds, expected_status,
		        COALESCE(environment, ''), status,
		        COALESCE(last_check_at, ''), last_status_code, COALESCE(last_error, ''),
		        last_latency_ms, consecutive_fail, created_at, updated_at
		 FROM uptime_monitors
		 WHERE status = 'active'
		   AND (last_check_at IS NULL OR last_check_at = ''
		        OR datetime(last_check_at, '+' || interval_seconds || ' seconds') <= ?)
		 ORDER BY COALESCE(last_check_at, '') ASC`,
		now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("list due uptime monitors: %w", err)
	}
	defer rows.Close()
	var out []UptimeMonitor
	for rows.Next() {
		m, err := scanUptimeMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// DeleteUptimeMonitor removes an uptime monitor and its check results.
func (s *UptimeMonitorStore) DeleteUptimeMonitor(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM uptime_check_results WHERE uptime_monitor_id = ?`, id); err != nil {
		return fmt.Errorf("delete uptime check results: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM uptime_monitors WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete uptime monitor: %w", err)
	}
	return nil
}

// SaveCheckResult stores a check result and updates the parent monitor state.
func (s *UptimeMonitorStore) SaveCheckResult(ctx context.Context, result *UptimeCheckResult) error {
	if result == nil {
		return nil
	}
	if result.ID == "" {
		result.ID = generateID()
	}
	if result.DateCreated.IsZero() {
		result.DateCreated = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO uptime_check_results
			(id, uptime_monitor_id, project_id, status_code, latency_ms, error, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		result.ID,
		result.UptimeMonitorID,
		result.ProjectID,
		result.StatusCode,
		result.LatencyMS,
		nullIfEmpty(result.Error),
		result.Status,
		result.DateCreated.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("insert uptime check result: %w", err)
	}

	consecutiveSQL := "consecutive_fail + 1"
	if result.Status == "ok" {
		consecutiveSQL = "0"
	}

	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx,
		`UPDATE uptime_monitors SET
			last_check_at = ?,
			last_status_code = ?,
			last_error = ?,
			last_latency_ms = ?,
			consecutive_fail = `+consecutiveSQL+`,
			updated_at = ?
		 WHERE id = ?`,
		now.Format(time.RFC3339),
		result.StatusCode,
		nullIfEmpty(result.Error),
		result.LatencyMS,
		now.Format(time.RFC3339),
		result.UptimeMonitorID,
	); err != nil {
		return fmt.Errorf("update uptime monitor state: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ListCheckResults returns recent check results for an uptime monitor.
func (s *UptimeMonitorStore) ListCheckResults(ctx context.Context, uptimeMonitorID string, limit int) ([]UptimeCheckResult, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, uptime_monitor_id, project_id, status_code, latency_ms,
		        COALESCE(error, ''), status, created_at
		 FROM uptime_check_results
		 WHERE uptime_monitor_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`, uptimeMonitorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list uptime check results: %w", err)
	}
	defer rows.Close()
	var out []UptimeCheckResult
	for rows.Next() {
		var r UptimeCheckResult
		var createdAt string
		if err := rows.Scan(
			&r.ID, &r.UptimeMonitorID, &r.ProjectID, &r.StatusCode, &r.LatencyMS,
			&r.Error, &r.Status, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan uptime check result: %w", err)
		}
		r.DateCreated = parseTime(createdAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

type uptimeScanner interface {
	Scan(dest ...any) error
}

func scanUptimeMonitor(s uptimeScanner) (*UptimeMonitor, error) {
	var m UptimeMonitor
	var lastCheckAt, createdAt, updatedAt string
	if err := s.Scan(
		&m.ID, &m.ProjectID, &m.Name, &m.URL,
		&m.IntervalSeconds, &m.TimeoutSeconds, &m.ExpectedStatus,
		&m.Environment, &m.Status,
		&lastCheckAt, &m.LastStatusCode, &m.LastError,
		&m.LastLatencyMS, &m.ConsecutiveFail, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan uptime monitor: %w", err)
	}
	m.LastCheckAt = parseTime(lastCheckAt)
	m.DateCreated = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
	return &m, nil
}

// UptimeMonitorJSON returns a JSON representation for storing as check-in payload.
func UptimeMonitorJSON(m *UptimeMonitor, result *UptimeCheckResult) json.RawMessage {
	payload := map[string]any{
		"monitor_type": "uptime",
		"url":          m.URL,
		"status_code":  result.StatusCode,
		"latency_ms":   result.LatencyMS,
	}
	if result.Error != "" {
		payload["error"] = result.Error
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}
