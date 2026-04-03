package sqlite //nolint:dupl

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/store"
)

// ExternalTeamStore is a SQLite-backed implementation of store.ExternalTeamStore.
type ExternalTeamStore struct {
	db *sql.DB
}

// NewExternalTeamStore creates an ExternalTeamStore backed by the given database.
func NewExternalTeamStore(db *sql.DB) *ExternalTeamStore {
	return &ExternalTeamStore{db: db}
}

// CreateExternalTeam inserts a new external team mapping row.
func (s *ExternalTeamStore) CreateExternalTeam(ctx context.Context, t *store.ExternalTeam) error {
	if t.ID == "" {
		t.ID = generateID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO external_teams (id, org_id, team_slug, provider, external_id, external_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.OrgID, t.TeamSlug, t.Provider, t.ExternalID, t.ExternalName,
		t.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// UpdateExternalTeam updates an external team mapping row by ID.
func (s *ExternalTeamStore) UpdateExternalTeam(ctx context.Context, t *store.ExternalTeam) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE external_teams SET team_slug = ?, provider = ?, external_id = ?, external_name = ?
		 WHERE id = ?`,
		t.TeamSlug, t.Provider, t.ExternalID, t.ExternalName, t.ID)
	return err
}

// DeleteExternalTeam removes an external team mapping row by ID.
func (s *ExternalTeamStore) DeleteExternalTeam(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM external_teams WHERE id = ?`, id)
	return err
}

// ListExternalTeams returns all external team mappings for the given organization.
func (s *ExternalTeamStore) ListExternalTeams(ctx context.Context, orgID string) ([]*store.ExternalTeam, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, team_slug, provider, external_id, external_name, created_at
		 FROM external_teams WHERE org_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*store.ExternalTeam
	for rows.Next() {
		var t store.ExternalTeam
		var createdAt string
		if err := rows.Scan(&t.ID, &t.OrgID, &t.TeamSlug, &t.Provider, &t.ExternalID, &t.ExternalName, &createdAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &t)
	}
	return out, rows.Err()
}
