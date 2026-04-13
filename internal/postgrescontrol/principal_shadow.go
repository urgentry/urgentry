package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"

	"urgentry/internal/auth"
)

func ListActiveUsers(ctx context.Context, db *sql.DB) ([]auth.User, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, email, display_name
		 FROM users
		 WHERE is_active = TRUE
		 ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list active users: %w", err)
	}
	defer rows.Close()

	var users []auth.User
	for rows.Next() {
		var user auth.User
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName); err != nil {
			return nil, fmt.Errorf("scan active user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active users: %w", err)
	}
	return users, nil
}

func ListOrganizationMembers(ctx context.Context, db *sql.DB) ([]OrgMemberRecord, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.organization_id, o.slug, m.user_id, u.email, u.display_name, m.role, m.created_at
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 JOIN users u ON u.id = m.user_id
		 WHERE u.is_active = TRUE
		 ORDER BY m.created_at ASC, m.id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	defer rows.Close()

	var items []OrgMemberRecord
	for rows.Next() {
		var rec OrgMemberRecord
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
			return nil, fmt.Errorf("scan organization member: %w", err)
		}
		items = append(items, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization members: %w", err)
	}
	return items, nil
}
