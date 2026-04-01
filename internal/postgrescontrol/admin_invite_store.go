package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"urgentry/internal/auth"
)

// ListInvites returns organization invites, deriving status from timestamps.
func (s *AdminStore) ListInvites(ctx context.Context, orgSlug string) ([]*InviteRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT i.id, i.organization_id, o.slug, i.team_id, t.slug, i.email, i.role, i.token_prefix, i.created_at, i.expires_at, i.accepted_at, i.revoked_at
		   FROM member_invites i
		   JOIN organizations o ON o.id = i.organization_id
		   LEFT JOIN teams t ON t.id = i.team_id
		  WHERE o.slug = $1
		  ORDER BY i.created_at DESC`,
		strings.TrimSpace(orgSlug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	invites := []*InviteRecord{}
	for rows.Next() {
		rec := &InviteRecord{}
		var teamID, teamSlug sql.NullString
		var expiresAt, acceptedAt, revokedAt sql.NullTime
		if err := rows.Scan(
			&rec.ID,
			&rec.OrganizationID,
			&rec.OrganizationSlug,
			&teamID,
			&teamSlug,
			&rec.Email,
			&rec.Role,
			&rec.TokenPrefix,
			&rec.CreatedAt,
			&expiresAt,
			&acceptedAt,
			&revokedAt,
		); err != nil {
			return nil, err
		}
		rec.TeamID = nullStr(teamID)
		rec.TeamSlug = nullStr(teamSlug)
		rec.CreatedAt = rec.CreatedAt.UTC()
		rec.ExpiresAt = nullTimePtr(expiresAt)
		rec.AcceptedAt = nullTimePtr(acceptedAt)
		rec.Status = inviteStatus(now, rec.ExpiresAt, rec.AcceptedAt, nullTimePtr(revokedAt))
		invites = append(invites, rec)
	}
	return invites, rows.Err()
}

// CreateInvite stores a pending invite and returns the raw token once.
func (s *AdminStore) CreateInvite(ctx context.Context, orgSlug, email, role, teamSlug, createdByUserID string) (*InviteRecord, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, "", fmt.Errorf("email is required")
	}
	if strings.TrimSpace(role) == "" {
		role = "member"
	}

	var orgID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, strings.TrimSpace(orgSlug)).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}

	var teamID string
	if strings.TrimSpace(teamSlug) != "" {
		if err := s.db.QueryRowContext(ctx,
			`SELECT t.id
			   FROM teams t
			   JOIN organizations o ON o.id = t.organization_id
			  WHERE o.slug = $1 AND t.slug = $2`,
			strings.TrimSpace(orgSlug), strings.TrimSpace(teamSlug),
		).Scan(&teamID); err != nil {
			if err == sql.ErrNoRows {
				return nil, "", nil
			}
			return nil, "", err
		}
	}

	raw := rawToken("ginvite")
	now := time.Now().UTC()
	expiresAt := now.Add(30 * 24 * time.Hour)
	rec := &InviteRecord{
		ID:               generateID(),
		OrganizationID:   orgID,
		OrganizationSlug: strings.TrimSpace(orgSlug),
		TeamID:           teamID,
		TeamSlug:         strings.TrimSpace(teamSlug),
		Email:            email,
		Role:             role,
		Status:           "pending",
		TokenPrefix:      tokenPrefix(raw),
		CreatedAt:        now,
		ExpiresAt:        &expiresAt,
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO member_invites
			(id, organization_id, team_id, email, role, token_prefix, token_hash, created_by_user_id, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		rec.ID, orgID, nullIfEmpty(teamID), email, role, rec.TokenPrefix, hashToken(raw), nullIfEmpty(strings.TrimSpace(createdByUserID)), expiresAt, now,
	); err != nil {
		return nil, "", err
	}
	return rec, raw, nil
}

// RevokeInvite marks an invite as revoked.
func (s *AdminStore) RevokeInvite(ctx context.Context, orgSlug, inviteID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE member_invites i
		    SET revoked_at = $1
		   FROM organizations o
		  WHERE i.organization_id = o.id
		    AND i.id = $2
		    AND o.slug = $3
		    AND i.accepted_at IS NULL
		    AND i.revoked_at IS NULL`,
		time.Now().UTC(), strings.TrimSpace(inviteID), strings.TrimSpace(orgSlug),
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

// AcceptInvite accepts a pending invite and creates a local user if needed.
func (s *AdminStore) AcceptInvite(ctx context.Context, rawToken, displayName, password string) (*InviteAcceptanceResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx,
		`SELECT i.id, i.organization_id, o.slug, i.team_id, t.slug, i.email, i.role, i.expires_at, i.accepted_at, i.revoked_at
		   FROM member_invites i
		   JOIN organizations o ON o.id = i.organization_id
		   LEFT JOIN teams t ON t.id = i.team_id
		  WHERE i.token_hash = $1
		  FOR UPDATE OF i`,
		hashToken(strings.TrimSpace(rawToken)),
	)

	var (
		invite                InviteRecord
		teamID, teamSlug      sql.NullString
		expiresAt             time.Time
		acceptedAt, revokedAt sql.NullTime
	)
	if err := row.Scan(
		&invite.ID,
		&invite.OrganizationID,
		&invite.OrganizationSlug,
		&teamID,
		&teamSlug,
		&invite.Email,
		&invite.Role,
		&expiresAt,
		&acceptedAt,
		&revokedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	invite.TeamID = nullStr(teamID)
	invite.TeamSlug = nullStr(teamSlug)
	if acceptedAt.Valid || revokedAt.Valid {
		return nil, ErrInviteConsumed
	}
	if time.Now().UTC().After(expiresAt.UTC()) {
		return nil, ErrInviteExpired
	}

	name := strings.TrimSpace(displayName)
	if name == "" {
		name = strings.TrimSpace(strings.Split(invite.Email, "@")[0])
		if name == "" {
			name = "Member"
		}
	}

	now := time.Now().UTC()
	user := auth.User{}
	var existingUserID sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE lower(email) = lower($1)`,
		invite.Email,
	).Scan(&existingUserID); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	created := false
	if !existingUserID.Valid {
		created = true
		user.ID = generateID()
		user.Email = invite.Email
		user.DisplayName = name
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
			 VALUES ($1, $2, $3, TRUE, $4, $4)
			 RETURNING id, email, display_name`,
			user.ID, user.Email, user.DisplayName, now,
		).Scan(&user.ID, &user.Email, &user.DisplayName); err != nil {
			return nil, err
		}
		if strings.TrimSpace(password) == "" {
			password = "urgentry-" + generateID()[:20]
		}
	} else {
		user.ID = existingUserID.String
		if err := tx.QueryRowContext(ctx,
			`SELECT email, display_name FROM users WHERE id = $1`,
			user.ID,
		).Scan(&user.Email, &user.DisplayName); err != nil {
			return nil, err
		}
		if name != "" && name != user.DisplayName {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET display_name = $1, updated_at = $2 WHERE id = $3`,
				name, now, user.ID,
			); err != nil {
				return nil, err
			}
			user.DisplayName = name
		}
	}

	if strings.TrimSpace(password) != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_password_credentials (user_id, password_hash, password_algo, password_updated_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id) DO UPDATE SET
			 	password_hash = EXCLUDED.password_hash,
			 	password_algo = EXCLUDED.password_algo,
			 	password_updated_at = EXCLUDED.password_updated_at`,
			user.ID, string(hash), passwordAlgorithmBcrypt, now,
		); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (organization_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		generateID(), invite.OrganizationID, user.ID, invite.Role, now,
	); err != nil {
		return nil, err
	}
	if invite.TeamID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO team_members (id, team_id, user_id, role, created_at)
			 VALUES ($1, $2, $3, 'member', $4)
			 ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
			generateID(), invite.TeamID, user.ID, now,
		); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE member_invites SET accepted_at = $1 WHERE id = $2`,
		now, invite.ID,
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
	if created && strings.TrimSpace(password) != "" {
		result.TemporaryPassword = password
	}
	return result, nil
}

func inviteStatus(now time.Time, expiresAt, acceptedAt, revokedAt *time.Time) string {
	switch {
	case acceptedAt != nil && !acceptedAt.IsZero():
		return "accepted"
	case revokedAt != nil && !revokedAt.IsZero():
		return "revoked"
	case expiresAt != nil && !expiresAt.IsZero() && now.After(expiresAt.UTC()):
		return "expired"
	default:
		return "pending"
	}
}
