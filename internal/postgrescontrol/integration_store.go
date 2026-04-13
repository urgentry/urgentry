package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"urgentry/internal/integration"
)

var _ integration.Store = (*IntegrationConfigStore)(nil)

// IntegrationConfigStore persists integration installations in PostgreSQL.
type IntegrationConfigStore struct {
	db *sql.DB
}

// NewIntegrationConfigStore creates a Postgres-backed integration installation store.
func NewIntegrationConfigStore(db *sql.DB) *IntegrationConfigStore {
	return &IntegrationConfigStore{db: db}
}

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
	configJSON, err := marshalJSON(cfg.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO integration_configs
			(id, organization_id, integration_id, project_id, config_json, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`,
		cfg.ID,
		cfg.OrganizationID,
		cfg.IntegrationID,
		nullIfEmpty(cfg.ProjectID),
		string(configJSON),
		cfg.Status,
		cfg.CreatedAt.UTC(),
		cfg.UpdatedAt.UTC(),
	)
	return err
}

func (s *IntegrationConfigStore) Get(ctx context.Context, id string) (*integration.IntegrationConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, organization_id, integration_id, COALESCE(project_id, ''), config_json::text, status, created_at, updated_at
		  FROM integration_configs
		 WHERE id = $1`,
		id,
	)
	return scanIntegrationConfig(row)
}

func (s *IntegrationConfigStore) ListByOrganization(ctx context.Context, orgID string) ([]*integration.IntegrationConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, organization_id, integration_id, COALESCE(project_id, ''), config_json::text, status, created_at, updated_at
		  FROM integration_configs
		 WHERE organization_id = $1
		 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*integration.IntegrationConfig
	for rows.Next() {
		item, err := scanIntegrationConfigRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *IntegrationConfigStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM integration_configs WHERE id = $1`, id)
	return err
}

func scanIntegrationConfig(row *sql.Row) (*integration.IntegrationConfig, error) {
	var (
		item       integration.IntegrationConfig
		configJSON string
		createdAt  sql.NullTime
		updatedAt  sql.NullTime
	)
	err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.IntegrationID,
		&item.ProjectID,
		&configJSON,
		&item.Status,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if strings := configJSON; strings != "" {
		_ = json.Unmarshal([]byte(strings), &item.Config)
	}
	if item.Config == nil {
		item.Config = map[string]string{}
	}
	item.CreatedAt = nullTime(createdAt)
	item.UpdatedAt = nullTime(updatedAt)
	return &item, nil
}

func scanIntegrationConfigRow(rows *sql.Rows) (*integration.IntegrationConfig, error) {
	var (
		item       integration.IntegrationConfig
		configJSON string
		createdAt  sql.NullTime
		updatedAt  sql.NullTime
	)
	if err := rows.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.IntegrationID,
		&item.ProjectID,
		&configJSON,
		&item.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	if configJSON != "" {
		_ = json.Unmarshal([]byte(configJSON), &item.Config)
	}
	if item.Config == nil {
		item.Config = map[string]string{}
	}
	item.CreatedAt = nullTime(createdAt)
	item.UpdatedAt = nullTime(updatedAt)
	return &item, nil
}
