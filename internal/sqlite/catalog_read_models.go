package sqlite

import (
	"context"
	"database/sql"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

func ListOrganizations(ctx context.Context, db *sql.DB) ([]store.Organization, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, slug, name, created_at FROM organizations ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.Organization
	for rows.Next() {
		var rec store.Organization
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Name, &createdAt); err != nil {
			return nil, err
		}
		rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		out = append(out, rec)
	}
	return out, rows.Err()
}

func GetOrganization(ctx context.Context, db *sql.DB, slug string) (*store.Organization, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, slug, name, created_at FROM organizations WHERE slug = ?`,
		slug,
	)
	var rec store.Organization
	var createdAt sql.NullString
	if err := row.Scan(&rec.ID, &rec.Slug, &rec.Name, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	return &rec, nil
}

func ListProjects(ctx context.Context, db *sql.DB, orgSlug string) ([]store.Project, error) {
	query := `SELECT p.id, p.slug, p.name, p.platform, p.status, p.event_retention_days, p.attachment_retention_days, p.debug_file_retention_days, p.created_at, o.slug, COALESCE(t.slug, '')
	          FROM projects p
	          JOIN organizations o ON o.id = p.organization_id
	          LEFT JOIN teams t ON t.id = p.team_id`
	args := []any{}
	if orgSlug != "" {
		query += ` WHERE o.slug = ?`
		args = append(args, orgSlug)
	}
	query += ` ORDER BY p.created_at ASC`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.Project
	for rows.Next() {
		var rec store.Project
		var platform, status, createdAt, orgValue, teamSlug sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Name, &platform, &status, &rec.EventRetentionDays, &rec.AttachRetentionDays, &rec.DebugRetentionDays, &createdAt, &orgValue, &teamSlug); err != nil {
			return nil, err
		}
		rec.Platform = sqlutil.NullStr(platform)
		rec.Status = sqlutil.NullStr(status)
		rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		rec.OrgSlug = sqlutil.NullStr(orgValue)
		rec.TeamSlug = sqlutil.NullStr(teamSlug)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func GetProject(ctx context.Context, db *sql.DB, orgSlug, projectSlug string) (*store.Project, error) {
	row := db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, p.name, p.platform, p.status, p.event_retention_days, p.attachment_retention_days, p.debug_file_retention_days, p.created_at, o.slug, COALESCE(t.slug, '')
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN teams t ON t.id = p.team_id
		 WHERE o.slug = ? AND p.slug = ?`,
		orgSlug, projectSlug,
	)
	var rec store.Project
	var platform, status, createdAt, orgValue, teamSlug sql.NullString
	if err := row.Scan(&rec.ID, &rec.Slug, &rec.Name, &platform, &status, &rec.EventRetentionDays, &rec.AttachRetentionDays, &rec.DebugRetentionDays, &createdAt, &orgValue, &teamSlug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.Platform = sqlutil.NullStr(platform)
	rec.Status = sqlutil.NullStr(status)
	rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	rec.OrgSlug = sqlutil.NullStr(orgValue)
	rec.TeamSlug = sqlutil.NullStr(teamSlug)
	return &rec, nil
}

func ListTeams(ctx context.Context, db *sql.DB, orgSlug string) ([]store.Team, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT t.id, t.slug, t.name, t.organization_id, t.created_at
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

	var out []store.Team
	for rows.Next() {
		var rec store.Team
		var createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Name, &rec.OrgID, &createdAt); err != nil {
			return nil, err
		}
		rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		out = append(out, rec)
	}
	return out, rows.Err()
}

func ListProjectKeys(ctx context.Context, db *sql.DB, orgSlug, projectSlug string) ([]store.ProjectKeyMeta, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT k.id, k.project_id, k.label, k.public_key, k.secret_key, k.status, k.created_at
		 FROM project_keys k
		 JOIN projects p ON p.id = k.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND p.slug = ?
		 ORDER BY k.created_at ASC`,
		orgSlug, projectSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.ProjectKeyMeta
	for rows.Next() {
		var rec store.ProjectKeyMeta
		var label, publicKey, secretKey, status, createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &label, &publicKey, &secretKey, &status, &createdAt); err != nil {
			return nil, err
		}
		rec.Label = sqlutil.NullStr(label)
		rec.PublicKey = sqlutil.NullStr(publicKey)
		rec.SecretKey = sqlutil.NullStr(secretKey)
		rec.Status = sqlutil.NullStr(status)
		rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		out = append(out, rec)
	}
	return out, rows.Err()
}

func ListAllProjectKeys(ctx context.Context, db *sql.DB) ([]store.ProjectKeyMeta, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, label, public_key, secret_key, status, created_at
		 FROM project_keys
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []store.ProjectKeyMeta
	for rows.Next() {
		var rec store.ProjectKeyMeta
		var label, publicKey, secretKey, status, createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &label, &publicKey, &secretKey, &status, &createdAt); err != nil {
			return nil, err
		}
		rec.Label = sqlutil.NullStr(label)
		rec.PublicKey = sqlutil.NullStr(publicKey)
		rec.SecretKey = sqlutil.NullStr(secretKey)
		rec.Status = sqlutil.NullStr(status)
		rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		out = append(out, rec)
	}
	return out, rows.Err()
}
