package postgrescontrol

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/integration"
)

var _ integration.ExternalIssueStore = (*ExternalIssueStore)(nil)

// ExternalIssueStore persists installation-backed issue links in PostgreSQL.
type ExternalIssueStore struct {
	db *sql.DB
}

// NewExternalIssueStore creates a Postgres-backed external issue store.
func NewExternalIssueStore(db *sql.DB) *ExternalIssueStore {
	return &ExternalIssueStore{db: db}
}

func (s *ExternalIssueStore) GetByInstallation(ctx context.Context, installationID, externalIssueID string) (*integration.ExternalIssueLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, installation_id, group_id, integration_id, key, title, url, description, created_at
		  FROM group_external_issues
		 WHERE installation_id = $1 AND id = $2`,
		strings.TrimSpace(installationID), strings.TrimSpace(externalIssueID),
	)
	return scanExternalIssueLink(row)
}

func (s *ExternalIssueStore) ListByGroup(ctx context.Context, groupID string) ([]*integration.ExternalIssueLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, installation_id, group_id, integration_id, key, title, url, description, created_at
		  FROM group_external_issues
		 WHERE group_id = $1
		 ORDER BY created_at DESC`,
		strings.TrimSpace(groupID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*integration.ExternalIssueLink
	for rows.Next() {
		item, err := scanExternalIssueLinkRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ExternalIssueStore) Upsert(ctx context.Context, link *integration.ExternalIssueLink) error {
	if link == nil {
		return nil
	}
	if link.ID == "" {
		link.ID = generateID()
	}
	now := time.Now().UTC()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	var createdAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO group_external_issues
			(id, installation_id, group_id, integration_id, key, title, url, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (installation_id, group_id, key) DO UPDATE SET
			integration_id = excluded.integration_id,
			title = excluded.title,
			url = excluded.url,
			description = excluded.description
		RETURNING id, created_at`,
		link.ID,
		strings.TrimSpace(link.InstallationID),
		strings.TrimSpace(link.GroupID),
		strings.TrimSpace(link.IntegrationID),
		strings.TrimSpace(link.Key),
		strings.TrimSpace(link.Title),
		strings.TrimSpace(link.URL),
		strings.TrimSpace(link.Description),
		link.CreatedAt.UTC(),
	).Scan(&link.ID, &createdAt)
	if err != nil {
		return err
	}
	link.CreatedAt = nullTime(createdAt)
	return nil
}

func (s *ExternalIssueStore) Delete(ctx context.Context, installationID, externalIssueID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM group_external_issues
		 WHERE installation_id = $1 AND id = $2`,
		strings.TrimSpace(installationID), strings.TrimSpace(externalIssueID),
	)
	return err
}

func scanExternalIssueLink(row *sql.Row) (*integration.ExternalIssueLink, error) {
	var item integration.ExternalIssueLink
	var createdAt sql.NullTime
	if err := row.Scan(
		&item.ID,
		&item.InstallationID,
		&item.GroupID,
		&item.IntegrationID,
		&item.Key,
		&item.Title,
		&item.URL,
		&item.Description,
		&createdAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.CreatedAt = nullTime(createdAt)
	return &item, nil
}

func scanExternalIssueLinkRow(rows *sql.Rows) (*integration.ExternalIssueLink, error) {
	var item integration.ExternalIssueLink
	var createdAt sql.NullTime
	if err := rows.Scan(
		&item.ID,
		&item.InstallationID,
		&item.GroupID,
		&item.IntegrationID,
		&item.Key,
		&item.Title,
		&item.URL,
		&item.Description,
		&createdAt,
	); err != nil {
		return nil, err
	}
	item.CreatedAt = nullTime(createdAt)
	return &item, nil
}
