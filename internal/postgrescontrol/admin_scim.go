package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	scimcore "urgentry/internal/scim"
)

var _ scimcore.UserStore = (*AdminStore)(nil)

func (s *AdminStore) ListUsers(ctx context.Context, orgID string, startIndex, count int, filter string) ([]scimcore.UserRecord, int, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return []scimcore.UserRecord{}, 0, nil
	}

	where, args, ok := postgresSCIMFilter(filter)
	if !ok {
		return []scimcore.UserRecord{}, 0, nil
	}

	countArgs := append([]any{orgID}, args...)
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = $1`+where,
		countArgs...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	limitPlaceholder := 2 + len(args)
	offsetPlaceholder := 3 + len(args)
	rowsArgs := append(append([]any{orgID}, args...), count, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.is_active, u.created_at, u.updated_at
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = $1`+where+`
		  ORDER BY u.created_at ASC, u.id ASC
		  LIMIT $`+fmt.Sprint(limitPlaceholder)+` OFFSET $`+fmt.Sprint(offsetPlaceholder),
		rowsArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := make([]scimcore.UserRecord, 0, count)
	for rows.Next() {
		rec, err := scanPostgresSCIMUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, rec)
	}
	return users, total, rows.Err()
}

func (s *AdminStore) GetUser(ctx context.Context, orgID, userID string) (*scimcore.UserRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.is_active, u.created_at, u.updated_at
		   FROM organization_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.organization_id = $1 AND u.id = $2`,
		strings.TrimSpace(orgID), strings.TrimSpace(userID),
	)
	rec, err := scanPostgresSCIMUser(row)
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

	if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id = $1`, orgID).Scan(new(string)); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("organization not found")
		}
		return nil, err
	}

	userID, membershipExists, err := postgresLookupSCIMUser(ctx, tx, orgID, user.Email)
	if err != nil {
		return nil, err
	}
	if membershipExists {
		return nil, ErrDuplicateRecord
	}

	now := time.Now().UTC()
	if userID == "" {
		userID = generateID()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $5)`,
			userID, user.Email, user.DisplayName, user.Active, now,
		); isDuplicateKeyError(err) {
			return nil, ErrDuplicateRecord
		} else if err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users
			    SET display_name = $1, is_active = $2, updated_at = $3
			  WHERE id = $4`,
			user.DisplayName, user.Active, now, userID,
		); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'member', $4)`,
		generateID(), orgID, userID, now,
	); isDuplicateKeyError(err) {
		return nil, ErrDuplicateRecord
	} else if err != nil {
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
		    SET email = $1, display_name = $2, is_active = $3, updated_at = $4
		  WHERE id = $5`,
		updated.Email, updated.DisplayName, updated.Active, time.Now().UTC(), updated.ID,
	); isDuplicateKeyError(err) {
		return nil, ErrDuplicateRecord
	} else if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, orgID, userID)
}

type postgresSCIMScanner interface {
	Scan(dest ...any) error
}

func scanPostgresSCIMUser(scanner postgresSCIMScanner) (scimcore.UserRecord, error) {
	var rec scimcore.UserRecord
	if err := scanner.Scan(&rec.ID, &rec.Email, &rec.DisplayName, &rec.Active, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return scimcore.UserRecord{}, err
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	rec.UpdatedAt = rec.UpdatedAt.UTC()
	rec.GivenName, rec.FamilyName = scimcore.InferNameParts(rec.DisplayName)
	return rec, nil
}

func postgresLookupSCIMUser(ctx context.Context, tx *sql.Tx, orgID, email string) (userID string, membershipExists bool, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT u.id,
		        EXISTS(
		            SELECT 1
		              FROM organization_members m
		             WHERE m.organization_id = $1 AND m.user_id = u.id
		        )
		   FROM users u
		  WHERE lower(u.email) = lower($2)`,
		orgID, email,
	).Scan(&userID, &membershipExists)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return userID, membershipExists, err
}

func postgresSCIMFilter(raw string) (string, []any, bool) {
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
		return ` AND u.id = $2`, []any{filter.Value}, true
	case "username", "email":
		return ` AND lower(u.email) = lower($2)`, []any{filter.Value}, true
	case "displayname":
		return ` AND u.display_name = $2`, []any{filter.Value}, true
	default:
		return "", nil, false
	}
}
