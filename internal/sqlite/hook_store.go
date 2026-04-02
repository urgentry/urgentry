package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/outboundhttp"
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
	db         *sql.DB
	HTTPClient *http.Client
}

// NewHookStore creates a HookStore backed by the given database.
func NewHookStore(db *sql.DB) *HookStore {
	return &HookStore{db: db}
}

// FireHooks POSTs the given payload to all active project hooks subscribed to
// the provided action. Delivery errors are collected and returned together.
func (s *HookStore) FireHooks(ctx context.Context, projectID, action string, payload any) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return nil
	}
	hooks, err := s.List(ctx, projectID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal hook payload: %w", err)
	}
	var errs []error
	for _, hook := range hooks {
		if strings.TrimSpace(hook.Status) != "" && hook.Status != "active" {
			continue
		}
		if !hookWantsEvent(hook, action) {
			continue
		}
		if err := s.postHook(ctx, hook.URL, body); err != nil {
			errs = append(errs, fmt.Errorf("fire hook %s: %w", hook.ID, err))
		}
	}
	return errors.Join(errs...)
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

func (s *HookStore) postHook(ctx context.Context, url string, body []byte) error {
	if _, err := outboundhttp.ValidateTargetURL(url); err != nil {
		return fmt.Errorf("invalid hook target: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "urgentry-service-hook/1.0")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("post hook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("hook returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *HookStore) httpClient() *http.Client {
	if s != nil && s.HTTPClient != nil {
		return s.HTTPClient
	}
	return outboundhttp.NewClient(10*time.Second, nil)
}

func hookWantsEvent(hook ServiceHook, action string) bool {
	if len(hook.Events) == 0 {
		return true
	}
	for _, item := range hook.Events {
		if strings.TrimSpace(item) == action {
			return true
		}
	}
	return false
}
