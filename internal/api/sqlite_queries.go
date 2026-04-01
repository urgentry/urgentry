package api

import (
	"context"
	"database/sql"
	"net/http"

	"urgentry/internal/controlplane"
	sharedstore "urgentry/internal/store"
)

func getOrganizationFromDB(r *http.Request, db *sql.DB, slug string) (*Organization, error) {
	if catalog := catalogFromRequest(r); catalog != nil {
		org, err := catalog.GetOrganization(r.Context(), slug)
		if err != nil || org == nil {
			return org, err
		}
		if err := ensureSQLiteOrganizationShadow(r.Context(), db, *org); err != nil {
			return nil, err
		}
		return org, nil
	}
	return controlplane.SQLiteServices(db).Catalog.GetOrganization(r.Context(), slug)
}

func projectIDFromSlugs(r *http.Request, db *sql.DB, orgSlug, projectSlug string) (string, error) {
	if catalog := catalogFromRequest(r); catalog != nil {
		org, err := catalog.GetOrganization(r.Context(), orgSlug)
		if err != nil || org == nil {
			return "", err
		}
		project, err := catalog.GetProject(r.Context(), orgSlug, projectSlug)
		if err != nil || project == nil {
			return "", err
		}
		if err := ensureSQLiteProjectShadow(r.Context(), db, *org, *project); err != nil {
			return "", err
		}
		return project.ID, nil
	}
	var projectID string
	err := db.QueryRowContext(r.Context(),
		`SELECT p.id
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND p.slug = ?`,
		orgSlug, projectSlug,
	).Scan(&projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return projectID, nil
}

func ensureSQLiteOrganizationShadow(ctx context.Context, db *sql.DB, org sharedstore.Organization) error {
	if db == nil || org.ID == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES (?, ?, ?)`,
		org.ID, org.Slug, org.Name,
	)
	return err
}

func ensureSQLiteProjectShadow(ctx context.Context, db *sql.DB, org sharedstore.Organization, project sharedstore.Project) error {
	if err := ensureSQLiteOrganizationShadow(ctx, db, org); err != nil {
		return err
	}
	if db == nil || project.ID == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		project.ID, org.ID, project.Slug, project.Name, project.Platform, project.Status,
	)
	return err
}
