package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/integration"
)

// IntegrationConfigStore is a SQLite-backed implementation of
// integration.Store.
type IntegrationConfigStore struct {
	db *sql.DB
}

// NewIntegrationConfigStore creates an IntegrationConfigStore backed by the
// given database.
func NewIntegrationConfigStore(db *sql.DB) *IntegrationConfigStore {
	return &IntegrationConfigStore{db: db}
}

// Create inserts a new integration configuration row.
func (s *IntegrationConfigStore) Create(ctx context.Context, cfg *integration.IntegrationConfig) error {
	if cfg.ID == "" {
		cfg.ID = generateID()
	}
	now := time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if cfg.Status == "" {
		cfg.Status = "active"
	}

	configJSON, err := json.Marshal(cfg.Config)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO integration_configs
			(id, organization_id, integration_id, project_id, config_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.ID, cfg.OrganizationID, cfg.IntegrationID, cfg.ProjectID,
		string(configJSON), cfg.Status,
		cfg.CreatedAt.UTC().Format(time.RFC3339),
		cfg.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// Get retrieves an integration configuration by its row ID.
func (s *IntegrationConfigStore) Get(ctx context.Context, id string) (*integration.IntegrationConfig, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, organization_id, integration_id, project_id, config_json, status, created_at, updated_at
		 FROM integration_configs WHERE id = ?`, id)
	return scanIntegrationConfig(row)
}

// ListByOrganization returns all integration configs for the given org.
func (s *IntegrationConfigStore) ListByOrganization(ctx context.Context, orgID string) ([]*integration.IntegrationConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, organization_id, integration_id, project_id, config_json, status, created_at, updated_at
		 FROM integration_configs
		 WHERE organization_id = ?
		 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*integration.IntegrationConfig
	for rows.Next() {
		cfg, err := scanIntegrationConfigRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// Delete removes an integration config row by ID.
func (s *IntegrationConfigStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM integration_configs WHERE id = ?`, id)
	return err
}

func scanIntegrationConfig(row *sql.Row) (*integration.IntegrationConfig, error) {
	var cfg integration.IntegrationConfig
	var configJSON, createdAt, updatedAt string
	err := row.Scan(
		&cfg.ID, &cfg.OrganizationID, &cfg.IntegrationID, &cfg.ProjectID,
		&configJSON, &cfg.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg.Config)
	}
	if cfg.Config == nil {
		cfg.Config = make(map[string]string)
	}
	cfg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	cfg.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &cfg, nil
}

func scanIntegrationConfigRow(rows *sql.Rows) (*integration.IntegrationConfig, error) {
	var cfg integration.IntegrationConfig
	var configJSON, createdAt, updatedAt string
	err := rows.Scan(
		&cfg.ID, &cfg.OrganizationID, &cfg.IntegrationID, &cfg.ProjectID,
		&configJSON, &cfg.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &cfg.Config)
	}
	if cfg.Config == nil {
		cfg.Config = make(map[string]string)
	}
	cfg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	cfg.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &cfg, nil
}
