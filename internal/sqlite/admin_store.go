package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"urgentry/internal/auth"
	"urgentry/pkg/id"
)

// AdminStore persists org, team, member, and invite data.
type AdminStore struct {
	db *sql.DB
}

// NewAdminStore creates an AdminStore backed by SQLite.
func NewAdminStore(db *sql.DB) *AdminStore {
	return &AdminStore{db: db}
}

// OrgMemberRecord represents an organization membership row.
type OrgMemberRecord struct {
	ID               string
	OrganizationID   string
	OrganizationSlug string
	UserID           string
	Email            string
	Name             string
	Role             string
	CreatedAt        time.Time
}

// TeamRecord represents a team row.
type TeamRecord struct {
	ID               string
	OrganizationID   string
	OrganizationSlug string
	Slug             string
	Name             string
	CreatedAt        time.Time
}

// TeamMemberRecord represents a team membership row.
type TeamMemberRecord struct {
	ID               string
	TeamID           string
	TeamSlug         string
	OrganizationID   string
	OrganizationSlug string
	UserID           string
	Email            string
	Name             string
	Role             string
	CreatedAt        time.Time
}

// InviteRecord represents an organization invite.
type InviteRecord struct {
	ID               string
	OrganizationID   string
	OrganizationSlug string
	TeamID           string
	TeamSlug         string
	Email            string
	Role             string
	Status           string
	TokenPrefix      string
	CreatedAt        time.Time
	ExpiresAt        *time.Time
	AcceptedAt       *time.Time
	AcceptedByUserID string
}

// InviteAcceptanceResult describes the outcome of accepting an invite.
type InviteAcceptanceResult struct {
	InviteID          string
	OrganizationID    string
	OrganizationSlug  string
	TeamID            string
	TeamSlug          string
	Role              string
	User              auth.User
	TemporaryPassword string
}

// TeamProjectRecord represents a project belonging to a team.
type TeamProjectRecord struct {
	ID          string
	Slug        string
	Name        string
	Platform    string
	Status      string
	DateCreated time.Time
}

// ProjectMemberRecord represents a project membership row.
type ProjectMemberRecord struct {
	ID        string
	ProjectID string
	UserID    string
	Email     string
	Name      string
	Role      string
	CreatedAt time.Time
}

var (
	ErrAdminNotFound   = errors.New("admin resource not found")
	ErrInviteConsumed  = errors.New("invite already accepted")
	ErrInviteExpired   = errors.New("invite expired")
	ErrInviteNotFound  = errors.New("invite not found")
	ErrDuplicateRecord = errors.New("duplicate record")
)

func (s *AdminStore) userRecordByID(ctx context.Context, userID string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name FROM users WHERE id = ? AND is_active = 1`,
		userID,
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
		 WHERE o.slug = ?
		 ORDER BY m.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*OrgMemberRecord
	for rows.Next() {
		var rec OrgMemberRecord
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		members = append(members, &rec)
	}
	return members, rows.Err()
}

// GetOrgMember returns a single organization member by membership record ID.
func (s *AdminStore) GetOrgMember(ctx context.Context, orgSlug, memberID string) (*OrgMemberRecord, error) {
	var rec OrgMemberRecord
	var createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 JOIN users u ON u.id = m.user_id
		 WHERE o.slug = ? AND m.id = ?`,
		orgSlug, memberID,
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = parseTime(nullStr(createdAt))
	return &rec, nil
}

// UpdateOrgMemberRole updates the role of an organization member by membership record ID.
func (s *AdminStore) UpdateOrgMemberRole(ctx context.Context, orgSlug, memberID, role string) (*OrgMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rec OrgMemberRecord
	var createdAt sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 JOIN users u ON u.id = m.user_id
		 WHERE o.slug = ? AND m.id = ?`,
		orgSlug, memberID,
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = parseTime(nullStr(createdAt))

	if _, err := tx.ExecContext(ctx,
		`UPDATE organization_members SET role = ? WHERE id = ?`,
		role, memberID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.Role = role
	return &rec, nil
}

// AddOrgMember adds or updates a user membership in an org.
func (s *AdminStore) AddOrgMember(ctx context.Context, orgSlug, userID, role string) (*OrgMemberRecord, error) {
	if role == "" {
		role = "member"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	user, err := s.userRecordByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var memberID, createdAt string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(organization_id, user_id) DO UPDATE SET role = excluded.role
		 RETURNING id, created_at`,
		id.New(), orgID, user.ID, role, now,
	).Scan(&memberID, &createdAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &OrgMemberRecord{
		ID:               memberID,
		OrganizationID:   orgID,
		OrganizationSlug: orgSlug,
		UserID:           user.ID,
		Email:            user.Email,
		Name:             user.DisplayName,
		Role:             role,
		CreatedAt:        parseTime(createdAt),
	}, nil
}

// RemoveOrgMember removes a user membership from an org and its team memberships.
func (s *AdminStore) RemoveOrgMember(ctx context.Context, orgSlug, userID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM team_members
		 WHERE user_id = ? AND team_id IN (SELECT id FROM teams WHERE organization_id = ?)`,
		userID, orgID,
	); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM organization_members WHERE organization_id = ? AND user_id = ?`,
		orgID, userID,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rows > 0, nil
}

// ListTeams returns teams for an organization.
func (s *AdminStore) ListTeams(ctx context.Context, orgSlug string) ([]*TeamRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = ?
		 ORDER BY t.created_at ASC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []*TeamRecord
	for rows.Next() {
		var rec TeamRecord
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		teams = append(teams, &rec)
	}
	return teams, rows.Err()
}

// CreateTeam creates a new team in an organization.
func (s *AdminStore) CreateTeam(ctx context.Context, orgSlug, slug, name string) (*TeamRecord, error) {
	if slug == "" || name == "" {
		return nil, fmt.Errorf("team slug and name are required")
	}
	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var rec TeamRecord
	var createdAt string
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO teams (id, organization_id, slug, name, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 RETURNING id, organization_id, ?, slug, name, created_at`,
		id.New(), orgID, slug, name, now, orgSlug,
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &createdAt); err != nil {
		return nil, err
	}
	rec.CreatedAt = parseTime(createdAt)
	return &rec, nil
}

// GetTeam returns a single team by org and team slug, along with member and project counts.
// Returns (nil, 0, 0, nil) if the team is not found.
func (s *AdminStore) GetTeam(ctx context.Context, orgSlug, teamSlug string) (*TeamRecord, int, int, error) {
	var rec TeamRecord
	var createdAt sql.NullString
	var memberCount, projectCount int
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at,
		        (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id) AS member_count,
		        (SELECT COUNT(*) FROM projects p WHERE p.team_id = t.id) AS project_count
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = ? AND t.slug = ?`,
		orgSlug, teamSlug,
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &createdAt, &memberCount, &projectCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, nil
		}
		return nil, 0, 0, err
	}
	rec.CreatedAt = parseTime(nullStr(createdAt))
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
	var createdAt sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT t.id, t.organization_id, o.slug, t.slug, t.name, t.created_at
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = ? AND t.slug = ?`,
		orgSlug, teamSlug,
	).Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = parseTime(nullStr(createdAt))

	if newName != nil {
		rec.Name = strings.TrimSpace(*newName)
	}
	if newSlug != nil {
		rec.Slug = strings.TrimSpace(*newSlug)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE teams SET name = ?, slug = ? WHERE id = ?`,
		rec.Name, rec.Slug, rec.ID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rec, nil
}

// DeleteTeam deletes a team and its memberships. Returns true if a team was deleted.
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
		 WHERE o.slug = ? AND t.slug = ?`,
		orgSlug, teamSlug,
	).Scan(&teamID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ?`, teamID,
	); err != nil {
		return false, err
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM teams WHERE id = ?`, teamID,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rows > 0, nil
}

// ListTeamMembers returns team members for a specific team.
func (s *AdminStore) ListTeamMembers(ctx context.Context, orgSlug, teamSlug string) ([]*TeamMemberRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tm.id, tm.team_id, t.slug, tm.user_id, o.id, o.slug, u.email, u.display_name, tm.role, tm.created_at
		 FROM team_members tm
		 JOIN teams t ON t.id = tm.team_id
		 JOIN organizations o ON o.id = t.organization_id
		 JOIN users u ON u.id = tm.user_id
		 WHERE o.slug = ? AND t.slug = ?
		 ORDER BY tm.created_at ASC`,
		orgSlug, teamSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*TeamMemberRecord
	for rows.Next() {
		var rec TeamMemberRecord
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.TeamID, &rec.TeamSlug, &rec.UserID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Email, &rec.Name, &rec.Role, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		members = append(members, &rec)
	}
	return members, rows.Err()
}

// AddTeamMember adds or updates a team membership.
func (s *AdminStore) AddTeamMember(ctx context.Context, orgSlug, teamSlug, userID, role string) (*TeamMemberRecord, error) {
	if role == "" {
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
		 WHERE o.slug = ? AND t.slug = ?`,
		orgSlug, teamSlug,
	).Scan(&teamID, &orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	user, err := s.userRecordByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(organization_id, user_id) DO NOTHING`,
		id.New(), orgID, user.ID, "member", now,
	); err != nil {
		return nil, err
	}

	var memberID, createdAt string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO team_members (id, team_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(team_id, user_id) DO UPDATE SET role = excluded.role
		 RETURNING id, created_at`,
		id.New(), teamID, user.ID, role, now,
	).Scan(&memberID, &createdAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TeamMemberRecord{
		ID:               memberID,
		TeamID:           teamID,
		TeamSlug:         teamSlug,
		OrganizationID:   orgID,
		OrganizationSlug: orgSlug,
		UserID:           user.ID,
		Email:            user.Email,
		Name:             user.DisplayName,
		Role:             role,
		CreatedAt:        parseTime(createdAt),
	}, nil
}

// RemoveTeamMember removes a user from a team.
func (s *AdminStore) RemoveTeamMember(ctx context.Context, orgSlug, teamSlug, userID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members
		 WHERE user_id = ?
		   AND team_id = (
		     SELECT t.id
		     FROM teams t
		     JOIN organizations o ON o.id = t.organization_id
		     WHERE o.slug = ? AND t.slug = ?
		   )`,
		userID, orgSlug, teamSlug,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// ListTeamProjects returns projects belonging to a specific team.
func (s *AdminStore) ListTeamProjects(ctx context.Context, orgSlug, teamSlug string) ([]TeamProjectRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.slug, p.name, p.platform, p.status, p.created_at
		 FROM projects p
		 JOIN teams t ON t.id = p.team_id
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = ? AND t.slug = ?
		 ORDER BY p.created_at ASC`,
		orgSlug, teamSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TeamProjectRecord
	for rows.Next() {
		var rec TeamProjectRecord
		var platform, status, createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Name, &platform, &status, &createdAt); err != nil {
			return nil, err
		}
		rec.Platform = nullStr(platform)
		rec.Status = nullStr(status)
		rec.DateCreated = parseTime(nullStr(createdAt))
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
		 WHERE o.slug = ? AND tm.user_id = ?
		 ORDER BY t.created_at ASC`,
		orgSlug, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []*TeamRecord
	for rows.Next() {
		var rec TeamRecord
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.Slug, &rec.Name, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		teams = append(teams, &rec)
	}
	return teams, rows.Err()
}

// AddMemberToTeamByMemberID adds an org member (by org membership ID) to a team.
func (s *AdminStore) AddMemberToTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (*TeamMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Resolve org member → user_id, team_id
	var userID, teamID, orgID, email, displayName string
	if err := tx.QueryRowContext(ctx,
		`SELECT m.user_id, t.id, o.id, u.email, u.display_name
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 JOIN teams t ON t.organization_id = o.id
		 JOIN users u ON u.id = m.user_id
		 WHERE o.slug = ? AND m.id = ? AND t.slug = ?`,
		orgSlug, memberID, teamSlug,
	).Scan(&userID, &teamID, &orgID, &email, &displayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var tmID, createdAt string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO team_members (id, team_id, user_id, role, created_at)
		 VALUES (?, ?, ?, 'member', ?)
		 ON CONFLICT(team_id, user_id) DO UPDATE SET role = excluded.role
		 RETURNING id, created_at`,
		id.New(), teamID, userID, now,
	).Scan(&tmID, &createdAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TeamMemberRecord{
		ID:               tmID,
		TeamID:           teamID,
		TeamSlug:         teamSlug,
		OrganizationID:   orgID,
		OrganizationSlug: orgSlug,
		UserID:           userID,
		Email:            email,
		Name:             displayName,
		Role:             "member",
		CreatedAt:        parseTime(createdAt),
	}, nil
}

// RemoveMemberFromTeamByMemberID removes an org member (by org membership ID) from a team.
func (s *AdminStore) RemoveMemberFromTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members
		 WHERE user_id = (
		   SELECT m.user_id
		   FROM organization_members m
		   JOIN organizations o ON o.id = m.organization_id
		   WHERE o.slug = ? AND m.id = ?
		 )
		 AND team_id = (
		   SELECT t.id
		   FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		   WHERE o.slug = ? AND t.slug = ?
		 )`,
		orgSlug, memberID, orgSlug, teamSlug,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// ListInvites returns organization invites.
func (s *AdminStore) ListInvites(ctx context.Context, orgSlug string) ([]*InviteRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT i.id, i.organization_id, o.slug, COALESCE(i.team_id, ''), COALESCE(t.slug, ''), i.email, i.role, i.status, i.token_prefix, i.created_at, i.expires_at, i.accepted_at, COALESCE(i.accepted_by_user_id, '')
		 FROM member_invites i
		 JOIN organizations o ON o.id = i.organization_id
		 LEFT JOIN teams t ON t.id = i.team_id
		 WHERE o.slug = ?
		 ORDER BY i.created_at DESC`,
		orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []*InviteRecord
	for rows.Next() {
		var rec InviteRecord
		var createdAt, expiresAt, acceptedAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.OrganizationID, &rec.OrganizationSlug, &rec.TeamID, &rec.TeamSlug, &rec.Email, &rec.Role, &rec.Status, &rec.TokenPrefix, &createdAt, &expiresAt, &acceptedAt, &rec.AcceptedByUserID); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		if s := nullStr(expiresAt); s != "" {
			t := parseTime(s)
			rec.ExpiresAt = &t
		}
		if s := nullStr(acceptedAt); s != "" {
			t := parseTime(s)
			rec.AcceptedAt = &t
		}
		invites = append(invites, &rec)
	}
	return invites, rows.Err()
}

// CreateInvite stores a pending invite and returns the raw token once.
func (s *AdminStore) CreateInvite(ctx context.Context, orgSlug, email, role, teamSlug, invitedByUserID string) (*InviteRecord, string, error) {
	if strings.TrimSpace(email) == "" {
		return nil, "", fmt.Errorf("email is required")
	}
	if role == "" {
		role = "member"
	}

	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}

	var teamID string
	if teamSlug != "" {
		row := s.db.QueryRowContext(ctx,
			`SELECT t.id
			 FROM teams t
			 JOIN organizations o ON o.id = t.organization_id
			 WHERE o.slug = ? AND t.slug = ?`,
			orgSlug, teamSlug,
		)
		if err := row.Scan(&teamID); err != nil {
			if err == sql.ErrNoRows {
				return nil, "", nil
			}
			return nil, "", err
		}
	}

	raw := rawToken("ginvite")
	now := time.Now().UTC()
	expiresAt := now.Add(30 * 24 * time.Hour)
	inviteID := id.New()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_invites
			(id, organization_id, team_id, email, role, token_prefix, token_hash, status, invited_by_user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		inviteID, orgID, nullable(teamID), strings.ToLower(strings.TrimSpace(email)), role, tokenPrefix(raw), hashToken(raw), nullable(invitedByUserID), now.Format(time.RFC3339), expiresAt.Format(time.RFC3339),
	); err != nil {
		return nil, "", err
	}

	rec := &InviteRecord{
		ID:               inviteID,
		OrganizationID:   orgID,
		OrganizationSlug: orgSlug,
		TeamID:           teamID,
		TeamSlug:         teamSlug,
		Email:            strings.ToLower(strings.TrimSpace(email)),
		Role:             role,
		Status:           "pending",
		TokenPrefix:      tokenPrefix(raw),
		CreatedAt:        now,
		ExpiresAt:        &expiresAt,
	}
	return rec, raw, nil
}

// RevokeInvite marks an invite as revoked.
func (s *AdminStore) RevokeInvite(ctx context.Context, orgSlug, inviteID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE member_invites
		 SET status = 'revoked'
		 WHERE id = ? AND organization_id = (SELECT id FROM organizations WHERE slug = ?)`,
		inviteID, orgSlug,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// AcceptInvite accepts a pending invite, creating a local user if needed.
func (s *AdminStore) AcceptInvite(ctx context.Context, rawToken, displayName, password string) (*InviteAcceptanceResult, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT i.id, i.organization_id, o.slug, COALESCE(i.team_id, ''), COALESCE(t.slug, ''), i.email, i.role, i.status, i.expires_at
		 FROM member_invites i
		 JOIN organizations o ON o.id = i.organization_id
		 LEFT JOIN teams t ON t.id = i.team_id
		 WHERE i.token_hash = ?`,
		hashToken(rawToken),
	)
	var invite InviteRecord
	var expiresAt sql.NullString
	if err := row.Scan(&invite.ID, &invite.OrganizationID, &invite.OrganizationSlug, &invite.TeamID, &invite.TeamSlug, &invite.Email, &invite.Role, &invite.Status, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if invite.Status != "pending" {
		return nil, ErrInviteConsumed
	}
	if s := nullStr(expiresAt); s != "" {
		expires := parseTime(s)
		if !expires.IsZero() && time.Now().UTC().After(expires) {
			return nil, ErrInviteExpired
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	name := strings.TrimSpace(displayName)
	if name == "" {
		name = strings.Split(invite.Email, "@")[0]
		if name == "" {
			name = "Member"
		}
	}

	var user auth.User
	var existingUserID sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE lower(email) = lower(?)`,
		invite.Email,
	).Scan(&existingUserID); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	created := false
	if !existingUserID.Valid {
		created = true
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
			 VALUES (?, ?, ?, 1, ?, ?)
			 RETURNING id, email, display_name`,
			id.New(), invite.Email, name, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
		).Scan(&user.ID, &user.Email, &user.DisplayName); err != nil {
			return nil, err
		}
		if password == "" {
			password = "urgentry-" + id.New()[:20]
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id) DO UPDATE SET password_hash = excluded.password_hash, password_algo = excluded.password_algo, password_updated_at = excluded.password_updated_at`,
			user.ID, string(hash), passwordAlgorithmBcrypt, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return nil, err
		}
	} else {
		user.ID = existingUserID.String
		if err := tx.QueryRowContext(ctx,
			`SELECT email, display_name FROM users WHERE id = ?`,
			user.ID,
		).Scan(&user.Email, &user.DisplayName); err != nil {
			return nil, err
		}
		if name != "" && name != user.DisplayName {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
				name, time.Now().UTC().Format(time.RFC3339), user.ID,
			); err != nil {
				return nil, err
			}
			user.DisplayName = name
		}
		if password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
				 VALUES (?, ?, ?, ?)
				 ON CONFLICT(user_id) DO UPDATE SET password_hash = excluded.password_hash, password_algo = excluded.password_algo, password_updated_at = excluded.password_updated_at`,
				user.ID, string(hash), passwordAlgorithmBcrypt, time.Now().UTC().Format(time.RFC3339),
			); err != nil {
				return nil, err
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var memberID string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(organization_id, user_id) DO UPDATE SET role = excluded.role
		 RETURNING id`,
		id.New(), invite.OrganizationID, user.ID, invite.Role, now,
	).Scan(&memberID); err != nil {
		return nil, err
	}

	var teamMemberID string
	if invite.TeamID != "" {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO team_members (id, team_id, user_id, role, created_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(team_id, user_id) DO UPDATE SET role = excluded.role
			 RETURNING id`,
			id.New(), invite.TeamID, user.ID, "member", now,
		).Scan(&teamMemberID); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE member_invites
		 SET status = 'accepted', accepted_by_user_id = ?, accepted_at = ?
		 WHERE id = ?`,
		user.ID, now, invite.ID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	result := &InviteAcceptanceResult{
		InviteID:         invite.ID,
		OrganizationID:   invite.OrganizationID,
		OrganizationSlug: invite.OrganizationSlug,
		TeamID:           invite.TeamID,
		TeamSlug:         invite.TeamSlug,
		Role:             invite.Role,
		User:             user,
	}
	if created && password != "" {
		result.TemporaryPassword = password
	}
	_ = memberID
	_ = teamMemberID
	return result, nil
}

// ListProjectMembers returns all members for a project identified by org/project slugs.
func (s *AdminStore) ListProjectMembers(ctx context.Context, orgSlug, projectSlug string) ([]*ProjectMemberRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pm.id, pm.project_id, pm.user_id, u.email, u.display_name, pm.role, pm.created_at
		 FROM project_memberships pm
		 JOIN projects p ON p.id = pm.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 JOIN users u ON u.id = pm.user_id
		 WHERE o.slug = ? AND p.slug = ?
		 ORDER BY pm.created_at ASC`,
		orgSlug, projectSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*ProjectMemberRecord
	for rows.Next() {
		var rec ProjectMemberRecord
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &rec.UserID, &rec.Email, &rec.Name, &rec.Role, &createdAt); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(nullStr(createdAt))
		members = append(members, &rec)
	}
	return members, rows.Err()
}

// UpdateProjectMemberRole updates the project role for a specific membership record.
// Returns the updated record, or nil if not found.
func (s *AdminStore) UpdateProjectMemberRole(ctx context.Context, orgSlug, projectSlug, memberID, role string) (*ProjectMemberRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Verify the membership belongs to this project.
	var rec ProjectMemberRecord
	var createdAt sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT pm.id, pm.project_id, pm.user_id, u.email, u.display_name, pm.created_at
		 FROM project_memberships pm
		 JOIN projects p ON p.id = pm.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 JOIN users u ON u.id = pm.user_id
		 WHERE pm.id = ? AND o.slug = ? AND p.slug = ?`,
		memberID, orgSlug, projectSlug,
	).Scan(&rec.ID, &rec.ProjectID, &rec.UserID, &rec.Email, &rec.Name, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = parseTime(nullStr(createdAt))

	if _, err := tx.ExecContext(ctx,
		`UPDATE project_memberships SET role = ? WHERE id = ?`,
		role, memberID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rec.Role = role
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
		 WHERE o.slug = ? AND p.slug = ?`,
		orgSlug, projectSlug,
	).Scan(&projectID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	user, err := s.userRecordByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var memberID, memberCreatedAt string
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO project_memberships (id, project_id, user_id, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, user_id) DO UPDATE SET role = excluded.role
		 RETURNING id, created_at`,
		id.New(), projectID, user.ID, role, now,
	).Scan(&memberID, &memberCreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ProjectMemberRecord{
		ID:        memberID,
		ProjectID: projectID,
		UserID:    user.ID,
		Email:     user.Email,
		Name:      user.DisplayName,
		Role:      role,
		CreatedAt: parseTime(memberCreatedAt),
	}, nil
}
