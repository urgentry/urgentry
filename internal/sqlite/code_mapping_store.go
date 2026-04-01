package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// CodeMappingStore is a SQLite-backed implementation of store.CodeMappingStore.
type CodeMappingStore struct {
	db *sql.DB
}

// NewCodeMappingStore creates a CodeMappingStore backed by the given database.
func NewCodeMappingStore(db *sql.DB) *CodeMappingStore {
	return &CodeMappingStore{db: db}
}

// CreateCodeMapping inserts a new code mapping row.
func (s *CodeMappingStore) CreateCodeMapping(ctx context.Context, m *store.CodeMapping) error {
	if m.ID == "" {
		m.ID = generateID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO code_mappings (id, project_id, stack_root, source_root, default_branch, repo_url, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ProjectID, m.StackRoot, m.SourceRoot, m.DefaultBranch, m.RepoURL,
		m.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListCodeMappings returns all code mappings for the given project.
func (s *CodeMappingStore) ListCodeMappings(ctx context.Context, projectID string) ([]*store.CodeMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, stack_root, source_root, default_branch, repo_url, created_at
		 FROM code_mappings
		 WHERE project_id = ?
		 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.CodeMapping
	for rows.Next() {
		var m store.CodeMapping
		var createdAt string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.StackRoot, &m.SourceRoot, &m.DefaultBranch, &m.RepoURL, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// DeleteCodeMapping removes a code mapping row by project and ID.
func (s *CodeMappingStore) DeleteCodeMapping(ctx context.Context, projectID, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM code_mappings WHERE project_id = ? AND id = ?`, projectID, id)
	return err
}
