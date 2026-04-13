package sqlite

import (
	"context"
	"database/sql"
	"strings"

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

func UpdateOrganization(ctx context.Context, db *sql.DB, slug string, update store.OrganizationUpdate) (*store.Organization, error) {
	org, err := GetOrganization(ctx, db, slug)
	if err != nil || org == nil {
		return nil, err
	}
	newName := strings.TrimSpace(update.Name)
	newSlug := strings.TrimSpace(update.Slug)
	if newName == "" {
		newName = org.Name
	}
	if newSlug == "" {
		newSlug = org.Slug
	}
	_, err = db.ExecContext(ctx,
		`UPDATE organizations SET name = ?, slug = ? WHERE id = ?`,
		newName, newSlug, org.ID,
	)
	if err != nil {
		return nil, err
	}
	return GetOrganization(ctx, db, newSlug)
}

func ListOrgEnvironments(ctx context.Context, db *sql.DB, orgSlug string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT e.environment FROM events e
		 JOIN projects p ON p.id = e.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND e.environment IS NOT NULL AND e.environment != ''
		 UNION
		 SELECT DISTINCT t.environment FROM transactions t
		 JOIN projects p ON p.id = t.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND t.environment IS NOT NULL AND t.environment != ''
		 ORDER BY 1`,
		orgSlug, orgSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var envs []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
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
		`SELECT k.id, k.project_id, k.label, k.public_key, k.secret_key, k.status, COALESCE(k.rate_limit, 0), k.created_at
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
	return scanSQLiteKeyRows(rows)
}

func ListAllProjectKeys(ctx context.Context, db *sql.DB) ([]store.ProjectKeyMeta, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, label, public_key, secret_key, status, COALESCE(rate_limit, 0), created_at
		 FROM project_keys
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteKeyRows(rows)
}

func GetProjectKey(ctx context.Context, db *sql.DB, orgSlug, projectSlug, keyID string) (*store.ProjectKeyMeta, error) {
	row := db.QueryRowContext(ctx,
		`SELECT k.id, k.project_id, k.label, k.public_key, k.secret_key, k.status, COALESCE(k.rate_limit, 0), k.created_at
		 FROM project_keys k
		 JOIN projects p ON p.id = k.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND p.slug = ? AND k.id = ?`,
		orgSlug, projectSlug, keyID,
	)
	var rec store.ProjectKeyMeta
	var label, publicKey, secretKey, status, createdAt sql.NullString
	if err := row.Scan(&rec.ID, &rec.ProjectID, &label, &publicKey, &secretKey, &status, &rec.RateLimit, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.Label = sqlutil.NullStr(label)
	rec.PublicKey = sqlutil.NullStr(publicKey)
	rec.SecretKey = sqlutil.NullStr(secretKey)
	rec.Status = sqlutil.NullStr(status)
	rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	return &rec, nil
}

func UpdateProjectKey(ctx context.Context, db *sql.DB, orgSlug, projectSlug, keyID string, update store.ProjectKeyUpdate) (*store.ProjectKeyMeta, error) {
	existing, err := GetProjectKey(ctx, db, orgSlug, projectSlug, keyID)
	if err != nil || existing == nil {
		return existing, err
	}
	if update.Name != "" {
		existing.Label = update.Name
	}
	if update.IsActive != nil {
		if *update.IsActive {
			existing.Status = "active"
		} else {
			existing.Status = "disabled"
		}
	}
	if update.RateLimit != nil {
		existing.RateLimit = *update.RateLimit
	}
	_, err = db.ExecContext(ctx,
		`UPDATE project_keys SET label = ?, status = ?, rate_limit = ? WHERE id = ?`,
		existing.Label, existing.Status, existing.RateLimit, existing.ID,
	)
	if err != nil {
		return nil, err
	}
	return existing, nil
}

func DeleteProjectKey(ctx context.Context, db *sql.DB, orgSlug, projectSlug, keyID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM project_keys
		 WHERE id = ? AND project_id IN (
		   SELECT p.id FROM projects p
		   JOIN organizations o ON o.id = p.organization_id
		   WHERE o.slug = ? AND p.slug = ?
		 )`,
		keyID, orgSlug, projectSlug,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListProjectEnvironments derives environments from events/transactions for
// a project and merges with the project_environments table for visibility.
func ListProjectEnvironments(ctx context.Context, db *sql.DB, projectID string) ([]store.ProjectEnvironment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT e.environment FROM events e
		 WHERE e.project_id = ? AND e.environment IS NOT NULL AND e.environment != ''
		 UNION
		 SELECT DISTINCT t.environment FROM transactions t
		 WHERE t.project_id = ? AND t.environment IS NOT NULL AND t.environment != ''
		 ORDER BY 1`,
		projectID, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		envNames = append(envNames, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load explicit visibility state.
	hiddenSet := make(map[string]bool)
	visRows, err := db.QueryContext(ctx,
		`SELECT name, is_hidden FROM project_environments WHERE project_id = ?`, projectID)
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return nil, err
	}
	if visRows != nil {
		defer visRows.Close()
		for visRows.Next() {
			var name string
			var hidden int
			if err := visRows.Scan(&name, &hidden); err != nil {
				return nil, err
			}
			hiddenSet[name] = hidden != 0
		}
		if err := visRows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]store.ProjectEnvironment, 0, len(envNames))
	for _, name := range envNames {
		out = append(out, store.ProjectEnvironment{
			Name:     name,
			IsHidden: hiddenSet[name],
		})
	}
	return out, nil
}

// GetProjectEnvironment returns a single environment for a project.
func GetProjectEnvironment(ctx context.Context, db *sql.DB, projectID, envName string) (*store.ProjectEnvironment, error) {
	// Check if the environment exists in events or transactions.
	var found int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (
		   SELECT 1 FROM events WHERE project_id = ? AND environment = ? LIMIT 1
		   UNION ALL
		   SELECT 1 FROM transactions WHERE project_id = ? AND environment = ? LIMIT 1
		 )`,
		projectID, envName, projectID, envName,
	).Scan(&found)
	if err != nil {
		return nil, err
	}
	if found == 0 {
		return nil, nil
	}

	var hidden int
	err = db.QueryRowContext(ctx,
		`SELECT is_hidden FROM project_environments WHERE project_id = ? AND name = ?`,
		projectID, envName,
	).Scan(&hidden)
	if err != nil && err != sql.ErrNoRows && !strings.Contains(err.Error(), "no such table") {
		return nil, err
	}

	return &store.ProjectEnvironment{Name: envName, IsHidden: hidden != 0}, nil
}

// UpdateProjectEnvironment upserts the isHidden flag for a project environment.
func UpdateProjectEnvironment(ctx context.Context, db *sql.DB, projectID, envName string, isHidden bool) (*store.ProjectEnvironment, error) {
	hiddenVal := 0
	if isHidden {
		hiddenVal = 1
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO project_environments (project_id, name, is_hidden) VALUES (?, ?, ?)
		 ON CONFLICT (project_id, name) DO UPDATE SET is_hidden = excluded.is_hidden`,
		projectID, envName, hiddenVal,
	)
	if err != nil {
		return nil, err
	}
	return &store.ProjectEnvironment{Name: envName, IsHidden: isHidden}, nil
}

// ListProjectTeams returns teams associated with a project via the project_teams
// join table, falling back to the single team_id FK on the projects table.
func ListProjectTeams(ctx context.Context, db *sql.DB, projectID string) ([]store.Team, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT t.id, t.slug, t.name, t.organization_id, t.created_at
		 FROM teams t
		 JOIN project_teams pt ON pt.team_id = t.id
		 WHERE pt.project_id = ?
		 ORDER BY t.created_at ASC`,
		projectID,
	)
	if err != nil && strings.Contains(err.Error(), "no such table") {
		// Fall back to legacy single FK.
		return listProjectTeamsFallback(ctx, db, projectID)
	}
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		// Include legacy single team_id if no join-table rows.
		return listProjectTeamsFallback(ctx, db, projectID)
	}
	return out, nil
}

func listProjectTeamsFallback(ctx context.Context, db *sql.DB, projectID string) ([]store.Team, error) {
	row := db.QueryRowContext(ctx,
		`SELECT t.id, t.slug, t.name, t.organization_id, t.created_at
		 FROM teams t
		 JOIN projects p ON p.team_id = t.id
		 WHERE p.id = ?`,
		projectID,
	)
	var rec store.Team
	var createdAt sql.NullString
	if err := row.Scan(&rec.ID, &rec.Slug, &rec.Name, &rec.OrgID, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return []store.Team{}, nil
		}
		return nil, err
	}
	rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	return []store.Team{rec}, nil
}

// AddProjectTeam associates a team with a project.
func AddProjectTeam(ctx context.Context, db *sql.DB, orgSlug, projectID, teamSlug string) (*store.Team, error) {
	row := db.QueryRowContext(ctx,
		`SELECT t.id, t.slug, t.name, t.organization_id, t.created_at
		 FROM teams t
		 JOIN organizations o ON o.id = t.organization_id
		 WHERE o.slug = ? AND t.slug = ?`,
		orgSlug, teamSlug,
	)
	var rec store.Team
	var createdAt sql.NullString
	if err := row.Scan(&rec.ID, &rec.Slug, &rec.Name, &rec.OrgID, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.DateCreated = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))

	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO project_teams (project_id, team_id) VALUES (?, ?)`,
		projectID, rec.ID,
	)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// RemoveProjectTeam removes a team association from a project.
func RemoveProjectTeam(ctx context.Context, db *sql.DB, orgSlug, projectID, teamSlug string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM project_teams
		 WHERE project_id = ? AND team_id IN (
		   SELECT t.id FROM teams t
		   JOIN organizations o ON o.id = t.organization_id
		   WHERE o.slug = ? AND t.slug = ?
		 )`,
		projectID, orgSlug, teamSlug,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func scanSQLiteKeyRows(rows *sql.Rows) ([]store.ProjectKeyMeta, error) {
	var out []store.ProjectKeyMeta
	for rows.Next() {
		var rec store.ProjectKeyMeta
		var label, publicKey, secretKey, status, createdAt sql.NullString
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &label, &publicKey, &secretKey, &status, &rec.RateLimit, &createdAt); err != nil {
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
