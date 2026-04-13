package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

type OperatorAuditStore struct {
	db *sql.DB
}

func NewOperatorAuditStore(db *sql.DB) *OperatorAuditStore {
	return &OperatorAuditStore{db: db}
}

func (s *OperatorAuditStore) Record(ctx context.Context, record store.OperatorAuditRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("operator audit store is not configured")
	}
	action := strings.TrimSpace(record.Action)
	if action == "" {
		return fmt.Errorf("operator audit action is required")
	}
	status := strings.TrimSpace(record.Status)
	if status == "" {
		status = "succeeded"
	}
	source := strings.TrimSpace(record.Source)
	if source == "" {
		source = "system"
	}
	actor := strings.TrimSpace(record.Actor)
	if actor == "" {
		actor = "system"
	}
	detail := strings.TrimSpace(record.Detail)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operator_audit_logs
			(id, organization_id, project_id, action, status, source, actor, detail, metadata_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(),
		nullIfEmpty(record.OrganizationID),
		nullIfEmpty(record.ProjectID),
		action,
		status,
		source,
		actor,
		detail,
		firstNonEmptyString(strings.TrimSpace(record.MetadataJSON), "{}"),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert operator audit log: %w", err)
	}
	return nil
}

func (s *OperatorAuditStore) List(ctx context.Context, orgSlug string, limit int) ([]store.OperatorAuditEntry, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("operator audit store is not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id,
		        COALESCE(a.organization_id, ''),
		        COALESCE(o.slug, ''),
		        COALESCE(a.project_id, ''),
		        COALESCE(p.slug, ''),
		        a.action,
		        a.status,
		        COALESCE(a.source, ''),
		        COALESCE(a.actor, ''),
		        COALESCE(a.detail, ''),
		        COALESCE(a.metadata_json, '{}'),
		        a.created_at
		   FROM operator_audit_logs a
		   LEFT JOIN organizations o ON o.id = a.organization_id
		   LEFT JOIN projects p ON p.id = a.project_id
		  WHERE ? = '' OR COALESCE(o.slug, '') = ? OR COALESCE(a.organization_id, '') = ''
		  ORDER BY a.created_at DESC, a.id DESC
		  LIMIT ?`,
		strings.TrimSpace(orgSlug),
		strings.TrimSpace(orgSlug),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list operator audit logs: %w", err)
	}
	defer rows.Close()

	entries := make([]store.OperatorAuditEntry, 0, limit)
	for rows.Next() {
		var item store.OperatorAuditEntry
		var createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.OrganizationID,
			&item.OrganizationSlug,
			&item.ProjectID,
			&item.ProjectSlug,
			&item.Action,
			&item.Status,
			&item.Source,
			&item.Actor,
			&item.Detail,
			&item.MetadataJSON,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan operator audit log: %w", err)
		}
		item.DateCreated = parseTime(createdAt)
		entries = append(entries, item)
	}
	return entries, rows.Err()
}
