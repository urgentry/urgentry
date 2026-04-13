package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/integration"
)

var _ integration.ExternalIssueStore = (*ExternalIssueStore)(nil)

// ExternalIssueStore persists installation-backed external issue links in SQLite.
type ExternalIssueStore struct {
	db *sql.DB
}

// NewExternalIssueStore creates a SQLite-backed external issue store.
func NewExternalIssueStore(db *sql.DB) *ExternalIssueStore {
	return &ExternalIssueStore{db: db}
}

func (s *ExternalIssueStore) GetByInstallation(ctx context.Context, installationID, externalIssueID string) (*integration.ExternalIssueLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(installation_id, ''), group_id, integration_id, key, title, url, description, created_at
		  FROM group_external_issues
		 WHERE installation_id = ? AND id = ?`,
		strings.TrimSpace(installationID), strings.TrimSpace(externalIssueID),
	)
	return scanExternalIssueLink(row)
}

func (s *ExternalIssueStore) ListByGroup(ctx context.Context, groupID string) ([]*integration.ExternalIssueLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(installation_id, ''), group_id, integration_id, key, title, url, description, created_at
		  FROM group_external_issues
		 WHERE group_id = ?
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
	now := time.Now().UTC()
	if link.ID == "" {
		existing, err := s.findExisting(ctx, link.InstallationID, link.GroupID, link.Key)
		if err != nil {
			return err
		}
		if existing != nil {
			link.ID = existing.ID
			link.CreatedAt = existing.CreatedAt
		} else {
			link.ID = generateID()
		}
	}
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO group_external_issues
			(id, installation_id, group_id, integration_id, key, title, url, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			installation_id = excluded.installation_id,
			group_id = excluded.group_id,
			integration_id = excluded.integration_id,
			key = excluded.key,
			title = excluded.title,
			url = excluded.url,
			description = excluded.description`,
		link.ID,
		strings.TrimSpace(link.InstallationID),
		strings.TrimSpace(link.GroupID),
		strings.TrimSpace(link.IntegrationID),
		strings.TrimSpace(link.Key),
		strings.TrimSpace(link.Title),
		strings.TrimSpace(link.URL),
		strings.TrimSpace(link.Description),
		link.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *ExternalIssueStore) Delete(ctx context.Context, installationID, externalIssueID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM group_external_issues
		 WHERE installation_id = ? AND id = ?`,
		strings.TrimSpace(installationID), strings.TrimSpace(externalIssueID),
	)
	return err
}

func (s *ExternalIssueStore) findExisting(ctx context.Context, installationID, groupID, key string) (*integration.ExternalIssueLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(installation_id, ''), group_id, integration_id, key, title, url, description, created_at
		  FROM group_external_issues
		 WHERE installation_id = ? AND group_id = ? AND key = ?
		 LIMIT 1`,
		strings.TrimSpace(installationID), strings.TrimSpace(groupID), strings.TrimSpace(key),
	)
	return scanExternalIssueLink(row)
}

func scanExternalIssueLink(row *sql.Row) (*integration.ExternalIssueLink, error) {
	var item integration.ExternalIssueLink
	var createdAt sql.NullString
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
	if createdAt.Valid {
		item.CreatedAt = parseTime(createdAt.String)
	}
	return &item, nil
}

func scanExternalIssueLinkRow(rows *sql.Rows) (*integration.ExternalIssueLink, error) {
	var item integration.ExternalIssueLink
	var createdAt sql.NullString
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
	if createdAt.Valid {
		item.CreatedAt = parseTime(createdAt.String)
	}
	return &item, nil
}
