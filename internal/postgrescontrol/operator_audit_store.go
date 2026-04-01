package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
)

type OperatorAuditStore struct {
	db *sql.DB
}

func NewOperatorAuditStore(db *sql.DB) *OperatorAuditStore {
	return &OperatorAuditStore{db: db}
}

func (s *OperatorAuditStore) Record(ctx context.Context, record sharedstore.OperatorAuditRecord) error {
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
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		generateID(),
		nullIfEmpty(record.OrganizationID),
		nullIfEmpty(record.ProjectID),
		action,
		status,
		source,
		actor,
		detail,
		firstNonEmptyOperatorAuditMetadata(strings.TrimSpace(record.MetadataJSON)),
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert operator audit log: %w", err)
	}
	return nil
}

func (s *OperatorAuditStore) List(ctx context.Context, orgSlug string, limit int) ([]sharedstore.OperatorAuditEntry, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("operator audit store is not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	orgSlug = strings.TrimSpace(orgSlug)
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
		        COALESCE(a.metadata_json::text, '{}'),
		        a.created_at
		   FROM operator_audit_logs a
		   LEFT JOIN organizations o ON o.id = a.organization_id
		   LEFT JOIN projects p ON p.id = a.project_id
		  WHERE $1 = '' OR COALESCE(o.slug, '') = $1 OR COALESCE(a.organization_id, '') = ''
		  ORDER BY a.created_at DESC, a.id DESC
		  LIMIT $2`,
		orgSlug,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list operator audit logs: %w", err)
	}
	defer rows.Close()

	entries := make([]sharedstore.OperatorAuditEntry, 0, limit)
	for rows.Next() {
		var item sharedstore.OperatorAuditEntry
		var createdAt time.Time
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
		item.DateCreated = createdAt.UTC()
		entries = append(entries, item)
	}
	return entries, rows.Err()
}

func firstNonEmptyOperatorAuditMetadata(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return strings.TrimSpace(value)
}
