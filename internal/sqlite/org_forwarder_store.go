package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// OrgForwarderStore is a SQLite-backed implementation of store.OrgForwarderStore.
type OrgForwarderStore struct {
	db *sql.DB
}

// NewOrgForwarderStore creates an OrgForwarderStore backed by the given database.
func NewOrgForwarderStore(db *sql.DB) *OrgForwarderStore {
	return &OrgForwarderStore{db: db}
}

// CreateOrgForwarder inserts a new org-level data forwarder row.
func (s *OrgForwarderStore) CreateOrgForwarder(ctx context.Context, f *store.OrgDataForwarder) error {
	if f.ID == "" {
		f.ID = generateID()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	if f.CredentialsJSON == "" {
		f.CredentialsJSON = "{}"
	}
	enabled := 0
	if f.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_data_forwarders (id, org_id, type, name, url, credentials_json, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.OrgID, f.Type, f.Name, f.URL, f.CredentialsJSON, enabled,
		f.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListOrgForwarders returns all org-level data forwarders for the given organization.
func (s *OrgForwarderStore) ListOrgForwarders(ctx context.Context, orgID string) ([]*store.OrgDataForwarder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, type, name, url, credentials_json, enabled, created_at
		 FROM org_data_forwarders WHERE org_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.OrgDataForwarder
	for rows.Next() {
		f, err := scanOrgForwarderRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateOrgForwarder updates an org-level data forwarder row by ID.
func (s *OrgForwarderStore) UpdateOrgForwarder(ctx context.Context, f *store.OrgDataForwarder) error {
	enabled := 0
	if f.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_data_forwarders SET type = ?, name = ?, url = ?, credentials_json = ?, enabled = ?
		 WHERE id = ?`,
		f.Type, f.Name, f.URL, f.CredentialsJSON, enabled, f.ID)
	return err
}

// DeleteOrgForwarder removes an org-level data forwarder row by ID.
func (s *OrgForwarderStore) DeleteOrgForwarder(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM org_data_forwarders WHERE id = ?`, id)
	return err
}

func scanOrgForwarderRow(rows *sql.Rows) (*store.OrgDataForwarder, error) {
	var f store.OrgDataForwarder
	var createdAt string
	var enabled int
	err := rows.Scan(&f.ID, &f.OrgID, &f.Type, &f.Name, &f.URL, &f.CredentialsJSON, &enabled, &createdAt)
	if err != nil {
		return nil, err
	}
	f.Enabled = enabled != 0
	f.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &f, nil
}
