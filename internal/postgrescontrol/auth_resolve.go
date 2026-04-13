package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/auth"
)

// LookupKey looks up a project key by public key.
func (s *AuthStore) LookupKey(ctx context.Context, publicKey string) (*auth.ProjectKey, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT public_key, project_id, status, COALESCE(rate_limit_per_minute, 0)
		 FROM project_keys
		 WHERE public_key = $1`,
		publicKey,
	)

	var key auth.ProjectKey
	if err := row.Scan(&key.PublicKey, &key.ProjectID, &key.Status, &key.RateLimit); err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrKeyNotFound
		}
		return nil, fmt.Errorf("lookup key: %w", err)
	}
	if key.Status == "" {
		key.Status = "active"
	}
	return &key, nil
}

// ResolveOrganizationBySlug resolves an organization resource.
func (s *AuthStore) ResolveOrganizationBySlug(ctx context.Context, slug string) (*auth.Organization, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, slug FROM organizations WHERE slug = $1`,
		slug,
	)
	var org auth.Organization
	if err := row.Scan(&org.ID, &org.Slug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve organization: %w", err)
	}
	return &org, nil
}

// ResolveProjectByID resolves a project resource by ID.
func (s *AuthStore) ResolveProjectByID(ctx context.Context, projectID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE p.id = $1`,
		projectID,
	)
	return scanResolvedProject(row)
}

// ResolveProjectBySlug resolves a project resource by org/project slugs.
func (s *AuthStore) ResolveProjectBySlug(ctx context.Context, orgSlug, projectSlug string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = $1 AND p.slug = $2`,
		orgSlug, projectSlug,
	)
	return scanResolvedProject(row)
}

// ResolveIssueProject resolves the owning project for an issue/group.
func (s *AuthStore) ResolveIssueProject(ctx context.Context, issueID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE g.id = $1`,
		issueID,
	)
	return scanResolvedProject(row)
}

// ResolveEventProject resolves the owning project for an event occurrence.
func (s *AuthStore) ResolveEventProject(ctx context.Context, eventID string) (*auth.Project, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM group_occurrences go
		 JOIN groups g ON g.id = go.group_id
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE go.event_id = $1
		 ORDER BY go.occurred_at DESC
		 LIMIT 1`,
		eventID,
	)
	return scanResolvedProject(row)
}

// LookupUserOrgRole returns the member role for a user in an organization.
func (s *AuthStore) LookupUserOrgRole(ctx context.Context, userID, organizationID string) (string, error) {
	var role string
	if err := s.db.QueryRowContext(ctx,
		`SELECT role FROM organization_members WHERE user_id = $1 AND organization_id = $2`,
		userID, organizationID,
	).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("lookup organization role: %w", err)
	}
	return role, nil
}

// ListUserOrgRoles returns all organization memberships for a user.
func (s *AuthStore) ListUserOrgRoles(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT organization_id, role FROM organization_members WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list user org roles: %w", err)
	}
	defer rows.Close()

	roles := map[string]string{}
	for rows.Next() {
		var organizationID, role string
		if err := rows.Scan(&organizationID, &role); err != nil {
			return nil, fmt.Errorf("scan user org role: %w", err)
		}
		roles[organizationID] = role
	}
	return roles, rows.Err()
}

// LookupUserProjectRole returns the project-level role for a user, or "" if none.
func (s *AuthStore) LookupUserProjectRole(ctx context.Context, userID, projectID string) (string, error) {
	var role string
	if err := s.db.QueryRowContext(ctx,
		`SELECT role FROM project_memberships WHERE user_id = $1 AND project_id = $2`,
		userID, projectID,
	).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("lookup project role: %w", err)
	}
	return role, nil
}

// TouchProjectKey records usage of a project key.
func (s *AuthStore) TouchProjectKey(ctx context.Context, publicKey string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_keys SET last_used_at = $1 WHERE public_key = $2`,
		time.Now().UTC(), publicKey,
	); err != nil {
		return fmt.Errorf("touch project key: %w", err)
	}
	return nil
}

func scanResolvedProject(row *sql.Row) (*auth.Project, error) {
	var project auth.Project
	if err := row.Scan(&project.ID, &project.Slug, &project.OrganizationID, &project.OrganizationSlug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &project, nil
}
