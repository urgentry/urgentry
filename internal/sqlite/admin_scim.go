package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	scimcore "urgentry/internal/scim"
	"urgentry/pkg/id"
)

var _ scimcore.UserStore = (*AdminStore)(nil)

func (s *AdminStore) ListUsers(ctx context.Context, orgID string, startIndex, count int, filter string) ([]scimcore.UserRecord, int, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return []scimcore.UserRecord{}, 0, nil
	}

	where, args, ok := sqliteSCIMFilter(filter)
	if !ok {
		return []scimcore.UserRecord{}, 0, nil
	}

	countArgs := append([]any{orgID}, args...)
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = ?`+where,
		countArgs...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	rowsArgs := append(append([]any{orgID}, args...), count, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.is_active, COALESCE(u.created_at, ''), COALESCE(u.updated_at, '')
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = ?`+where+`
		  ORDER BY COALESCE(u.created_at, ''), u.id
		  LIMIT ? OFFSET ?`,
		rowsArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]scimcore.UserRecord, 0, count)
	for rows.Next() {
		rec, err := scanSQLiteSCIMUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, rec)
	}
	return users, total, rows.Err()
}

func (s *AdminStore) GetUser(ctx context.Context, orgID, userID string) (*scimcore.UserRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.is_active, COALESCE(u.created_at, ''), COALESCE(u.updated_at, '')
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = ? AND u.id = ?`,
		strings.TrimSpace(orgID), strings.TrimSpace(userID),
	)
	rec, err := scanSQLiteSCIMUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *AdminStore) CreateUser(ctx context.Context, orgID string, user scimcore.UserRecord) (*scimcore.UserRecord, error) {
	orgID = strings.TrimSpace(orgID)
	scimcore.NormalizeUserRecord(&user)
	if orgID == "" || user.Email == "" {
		return nil, fmt.Errorf("email and organization are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id = ?`, orgID).Scan(new(string)); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("organization not found")
		}
		return nil, err
	}

	userID, membershipExists, err := sqliteLookupSCIMUser(ctx, tx, orgID, user.Email)
	if err != nil {
		return nil, err
	}
	if membershipExists {
		return nil, ErrDuplicateRecord
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if userID == "" {
		userID = id.New()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			userID, user.Email, user.DisplayName, boolToSQLite(user.Active), now, now,
		); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return nil, ErrDuplicateRecord
			}
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users
			    SET display_name = ?, is_active = ?, updated_at = ?
			  WHERE id = ?`,
			user.DisplayName, boolToSQLite(user.Active), now, userID,
		); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, ?, ?, 'member', ?)`,
		id.New(), orgID, userID, now,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrDuplicateRecord
		}
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUser(ctx, orgID, userID)
}

func (s *AdminStore) PatchUser(ctx context.Context, orgID, userID string, ops []scimcore.PatchOp) (*scimcore.UserRecord, error) {
	current, err := s.GetUser(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("user not found")
	}

	updated := *current
	scimcore.ApplyPatchOps(&updated, ops)
	if updated.Email == "" {
		updated.Email = current.Email
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE users
		    SET email = ?, display_name = ?, is_active = ?, updated_at = ?
		  WHERE id = ?`,
		updated.Email, updated.DisplayName, boolToSQLite(updated.Active), time.Now().UTC().Format(time.RFC3339), updated.ID,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrDuplicateRecord
		}
		return nil, err
	}
	return s.GetUser(ctx, orgID, userID)
}

func (s *AdminStore) DeleteUser(ctx context.Context, orgID, userID string) (bool, error) {
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	if orgID == "" || userID == "" {
		return false, nil
	}

	// Deactivate the user rather than hard-deleting, preserving audit history.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Verify membership exists.
	var memberID string
	if err := tx.QueryRowContext(ctx,
		`SELECT m.id FROM organization_members m WHERE m.organization_id = ? AND m.user_id = ?`,
		orgID, userID,
	).Scan(&memberID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}

	// Deactivate the user.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET is_active = 0, updated_at = ? WHERE id = ?`,
		now, userID,
	); err != nil {
		return false, err
	}

	// Remove team memberships within this org.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM team_members
		 WHERE user_id = ? AND team_id IN (SELECT id FROM teams WHERE organization_id = ?)`,
		userID, orgID,
	); err != nil {
		return false, err
	}

	// Remove org membership.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM organization_members WHERE organization_id = ? AND user_id = ?`,
		orgID, userID,
	); err != nil {
		return false, err
	}

	return true, tx.Commit()
}

type sqliteSCIMScanner interface {
	Scan(dest ...any) error
}

func scanSQLiteSCIMUser(scanner sqliteSCIMScanner) (scimcore.UserRecord, error) {
	var (
		rec       scimcore.UserRecord
		active    int
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(&rec.ID, &rec.Email, &rec.DisplayName, &active, &createdAt, &updatedAt); err != nil {
		return scimcore.UserRecord{}, err
	}
	rec.Active = active != 0
	rec.CreatedAt = parseTime(createdAt)
	rec.UpdatedAt = parseTime(updatedAt)
	rec.GivenName, rec.FamilyName = scimcore.InferNameParts(rec.DisplayName)
	return rec, nil
}

func sqliteLookupSCIMUser(ctx context.Context, tx *sql.Tx, orgID, email string) (userID string, membershipExists bool, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT u.id,
		        EXISTS(
		            SELECT 1
		              FROM organization_members m
		             WHERE m.organization_id = ? AND m.user_id = u.id
		        )
		   FROM users u
		  WHERE lower(u.email) = lower(?)`,
		orgID, email,
	).Scan(&userID, &membershipExists)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return userID, membershipExists, err
}

func sqliteSCIMFilter(raw string) (string, []any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, true
	}
	filter, ok := scimcore.ParseUserFilter(raw)
	if !ok {
		return "", nil, false
	}
	switch filter.Field {
	case "id":
		return ` AND u.id = ?`, []any{filter.Value}, true
	case "username", "email":
		return ` AND lower(u.email) = lower(?)`, []any{filter.Value}, true
	case "displayname":
		return ` AND u.display_name = ?`, []any{filter.Value}, true
	default:
		return "", nil, false
	}
}

func boolToSQLite(v bool) int {
	if v {
		return 1
	}
	return 0
}
