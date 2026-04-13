package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/store"
)

// AuditStore lists recent auth/admin audit events.
type AuditStore struct {
	db *sql.DB
}

type TrustedRelayUsageEntry struct {
	RelayID   string
	FirstSeen time.Time
	LastSeen  time.Time
}

// NewAuditStore creates an audit log reader.
func NewAuditStore(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

// ListOrganizationAuditLogs returns recent audit rows for an organization.
func (s *AuditStore) ListOrganizationAuditLogs(ctx context.Context, orgSlug string, limit int) ([]store.AuditLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id,
		        a.credential_type,
		        COALESCE(a.credential_id, ''),
		        COALESCE(a.user_id, ''),
		        COALESCE(u.email, ''),
		        COALESCE(a.project_id, ''),
		        COALESCE(p.slug, ''),
		        COALESCE(a.organization_id, ''),
		        COALESCE(o.slug, ''),
		        a.action,
		        COALESCE(a.request_path, ''),
		        COALESCE(a.request_method, ''),
		        COALESCE(a.ip_address, ''),
		        COALESCE(a.user_agent, ''),
		        a.created_at
		 FROM auth_audit_logs a
		 LEFT JOIN users u ON u.id = a.user_id
		 LEFT JOIN projects p ON p.id = a.project_id
		 LEFT JOIN organizations o ON o.id = COALESCE(a.organization_id, p.organization_id)
		 WHERE o.slug = ?
		 ORDER BY a.created_at DESC
		 LIMIT ?`,
		orgSlug, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []store.AuditLogEntry
	for rows.Next() {
		var entry store.AuditLogEntry
		var createdAt string
		if err := rows.Scan(
			&entry.ID,
			&entry.CredentialType,
			&entry.CredentialID,
			&entry.UserID,
			&entry.UserEmail,
			&entry.ProjectID,
			&entry.ProjectSlug,
			&entry.OrganizationID,
			&entry.OrganizationSlug,
			&entry.Action,
			&entry.RequestPath,
			&entry.RequestMethod,
			&entry.IPAddress,
			&entry.UserAgent,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		entry.DateCreated = parseTime(createdAt)
		logs = append(logs, entry)
	}
	return logs, rows.Err()
}

func (s *AuditStore) ListTrustedRelayUsage(ctx context.Context, orgSlug string, limit int) ([]TrustedRelayUsageEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(a.credential_id, ''),
		        MIN(a.created_at),
		        MAX(a.created_at)
		   FROM auth_audit_logs a
		   JOIN organizations o ON o.id = a.organization_id
		  WHERE o.slug = ?
		    AND a.credential_type = 'relay'
		    AND a.action = 'relay.allowed'
		  GROUP BY a.credential_id
		  ORDER BY MAX(a.created_at) DESC
		  LIMIT ?`,
		orgSlug, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list trusted relay usage: %w", err)
	}
	defer rows.Close()

	var entries []TrustedRelayUsageEntry
	for rows.Next() {
		var entry TrustedRelayUsageEntry
		var firstSeen, lastSeen string
		if err := rows.Scan(&entry.RelayID, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan trusted relay usage: %w", err)
		}
		entry.FirstSeen = parseTime(firstSeen)
		entry.LastSeen = parseTime(lastSeen)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
