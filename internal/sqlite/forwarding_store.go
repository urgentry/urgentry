package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// ForwardingConfigStore is a SQLite-backed implementation of
// store.ForwardingStore.
type ForwardingConfigStore struct {
	db *sql.DB
}

// NewForwardingConfigStore creates a ForwardingConfigStore backed by the
// given database.
func NewForwardingConfigStore(db *sql.DB) *ForwardingConfigStore {
	return &ForwardingConfigStore{db: db}
}

// CreateForwarding inserts a new data forwarding configuration row.
func (s *ForwardingConfigStore) CreateForwarding(ctx context.Context, cfg *store.ForwardingConfig) error {
	if cfg.ID == "" {
		cfg.ID = generateID()
	}
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = time.Now().UTC()
	}
	if cfg.Status == "" {
		cfg.Status = "active"
	}
	if cfg.Type == "" {
		cfg.Type = "webhook"
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO data_forwarding_configs
			(id, project_id, type, url, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		cfg.ID, cfg.ProjectID, cfg.Type, cfg.URL, cfg.Status,
		cfg.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListForwardingByProject returns all forwarding configs for the given project.
func (s *ForwardingConfigStore) ListForwardingByProject(ctx context.Context, projectID string) ([]*store.ForwardingConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, type, url, status, created_at
		 FROM data_forwarding_configs
		 WHERE project_id = ?
		 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.ForwardingConfig
	for rows.Next() {
		cfg, err := scanForwardingConfigRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// DeleteForwarding removes a forwarding config row by ID.
func (s *ForwardingConfigStore) DeleteForwarding(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM data_forwarding_configs WHERE id = ?`, id)
	return err
}

func scanForwardingConfigRow(rows *sql.Rows) (*store.ForwardingConfig, error) {
	var cfg store.ForwardingConfig
	var createdAt string
	err := rows.Scan(
		&cfg.ID, &cfg.ProjectID, &cfg.Type, &cfg.URL, &cfg.Status, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	cfg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &cfg, nil
}
