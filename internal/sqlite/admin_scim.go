package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/auth"
	scimcore "urgentry/internal/scim"
	"urgentry/pkg/id"
)

var _ scimcore.UserStore = (*AdminStore)(nil)
var _ auth.SAMLUserProvisioner = (*AdminStore)(nil)

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
		`SELECT u.id,
		        COALESCE((
		            SELECT eu.external_id
		              FROM external_users eu
		             WHERE eu.org_id = m.organization_id AND eu.user_id = u.id AND eu.provider = 'scim'
		             ORDER BY eu.created_at DESC
		             LIMIT 1
		        ), ''),
		        u.email, u.display_name, u.is_active, COALESCE(u.created_at, ''), COALESCE(u.updated_at, '')
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
		`SELECT u.id,
		        COALESCE((
		            SELECT eu.external_id
		              FROM external_users eu
		             WHERE eu.org_id = m.organization_id AND eu.user_id = u.id AND eu.provider = 'scim'
		             ORDER BY eu.created_at DESC
		             LIMIT 1
		        ), ''),
		        u.email, u.display_name, u.is_active, COALESCE(u.created_at, ''), COALESCE(u.updated_at, '')
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
	if err := sqliteUpsertSCIMExternalUser(ctx, tx, orgID, userID, user.ExternalID, user.DisplayName); err != nil {
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
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
	if err := sqliteUpsertSCIMExternalUser(ctx, tx, strings.TrimSpace(orgID), updated.ID, updated.ExternalID, updated.DisplayName); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
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
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM external_users WHERE org_id = ? AND user_id = ? AND provider = 'scim'`,
		orgID, userID,
	); err != nil {
		return false, err
	}

	return true, tx.Commit()
}

func (s *AdminStore) FindOrCreateSAMLUser(ctx context.Context, orgID string, user auth.SAMLUser) (*auth.User, error) {
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" {
		return nil, fmt.Errorf("saml email is required")
	}
	displayName := strings.TrimSpace(user.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(user.FirstName + " " + user.LastName)
	}
	rec, err := s.CreateUser(ctx, orgID, scimcore.UserRecord{
		Email:       email,
		DisplayName: displayName,
		Active:      true,
	})
	if err != nil && err != ErrDuplicateRecord {
		return nil, err
	}
	if rec == nil {
		row := s.db.QueryRowContext(ctx,
			`SELECT u.id, u.email, u.display_name
			   FROM organization_members m
			   JOIN users u ON u.id = m.user_id
			  WHERE m.organization_id = ? AND lower(u.email) = lower(?)`,
			strings.TrimSpace(orgID), email,
		)
		existing, lookupErr := scanActiveAdminUser(row)
		if lookupErr != nil {
			return nil, lookupErr
		}
		return existing, nil
	}
	return &auth.User{ID: rec.ID, Email: rec.Email, DisplayName: rec.DisplayName}, nil
}

type sqliteSCIMScanner interface {
	Scan(dest ...any) error
}

func scanSQLiteSCIMUser(scanner sqliteSCIMScanner) (scimcore.UserRecord, error) {
	var (
		rec        scimcore.UserRecord
		externalID string
		active     int
		createdAt  string
		updatedAt  string
	)
	if err := scanner.Scan(&rec.ID, &externalID, &rec.Email, &rec.DisplayName, &active, &createdAt, &updatedAt); err != nil {
		return scimcore.UserRecord{}, err
	}
	rec.ExternalID = strings.TrimSpace(externalID)
	rec.Active = active != 0
	rec.CreatedAt = parseTime(createdAt)
	rec.UpdatedAt = parseTime(updatedAt)
	rec.GivenName, rec.FamilyName = scimcore.InferNameParts(rec.DisplayName)
	return rec, nil
}

func sqliteUpsertSCIMExternalUser(ctx context.Context, tx *sql.Tx, orgID, userID, externalID, externalName string) error {
	orgID = strings.TrimSpace(orgID)
	userID = strings.TrimSpace(userID)
	externalID = strings.TrimSpace(externalID)
	externalName = strings.TrimSpace(externalName)
	if orgID == "" || userID == "" {
		return nil
	}
	if externalID == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM external_users WHERE org_id = ? AND user_id = ? AND provider = 'scim'`, orgID, userID)
		return err
	}

	var existingID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM external_users WHERE org_id = ? AND user_id = ? AND provider = 'scim' ORDER BY created_at DESC LIMIT 1`,
		orgID, userID,
	).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO external_users (id, org_id, user_id, provider, external_id, external_name, created_at)
			 VALUES (?, ?, ?, 'scim', ?, ?, ?)`,
			id.New(), orgID, userID, externalID, externalName, time.Now().UTC().Format(time.RFC3339),
		)
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE external_users SET external_id = ?, external_name = ? WHERE id = ?`,
		externalID, externalName, existingID,
	)
	return err
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
	case "externalid":
		return ` AND EXISTS (SELECT 1 FROM external_users eu WHERE eu.org_id = m.organization_id AND eu.user_id = u.id AND eu.provider = 'scim' AND eu.external_id = ?)`, []any{filter.Value}, true
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
