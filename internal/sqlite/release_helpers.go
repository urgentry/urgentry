package sqlite

import (
	"context"
	"database/sql"
	"strings"
)

func ensureReleaseForOwner(ctx context.Context, db *sql.DB, ownerID, version string) error {
	ownerID = strings.TrimSpace(ownerID)
	version = strings.TrimSpace(version)
	if ownerID == "" || version == "" {
		return nil
	}

	orgID, err := releaseOwnerOrganizationID(ctx, db, ownerID)
	if err != nil || orgID == "" {
		return err
	}
	res, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO releases (id, organization_id, version)
		 VALUES (?, ?, ?)`,
		generateID(), orgID, version,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		return err
	}
	return bindResolvedNextReleaseIssues(ctx, db, orgID, version)
}

func releaseOwnerOrganizationID(ctx context.Context, db *sql.DB, ownerID string) (string, error) {
	var orgID sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(
			(SELECT id FROM organizations WHERE id = ?),
			(SELECT organization_id FROM projects WHERE id = ?)
		)`,
		ownerID, ownerID,
	).Scan(&orgID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(orgID.String), nil
}

func bindResolvedNextReleaseIssues(ctx context.Context, db *sql.DB, orgID, version string) error {
	orgID = strings.TrimSpace(orgID)
	version = strings.TrimSpace(version)
	if orgID == "" || version == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`UPDATE groups
		    SET resolved_in_release = ?
		  WHERE status = 'resolved'
		    AND COALESCE(resolution_substatus, '') = 'next_release'
		    AND COALESCE(resolved_in_release, '') = ''
		    AND project_id IN (
		        SELECT id FROM projects WHERE organization_id = ?
		    )`,
		version, orgID,
	)
	return err
}
