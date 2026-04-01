package postgrescontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	"urgentry/pkg/id"
)

type OrgMemberRecord = sqlite.OrgMemberRecord
type TeamRecord = sqlite.TeamRecord
type TeamMemberRecord = sqlite.TeamMemberRecord
type InviteRecord = sqlite.InviteRecord
type InviteAcceptanceResult = sqlite.InviteAcceptanceResult
type ProjectMemberRecord = sqlite.ProjectMemberRecord

var (
	ErrAdminNotFound   = sqlite.ErrAdminNotFound
	ErrInviteConsumed  = sqlite.ErrInviteConsumed
	ErrInviteExpired   = sqlite.ErrInviteExpired
	ErrInviteNotFound  = sqlite.ErrInviteNotFound
	ErrDuplicateRecord = sqlite.ErrDuplicateRecord
)

type userLookup interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// AdminStore persists org, team, member, and invite data in Postgres.
type AdminStore struct {
	db *sql.DB
}

// NewAdminStore creates a Postgres-backed admin store.
func NewAdminStore(db *sql.DB) *AdminStore {
	return &AdminStore{db: db}
}

func (s *AdminStore) userRecordByID(ctx context.Context, q userLookup, userID string) (*auth.User, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, email, display_name
		   FROM users
		  WHERE id = $1 AND is_active = TRUE`,
		strings.TrimSpace(userID),
	)
	var user auth.User
	if err := row.Scan(&user.ID, &user.Email, &user.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// ListOrgMembers returns members for an organization.
func (s *AdminStore) ListOrgMembers(ctx context.Context, orgSlug string) ([]*OrgMemberRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		   FROM organization_members m
		   JOIN organizations o ON o.id = m.organization_id
		   JOIN users u ON u.id = m.user_id
		  WHERE o.slug = $1
		  ORDER BY m.created_at ASC`,
		strings.TrimSpace(orgSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []*OrgMemberRecord{}
	for rows.Next() {
		rec := &OrgMemberRecord{}
		if err := rows.Scan(
			&rec.ID,
			&rec.OrganizationID,
			&rec.OrganizationSlug,
			&rec.UserID,
			&rec.Email,
			&rec.Name,
			&rec.Role,
			&rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt = rec.CreatedAt.UTC()
		members = append(members, rec)
	}
	return members, rows.Err()
}

// GetOrgMember returns a single organization member by membership record ID.
func (s *AdminStore) GetOrgMember(ctx context.Context, orgSlug, memberID string) (*OrgMemberRecord, error) {
	rec := &OrgMemberRecord{}
	err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		   FROM organization_members m
		   JOIN organizations o ON o.id = m.organization_id
		   JOIN users u ON u.id = m.user_id
		  WHERE o.slug = $1 AND m.id = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(memberID),
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &rec.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// UpdateOrgMemberRole updates the role of an organization member by membership record ID.
func (s *AdminStore) UpdateOrgMemberRole(ctx context.Context, orgSlug, memberID, role string) (*OrgMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rec := &OrgMemberRecord{}
	if err := tx.QueryRowContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		   FROM organization_members m
		   JOIN organizations o ON o.id = m.organization_id
		   JOIN users u ON u.id = m.user_id
		  WHERE o.slug = $1 AND m.id = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(memberID),
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE organization_members SET role = $1 WHERE id = $2`,
		strings.TrimSpace(role), strings.TrimSpace(memberID),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.Role = strings.TrimSpace(role)
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// AddOrgMember adds or updates a user membership in an org.
func (s *AdminStore) AddOrgMember(ctx context.Context, orgSlug, userID, role string) (*OrgMemberRecord, error) {
	if strings.TrimSpace(role) == "" {
		role = "member"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, strings.TrimSpace(orgSlug)).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	user, err := s.userRecordByID(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC()
	rec := &OrgMemberRecord{
		OrganizationID:   orgID,
		OrganizationSlug: strings.TrimSpace(orgSlug),
		UserID:           user.ID,
		Email:            user.Email,
		Name:             user.DisplayName,
		Role:             role,
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (organization_id, user_id) DO UPDATE SET role = EXCLUDED.role
		 RETURNING id, created_at`,
		generateID(), orgID, user.ID, role, now,
	).Scan(&rec.ID, &rec.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// RemoveOrgMember removes an organization membership and any team memberships under that org.
func (s *AdminStore) RemoveOrgMember(ctx context.Context, orgSlug, userID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, strings.TrimSpace(orgSlug)).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM team_members tm
		  USING teams t
		 WHERE tm.user_id = $1
		   AND tm.team_id = t.id
		   AND t.organization_id = $2`,
		strings.TrimSpace(userID), orgID,
	); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM organization_members
		  WHERE organization_id = $1 AND user_id = $2`,
		orgID, strings.TrimSpace(userID),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// ListTeams returns teams for an organization.
func (s *AdminStore) ListTeams(ctx context.Context, orgSlug string) ([]*TeamRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		  WHERE o.slug = $1
		  ORDER BY t.created_at ASC`,
		strings.TrimSpace(orgSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := []*TeamRecord{}
	for rows.Next() {
		rec := &TeamRecord{}
		if err := rows.Scan(
			&rec.ID,
			&rec.OrganizationID,
			&rec.OrganizationSlug,
			&rec.Slug,
			&rec.Name,
			&rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt = rec.CreatedAt.UTC()
		teams = append(teams, rec)
	}
	return teams, rows.Err()
}

// CreateTeam creates a new team in an organization.
func (s *AdminStore) CreateTeam(ctx context.Context, orgSlug, slug, name string) (*TeamRecord, error) {
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	if slug == "" || name == "" {
		return nil, fmt.Errorf("team slug and name are required")
	}

	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, strings.TrimSpace(orgSlug)).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	rec := &TeamRecord{
		OrganizationID:   orgID,
		OrganizationSlug: strings.TrimSpace(orgSlug),
		Slug:             slug,
		Name:             name,
	}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO teams (id, organization_id, slug, name, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)
		 RETURNING id, created_at`,
		generateID(), orgID, slug, name, time.Now().UTC(),
	).Scan(&rec.ID, &rec.CreatedAt)
	if isDuplicateKeyError(err) {
		return nil, ErrDuplicateRecord
	}
	if err != nil {
		return nil, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// GetTeam returns a single team by org and team slug, along with member and project counts.
// Returns (nil, 0, 0, nil) if the team is not found.
func (s *AdminStore) GetTeam(ctx context.Context, orgSlug, teamSlug string) (*TeamRecord, int, int, error) {
	var rec TeamRecord
	var memberCount, projectCount int
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at,
		        (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id),
		        (SELECT COUNT(*) FROM projects p WHERE p.team_id = t.id)
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		  WHERE o.slug = $1 AND t.slug = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &rec.CreatedAt, &memberCount, &projectCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, nil
		}
		return nil, 0, 0, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return &rec, memberCount, projectCount, nil
}

// UpdateTeam updates a team's name and/or slug. Returns the updated record, or nil if not found.
func (s *AdminStore) UpdateTeam(ctx context.Context, orgSlug, teamSlug string, newName, newSlug *string) (*TeamRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rec TeamRecord
	if err := tx.QueryRowContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		  WHERE o.slug = $1 AND t.slug = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if newName != nil {
		rec.Name = strings.TrimSpace(*newName)
	}
	if newSlug != nil {
		rec.Slug = strings.TrimSpace(*newSlug)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE teams SET name = $1, slug = $2, updated_at = $3 WHERE id = $4`,
		rec.Name, rec.Slug, time.Now().UTC(), rec.ID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return &rec, nil
}

// DeleteTeam deletes a team and cascades to team memberships. Returns true if a team was deleted.
func (s *AdminStore) DeleteTeam(ctx context.Context, orgSlug, teamSlug string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var teamID string
	if err := tx.QueryRowContext(ctx,
		`SELECT t.id
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		  WHERE o.slug = $1 AND t.slug = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	).Scan(&teamID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = $1`, teamID,
	); err != nil {
		return false, err
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM teams WHERE id = $1`, teamID,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// ListTeamMembers returns team members for one team.
func (s *AdminStore) ListTeamMembers(ctx context.Context, orgSlug, teamSlug string) ([]*TeamMemberRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tm.id, tm.team_id, t.slug, tm.user_id, o.id, o.slug, u.email, u.display_name, tm.role, tm.created_at
		   FROM team_members tm
		   JOIN teams t ON t.id = tm.team_id
		   JOIN organizations o ON o.id = t.organization_id
		   JOIN users u ON u.id = tm.user_id
		  WHERE o.slug = $1 AND t.slug = $2
		  ORDER BY tm.created_at ASC`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []*TeamMemberRecord{}
	for rows.Next() {
		rec := &TeamMemberRecord{}
		if err := rows.Scan(
			&rec.ID,
			&rec.TeamID,
			&rec.TeamSlug,
			&rec.UserID,
			&rec.OrganizationID,
			&rec.OrganizationSlug,
			&rec.Email,
			&rec.Name,
			&rec.Role,
			&rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt = rec.CreatedAt.UTC()
		members = append(members, rec)
	}
	return members, rows.Err()
}

// AddTeamMember adds or updates a team membership and ensures the org membership exists.
func (s *AdminStore) AddTeamMember(ctx context.Context, orgSlug, teamSlug, userID, role string) (*TeamMemberRecord, error) {
	if strings.TrimSpace(role) == "" {
		role = "member"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var teamID, orgID string
	if err := tx.QueryRowContext(ctx,
		`SELECT t.id, o.id
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		  WHERE o.slug = $1 AND t.slug = $2`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	).Scan(&teamID, &orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	user, err := s.userRecordByID(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'member', $4)
		 ON CONFLICT (organization_id, user_id) DO NOTHING`,
		generateID(), orgID, user.ID, now,
	); err != nil {
		return nil, err
	}

	rec := &TeamMemberRecord{
		TeamID:           teamID,
		TeamSlug:         strings.TrimSpace(teamSlug),
		OrganizationID:   orgID,
		OrganizationSlug: strings.TrimSpace(orgSlug),
		UserID:           user.ID,
		Email:            user.Email,
		Name:             user.DisplayName,
		Role:             role,
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO team_members (id, team_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role
		 RETURNING id, created_at`,
		generateID(), teamID, user.ID, role, now,
	).Scan(&rec.ID, &rec.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// RemoveTeamMember removes a user from a team.
func (s *AdminStore) RemoveTeamMember(ctx context.Context, orgSlug, teamSlug, userID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members tm
		  USING teams t, organizations o
		 WHERE tm.user_id = $1
		   AND tm.team_id = t.id
		   AND t.organization_id = o.id
		   AND o.slug = $2
		   AND t.slug = $3`,
		strings.TrimSpace(userID), strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

// ListProjectMembers returns all members for a project identified by org/project slugs.
func (s *AdminStore) ListProjectMembers(ctx context.Context, orgSlug, projectSlug string) ([]*ProjectMemberRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pm.id, pm.project_id, pm.user_id, u.email, u.display_name, pm.role, pm.created_at
		   FROM project_memberships pm
		   JOIN projects p ON p.id = pm.project_id
		   JOIN organizations o ON o.id = p.organization_id
		   JOIN users u ON u.id = pm.user_id
		  WHERE o.slug = $1 AND p.slug = $2
		  ORDER BY pm.created_at ASC`,
		strings.TrimSpace(orgSlug), strings.TrimSpace(projectSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []*ProjectMemberRecord{}
	for rows.Next() {
		rec := &ProjectMemberRecord{}
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = rec.CreatedAt.UTC()
		members = append(members, rec)
	}
	return members, rows.Err()
}

// UpdateProjectMemberRole updates the project role for a specific membership record.
func (s *AdminStore) UpdateProjectMemberRole(ctx context.Context, orgSlug, projectSlug, memberID, role string) (*ProjectMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rec ProjectMemberRecord
	if err := tx.QueryRowContext(ctx,
		`SELECT pm.id, pm.project_id, pm.user_id, u.email, u.display_name, pm.created_at
		   FROM project_memberships pm
		   JOIN projects p ON p.id = pm.project_id
		   JOIN organizations o ON o.id = p.organization_id
		   JOIN users u ON u.id = pm.user_id
		  WHERE pm.id = $1 AND o.slug = $2 AND p.slug = $3`,
		memberID, orgSlug, projectSlug,
	).Scan(&rec.ID, &rec.ProjectID, &rec.UserID, &rec.Email, &rec.Name, &rec.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE project_memberships SET role = $1 WHERE id = $2`,
		role, memberID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.Role = role
	rec.CreatedAt = rec.CreatedAt.UTC()
	return &rec, nil
}

// AddProjectMember adds or updates a project membership.
func (s *AdminStore) AddProjectMember(ctx context.Context, orgSlug, projectSlug, userID, role string) (*ProjectMemberRecord, error) {
	if role == "" {
		role = "member"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var projectID string
	if err := tx.QueryRowContext(ctx,
		`SELECT p.id
		   FROM projects p
		   JOIN organizations o ON o.id = p.organization_id
		  WHERE o.slug = $1 AND p.slug = $2`,
		orgSlug, projectSlug,
	).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	user, err := s.userRecordByID(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC()
	memberID := generateID()
	var rec ProjectMemberRecord
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO project_memberships (id, project_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
		 RETURNING id, created_at`,
		memberID, projectID, user.ID, role, now,
	).Scan(&rec.ID, &rec.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.ProjectID = projectID
	rec.UserID = user.ID
	rec.Email = user.Email
	rec.Name = user.DisplayName
	rec.Role = role
	rec.CreatedAt = rec.CreatedAt.UTC()
	return &rec, nil
}

// ListTeamProjects returns projects belonging to a team.
func (s *AdminStore) ListTeamProjects(ctx context.Context, orgSlug, teamSlug string) ([]sqlite.TeamProjectRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.slug, p.name, COALESCE(p.platform,''), COALESCE(p.status,''), p.created_at
		 FROM projects p
		 JOIN teams t ON t.id = p.team_id
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = $1 AND t.slug = $2
		 ORDER BY p.created_at ASC`, orgSlug, teamSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqlite.TeamProjectRecord
	for rows.Next() {
		var rec sqlite.TeamProjectRecord
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Name, &rec.Platform, &rec.Status, &rec.DateCreated); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListUserTeams returns teams the given user belongs to within an organization.
func (s *AdminStore) ListUserTeams(ctx context.Context, orgSlug, userID string) ([]*TeamRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 JOIN team_members tm ON tm.team_id = t.id
		 WHERE o.slug = $1 AND tm.user_id = $2
		 ORDER BY t.created_at ASC`, orgSlug, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TeamRecord
	for rows.Next() {
		var rec TeamRecord
		if err := rows.Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &rec)
	}
	return out, rows.Err()
}

// AddMemberToTeamByMemberID adds an org member to a team by membership ID.
func (s *AdminStore) AddMemberToTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (*TeamMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID, teamID, orgID, email, displayName string
	if err := tx.QueryRowContext(ctx,
		`SELECT m.user_id, t.id, o.id, u.email, u.display_name
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 JOIN teams t ON t.organization_id = o.id
		 JOIN users u ON u.id = m.user_id
		 WHERE o.slug = $1 AND m.id = $2 AND t.slug = $3`,
		orgSlug, memberID, teamSlug).Scan(&userID, &teamID, &orgID, &email, &displayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	tmID := id.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO team_members (id, team_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'member', $4)
		 ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		tmID, teamID, userID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TeamMemberRecord{
		ID: tmID, TeamID: teamID, TeamSlug: teamSlug,
		OrganizationID: orgID, OrganizationSlug: orgSlug,
		UserID: userID, Email: email, Name: displayName,
		Role: "member", CreatedAt: now,
	}, nil
}

// RemoveMemberFromTeamByMemberID removes an org member from a team by membership ID.
func (s *AdminStore) RemoveMemberFromTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members
		 WHERE user_id = (SELECT m.user_id FROM organization_members m JOIN organizations o ON o.id = m.organization_id WHERE o.slug = $1 AND m.id = $2)
		 AND team_id = (SELECT t.id FROM teams t JOIN organizations o ON o.id = t.organization_id WHERE o.slug = $3 AND t.slug = $4)`,
		orgSlug, memberID, orgSlug, teamSlug)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
