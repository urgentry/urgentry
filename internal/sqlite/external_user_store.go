package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// ExternalUserStore is a SQLite-backed implementation of store.ExternalUserStore.
type ExternalUserStore struct {
	db *sql.DB
}

// NewExternalUserStore creates an ExternalUserStore backed by the given database.
func NewExternalUserStore(db *sql.DB) *ExternalUserStore {
	return &ExternalUserStore{db: db}
}

// CreateExternalUser inserts a new external user mapping row.
func (s *ExternalUserStore) CreateExternalUser(ctx context.Context, u *store.ExternalUser) error {
	if u.ID == "" {
		u.ID = generateID()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO external_users (id, org_id, user_id, provider, external_id, external_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.OrgID, u.UserID, u.Provider, u.ExternalID, u.ExternalName,
		u.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// UpdateExternalUser updates an external user mapping row by ID.
func (s *ExternalUserStore) UpdateExternalUser(ctx context.Context, u *store.ExternalUser) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE external_users SET user_id = ?, provider = ?, external_id = ?, external_name = ?
		 WHERE id = ?`,
		u.UserID, u.Provider, u.ExternalID, u.ExternalName, u.ID)
	return err
}

// DeleteExternalUser removes an external user mapping row by ID.
func (s *ExternalUserStore) DeleteExternalUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM external_users WHERE id = ?`, id)
	return err
}

// ListExternalUsers returns all external user mappings for the given organization.
func (s *ExternalUserStore) ListExternalUsers(ctx context.Context, orgID string) ([]*store.ExternalUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, user_id, provider, external_id, external_name, created_at
		 FROM external_users WHERE org_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.ExternalUser
	for rows.Next() {
		var u store.ExternalUser
		var createdAt string
		if err := rows.Scan(&u.ID, &u.OrgID, &u.UserID, &u.Provider, &u.ExternalID, &u.ExternalName, &createdAt); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &u)
	}
	return out, rows.Err()
}
