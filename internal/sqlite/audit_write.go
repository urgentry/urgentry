package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AuditRecord is a generic operator action written to the shared audit log.
type AuditRecord struct {
	CredentialType string
	CredentialID   string
	UserID         string
	ProjectID      string
	OrganizationID string
	Action         string
	RequestPath    string
	RequestMethod  string
	IPAddress      string
	UserAgent      string
}

// Record appends one audit row to the shared auth/admin audit table.
func (s *AuditStore) Record(ctx context.Context, entry AuditRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("audit store is not configured")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_audit_logs
			(id, credential_type, credential_id, user_id, project_id, organization_id, action, request_path, request_method, ip_address, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(),
		nullIfEmpty(entry.CredentialType),
		nullIfEmpty(entry.CredentialID),
		nullIfEmpty(entry.UserID),
		nullIfEmpty(entry.ProjectID),
		nullIfEmpty(entry.OrganizationID),
		strings.TrimSpace(entry.Action),
		nullIfEmpty(entry.RequestPath),
		nullIfEmpty(entry.RequestMethod),
		nullIfEmpty(entry.IPAddress),
		nullIfEmpty(entry.UserAgent),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}
