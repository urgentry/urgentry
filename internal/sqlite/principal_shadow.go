package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/auth"
)

// PrincipalShadowStore keeps the query-plane user and membership shadows in sync
// with control-plane lifecycle events.
type PrincipalShadowStore struct {
	db *sql.DB
}

func NewPrincipalShadowStore(db *sql.DB) *PrincipalShadowStore {
	return &PrincipalShadowStore{db: db}
}

func (s *PrincipalShadowStore) UpsertUser(ctx context.Context, user *auth.User) error {
	if s == nil || s.db == nil || user == nil {
		return nil
	}
	userID := strings.TrimSpace(user.ID)
	if userID == "" {
		return nil
	}
	email := strings.TrimSpace(user.Email)
	if email == "" {
		email = userID
	}
	displayName := strings.TrimSpace(user.DisplayName)
	if displayName == "" {
		displayName = email
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		 	email = excluded.email,
		 	display_name = excluded.display_name,
		 	is_active = 1,
		 	updated_at = excluded.updated_at`,
		userID,
		email,
		displayName,
		now,
		now,
	); err != nil {
		return fmt.Errorf("upsert user shadow: %w", err)
	}
	return nil
}

func (s *PrincipalShadowStore) UpsertOrganizationMember(ctx context.Context, member OrgMemberRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	orgID := strings.TrimSpace(member.OrganizationID)
	userID := strings.TrimSpace(member.UserID)
	if orgID == "" || userID == "" {
		return nil
	}
	if err := s.ensureOrganization(ctx, member.OrganizationID, member.OrganizationSlug); err != nil {
		return err
	}
	if err := s.UpsertUser(ctx, &auth.User{
		ID:          member.UserID,
		Email:       member.Email,
		DisplayName: member.Name,
	}); err != nil {
		return err
	}
	memberID := strings.TrimSpace(member.ID)
	if memberID == "" {
		memberID = generateID()
	}
	createdAt := member.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(organization_id, user_id) DO UPDATE SET role = excluded.role`,
		memberID,
		orgID,
		userID,
		strings.TrimSpace(member.Role),
		createdAt.Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("upsert organization membership shadow: %w", err)
	}
	return nil
}

func (s *PrincipalShadowStore) DeleteOrganizationMember(ctx context.Context, organizationID, userID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM organization_members WHERE organization_id = ? AND user_id = ?`,
		strings.TrimSpace(organizationID),
		strings.TrimSpace(userID),
	); err != nil {
		return fmt.Errorf("delete organization membership shadow: %w", err)
	}
	return nil
}

func (s *PrincipalShadowStore) ensureOrganization(ctx context.Context, organizationID, organizationSlug string) error {
	if s == nil || s.db == nil {
		return nil
	}
	orgID := strings.TrimSpace(organizationID)
	if orgID == "" {
		return nil
	}
	slug := strings.TrimSpace(organizationSlug)
	if slug == "" {
		slug = orgID
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES (?, ?, ?)`,
		orgID,
		slug,
		slug,
	); err != nil {
		return fmt.Errorf("ensure organization shadow: %w", err)
	}
	return nil
}
