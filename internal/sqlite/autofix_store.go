package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AutofixStore persists experimental issue-autofix runs in SQLite so Tiny and
// serious self-hosted mode share the same read model.
type AutofixStore struct {
	db *sql.DB
}

type AutofixRun struct {
	RunID          int64
	OrganizationID string
	ProjectID      string
	IssueID        string
	Status         string
	EventID        string
	StoppingPoint  string
	Payload        map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewAutofixStore creates an issue-autofix store backed by SQLite.
func NewAutofixStore(db *sql.DB) *AutofixStore {
	return &AutofixStore{db: db}
}

// CreateRun stores a new autofix run and returns its numeric run ID.
func (s *AutofixStore) CreateRun(ctx context.Context, run *AutofixRun) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("autofix store is not configured")
	}
	if run == nil {
		return 0, errors.New("autofix run is nil")
	}
	if run.OrganizationID == "" || run.ProjectID == "" || run.IssueID == "" {
		return 0, errors.New("autofix run organization_id, project_id, and issue_id are required")
	}
	if run.Status == "" {
		run.Status = "COMPLETED"
	}
	if run.StoppingPoint == "" {
		run.StoppingPoint = "root_cause"
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}
	payloadJSON, err := json.Marshal(run.Payload)
	if err != nil {
		return 0, fmt.Errorf("encode autofix payload: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO issue_autofix_runs (
			organization_id, project_id, issue_id, status, event_id, stopping_point, payload_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.OrganizationID,
		run.ProjectID,
		run.IssueID,
		run.Status,
		nullIfEmpty(run.EventID),
		run.StoppingPoint,
		string(payloadJSON),
		run.CreatedAt.UTC().Format(time.RFC3339),
		run.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("insert autofix run: %w", err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read autofix run id: %w", err)
	}
	run.RunID = runID
	return runID, nil
}

// GetLatestRun loads the most recent autofix run for one issue.
func (s *AutofixStore) GetLatestRun(ctx context.Context, issueID string) (*AutofixRun, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("autofix store is not configured")
	}
	var (
		run         AutofixRun
		eventID     sql.NullString
		payloadJSON string
		createdAt   string
		updatedAt   string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT run_id, organization_id, project_id, issue_id, status, event_id, stopping_point, payload_json, created_at, updated_at
		FROM issue_autofix_runs
		WHERE issue_id = ?
		ORDER BY run_id DESC
		LIMIT 1`,
		issueID,
	).Scan(
		&run.RunID,
		&run.OrganizationID,
		&run.ProjectID,
		&run.IssueID,
		&run.Status,
		&eventID,
		&run.StoppingPoint,
		&payloadJSON,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load autofix run: %w", err)
	}
	run.EventID = nullStr(eventID)
	run.CreatedAt = parseTime(createdAt)
	run.UpdatedAt = parseTime(updatedAt)
	if payloadJSON != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &run.Payload); err != nil {
			return nil, fmt.Errorf("decode autofix payload: %w", err)
		}
	}
	if run.Payload == nil {
		run.Payload = map[string]any{}
	}
	if _, ok := run.Payload["run_id"]; !ok {
		run.Payload["run_id"] = run.RunID
	}
	if _, ok := run.Payload["status"]; !ok {
		run.Payload["status"] = run.Status
	}
	return &run, nil
}
