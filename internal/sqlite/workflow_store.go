package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// WorkflowStore is a SQLite-backed implementation of store.WorkflowStore.
type WorkflowStore struct {
	db *sql.DB
}

// NewWorkflowStore creates a WorkflowStore backed by the given database.
func NewWorkflowStore(db *sql.DB) *WorkflowStore {
	return &WorkflowStore{db: db}
}

// CreateWorkflow inserts a new workflow row.
func (s *WorkflowStore) CreateWorkflow(ctx context.Context, w *store.Workflow) error {
	if w.ID == "" {
		w.ID = generateID()
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	if w.TriggersJSON == "" {
		w.TriggersJSON = "[]"
	}
	if w.ConditionsJSON == "" {
		w.ConditionsJSON = "[]"
	}
	if w.ActionsJSON == "" {
		w.ActionsJSON = "[]"
	}
	enabled := 0
	if w.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO workflows (id, org_id, name, triggers_json, conditions_json, actions_json, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.OrgID, w.Name, w.TriggersJSON, w.ConditionsJSON, w.ActionsJSON, enabled,
		w.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListWorkflows returns all workflows for the given organization.
func (s *WorkflowStore) ListWorkflows(ctx context.Context, orgID string) ([]*store.Workflow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, name, triggers_json, conditions_json, actions_json, enabled, created_at
		 FROM workflows WHERE org_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.Workflow
	for rows.Next() {
		w, err := scanWorkflowRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWorkflow returns a single workflow by ID.
func (s *WorkflowStore) GetWorkflow(ctx context.Context, id string) (*store.Workflow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, name, triggers_json, conditions_json, actions_json, enabled, created_at
		 FROM workflows WHERE id = ?`, id)
	var w store.Workflow
	var createdAt string
	var enabled int
	err := row.Scan(&w.ID, &w.OrgID, &w.Name, &w.TriggersJSON, &w.ConditionsJSON, &w.ActionsJSON, &enabled, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w.Enabled = enabled != 0
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &w, nil
}

// UpdateWorkflow updates a workflow row by ID.
func (s *WorkflowStore) UpdateWorkflow(ctx context.Context, w *store.Workflow) error {
	enabled := 0
	if w.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE workflows SET name = ?, triggers_json = ?, conditions_json = ?, actions_json = ?, enabled = ?
		 WHERE id = ?`,
		w.Name, w.TriggersJSON, w.ConditionsJSON, w.ActionsJSON, enabled, w.ID)
	return err
}

// DeleteWorkflow removes a workflow row by ID.
func (s *WorkflowStore) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	return err
}

// BulkUpdateWorkflows updates the enabled state of multiple workflows by ID within an org.
func (s *WorkflowStore) BulkUpdateWorkflows(ctx context.Context, orgID string, ids []string, enabled bool) error {
	if len(ids) == 0 {
		return nil
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET enabled = ? WHERE id = ? AND org_id = ?`,
			enabledInt, id, orgID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// BulkDeleteWorkflows deletes multiple workflows by ID within an org.
func (s *WorkflowStore) BulkDeleteWorkflows(ctx context.Context, orgID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM workflows WHERE id = ? AND org_id = ?`,
			id, orgID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func scanWorkflowRow(rows *sql.Rows) (*store.Workflow, error) {
	var w store.Workflow
	var createdAt string
	var enabled int
	err := rows.Scan(&w.ID, &w.OrgID, &w.Name, &w.TriggersJSON, &w.ConditionsJSON, &w.ActionsJSON, &enabled, &createdAt)
	if err != nil {
		return nil, err
	}
	w.Enabled = enabled != 0
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &w, nil
}
