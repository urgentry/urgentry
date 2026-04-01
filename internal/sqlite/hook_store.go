package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/pkg/id"
)

// ServiceHook represents a project webhook subscription.
type ServiceHook struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"dateCreated"`
	UpdatedAt time.Time `json:"dateUpdated"`
}

// HookStore persists project service hooks in SQLite.
type HookStore struct {
	db *sql.DB
}

// NewHookStore creates a HookStore backed by the given database.
func NewHookStore(db *sql.DB) *HookStore {
	return &HookStore{db: db}
}

// List returns all hooks for a project.
func (s *HookStore) List(ctx context.Context, projectID string) ([]ServiceHook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, url, events_json, status, created_at, updated_at
		 FROM project_hooks WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list hooks: %w", err)
	}
	defer rows.Close()

	var hooks []ServiceHook
	for rows.Next() {
		h, err := scanHookRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan hook: %w", err)
		}
		hooks = append(hooks, h)
	}
	return hooks, rows.Err()
}

// Get returns a single hook by ID.
func (s *HookStore) Get(ctx context.Context, hookID string) (*ServiceHook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, url, events_json, status, created_at, updated_at
		 FROM project_hooks WHERE id = ?`, hookID)
	if err != nil {
		return nil, fmt.Errorf("get hook: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	h, err := scanHookRow(rows)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// Create inserts a new hook.
func (s *HookStore) Create(ctx context.Context, h *ServiceHook) error {
	if h.ID == "" {
		h.ID = id.New()
	}
	now := time.Now().UTC()
	if h.CreatedAt.IsZero() {
		h.CreatedAt = now
	}
	h.UpdatedAt = now
	if h.Status == "" {
		h.Status = "active"
	}
	if h.Events == nil {
		h.Events = []string{}
	}

	eventsJSON, err := json.Marshal(h.Events)
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO project_hooks (id, project_id, url, events_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.ProjectID, h.URL, string(eventsJSON), h.Status,
		h.CreatedAt.Format(time.RFC3339), h.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert hook: %w", err)
	}
	return nil
}

// Update modifies an existing hook.
func (s *HookStore) Update(ctx context.Context, h *ServiceHook) error {
	h.UpdatedAt = time.Now().UTC()

	eventsJSON, err := json.Marshal(h.Events)
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE project_hooks SET url = ?, events_json = ?, status = ?, updated_at = ?
		 WHERE id = ?`,
		h.URL, string(eventsJSON), h.Status, h.UpdatedAt.Format(time.RFC3339), h.ID,
	)
	if err != nil {
		return fmt.Errorf("update hook: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("hook not found: %s", h.ID)
	}
	return nil
}

// Delete removes a hook by ID.
func (s *HookStore) Delete(ctx context.Context, hookID string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM project_hooks WHERE id = ?`, hookID)
	if err != nil {
		return fmt.Errorf("delete hook: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("hook not found: %s", hookID)
	}
	return nil
}

func scanHookRow(rows *sql.Rows) (ServiceHook, error) {
	var h ServiceHook
	var eventsJSON, createdAt, updatedAt sql.NullString
	if err := rows.Scan(&h.ID, &h.ProjectID, &h.URL, &eventsJSON, &h.Status, &createdAt, &updatedAt); err != nil {
		return h, err
	}
	if eventsJSON.Valid && eventsJSON.String != "" {
		_ = json.Unmarshal([]byte(eventsJSON.String), &h.Events)
	}
	if h.Events == nil {
		h.Events = []string{}
	}
	h.CreatedAt = parseTime(nullStr(createdAt))
	h.UpdatedAt = parseTime(nullStr(updatedAt))
	return h, nil
}
