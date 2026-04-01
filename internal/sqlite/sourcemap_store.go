package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/sourcemap"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

// Compile-time check that SourceMapStore implements sourcemap.Store.
var _ sourcemap.Store = (*SourceMapStore)(nil)

// SourceMapStore persists source map artifacts in SQLite metadata + blob store.
type SourceMapStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

// NewSourceMapStore creates a SourceMapStore.
func NewSourceMapStore(db *sql.DB, blobs store.BlobStore) *SourceMapStore {
	return &SourceMapStore{db: db, blobs: blobs}
}

// SaveArtifact stores a source map file. Metadata goes into the artifacts table,
// content goes into the blob store.
func (s *SourceMapStore) SaveArtifact(ctx context.Context, a *sourcemap.Artifact, data []byte) error {
	if a.ID == "" {
		a.ID = id.New()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if err := ensureReleaseForOwner(ctx, s.db, a.ProjectID, a.ReleaseID); err != nil {
		return err
	}
	blobKey := fmt.Sprintf("sourcemaps/%s/%s/%s", a.ProjectID, a.ReleaseID, a.Name)
	a.ObjectKey = blobKey

	if err := s.blobs.Put(ctx, blobKey, data); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO artifacts (id, project_id, release_version, name, object_key, size, checksum, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ProjectID, a.ReleaseID, a.Name, blobKey, len(data), nullable(a.Checksum),
		a.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetArtifact retrieves an artifact by ID.
func (s *SourceMapStore) GetArtifact(ctx context.Context, artifactID string) (*sourcemap.Artifact, []byte, error) {
	var art sourcemap.Artifact
	var blobKey string
	var size int64
	var checksum, createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, release_version, name, object_key, size, checksum, created_at
		 FROM artifacts WHERE id = ?`, artifactID,
	).Scan(&art.ID, &art.ProjectID, &art.ReleaseID, &art.Name, &blobKey, &size, &checksum, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	art.ObjectKey = blobKey
	art.Size = size
	art.Checksum = nullStr(checksum)
	if createdAt.Valid {
		art.CreatedAt = parseTime(createdAt.String)
	}

	data, err := s.blobs.Get(ctx, blobKey)
	if err != nil {
		return &art, nil, nil // metadata exists, blob missing
	}
	return &art, data, nil
}

// ListByRelease returns all artifacts for a project + release version.
func (s *SourceMapStore) ListByRelease(ctx context.Context, projectID, releaseVersion string) ([]*sourcemap.Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, release_version, name, object_key, size, checksum, created_at
		 FROM artifacts WHERE project_id = ? AND release_version = ?
		 ORDER BY created_at DESC`,
		projectID, releaseVersion,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*sourcemap.Artifact
	for rows.Next() {
		var art sourcemap.Artifact
		var blobKey string
		var size int64
		var checksum, createdAt sql.NullString
		if err := rows.Scan(&art.ID, &art.ProjectID, &art.ReleaseID, &art.Name, &blobKey, &size, &checksum, &createdAt); err != nil {
			return nil, err
		}
		art.ObjectKey = blobKey
		art.Size = size
		art.Checksum = nullStr(checksum)
		if createdAt.Valid {
			art.CreatedAt = parseTime(createdAt.String)
		}
		result = append(result, &art)
	}
	return result, rows.Err()
}

// DeleteArtifact removes an artifact by ID.
func (s *SourceMapStore) DeleteArtifact(ctx context.Context, artifactID string) error {
	// Get blob key first so we can clean up the blob.
	var blobKey string
	err := s.db.QueryRowContext(ctx,
		`SELECT object_key FROM artifacts WHERE id = ?`, artifactID,
	).Scan(&blobKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	_, err = s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE id = ?`, artifactID)
	if err != nil {
		return err
	}

	// Best-effort blob deletion.
	_ = s.blobs.Delete(ctx, blobKey)
	return nil
}

// LookupByName retrieves an artifact by (project, release, name) triple.
func (s *SourceMapStore) LookupByName(ctx context.Context, projectID, releaseVersion, name string) (*sourcemap.Artifact, []byte, error) {
	var art sourcemap.Artifact
	var blobKey string
	var size int64
	var checksum, createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, release_version, name, object_key, size, checksum, created_at
		 FROM artifacts WHERE project_id = ? AND release_version = ? AND name = ?`,
		projectID, releaseVersion, name,
	).Scan(&art.ID, &art.ProjectID, &art.ReleaseID, &art.Name, &blobKey, &size, &checksum, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	art.ObjectKey = blobKey
	art.Size = size
	art.Checksum = nullStr(checksum)
	if createdAt.Valid {
		art.CreatedAt = parseTime(createdAt.String)
	}

	data, err := s.blobs.Get(ctx, blobKey)
	if err != nil {
		return nil, nil, nil
	}
	return &art, data, nil
}

// Upload is a convenience method that wraps SaveArtifact.
func (s *SourceMapStore) Upload(ctx context.Context, projectID, release, filename string, data []byte) error {
	art := &sourcemap.Artifact{
		ID:        id.New(),
		ProjectID: projectID,
		ReleaseID: release,
		Name:      filename,
		Size:      int64(len(data)),
		CreatedAt: time.Now().UTC(),
	}
	return s.SaveArtifact(ctx, art, data)
}

// Lookup retrieves the source map content for a given project, release, and filename.
// Returns nil, nil if not found.
func (s *SourceMapStore) Lookup(ctx context.Context, projectID, release, filename string) ([]byte, error) {
	_, data, err := s.LookupByName(ctx, projectID, release, filename)
	return data, err
}

// SaveOrgArtifact stores a release file scoped to an organization (not a project).
func (s *SourceMapStore) SaveOrgArtifact(ctx context.Context, a *sourcemap.Artifact, data []byte) error {
	if a.ID == "" {
		a.ID = id.New()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	blobKey := fmt.Sprintf("release-files/%s/%s/%s", a.OrganizationID, a.ReleaseID, a.Name)
	a.ObjectKey = blobKey

	if err := s.blobs.Put(ctx, blobKey, data); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO artifacts (id, organization_id, release_version, name, object_key, size, checksum, created_at, project_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '')`,
		a.ID, a.OrganizationID, a.ReleaseID, a.Name, blobKey, len(data), nullable(a.Checksum),
		a.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// ListByOrgRelease returns all artifacts for an organization + release version.
func (s *SourceMapStore) ListByOrgRelease(ctx context.Context, orgID, releaseVersion string) ([]*sourcemap.Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(organization_id, ''), release_version, name, object_key, size, checksum, created_at
		 FROM artifacts WHERE organization_id = ? AND release_version = ?
		 ORDER BY created_at DESC`,
		orgID, releaseVersion,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*sourcemap.Artifact
	for rows.Next() {
		var art sourcemap.Artifact
		var blobKey string
		var size int64
		var checksum, createdAt sql.NullString
		if err := rows.Scan(&art.ID, &art.OrganizationID, &art.ReleaseID, &art.Name, &blobKey, &size, &checksum, &createdAt); err != nil {
			return nil, err
		}
		art.ObjectKey = blobKey
		art.Size = size
		art.Checksum = nullStr(checksum)
		if createdAt.Valid {
			art.CreatedAt = parseTime(createdAt.String)
		}
		result = append(result, &art)
	}
	return result, rows.Err()
}

// GetOrgArtifact retrieves an artifact by ID, verifying it belongs to the given org and release.
func (s *SourceMapStore) GetOrgArtifact(ctx context.Context, orgID, releaseVersion, artifactID string) (*sourcemap.Artifact, error) {
	var art sourcemap.Artifact
	var blobKey string
	var size int64
	var checksum, createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(organization_id, ''), release_version, name, object_key, size, checksum, created_at
		 FROM artifacts WHERE id = ? AND organization_id = ? AND release_version = ?`,
		artifactID, orgID, releaseVersion,
	).Scan(&art.ID, &art.OrganizationID, &art.ReleaseID, &art.Name, &blobKey, &size, &checksum, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	art.ObjectKey = blobKey
	art.Size = size
	art.Checksum = nullStr(checksum)
	if createdAt.Valid {
		art.CreatedAt = parseTime(createdAt.String)
	}
	return &art, nil
}

// UpdateArtifactName updates the name of an artifact.
func (s *SourceMapStore) UpdateArtifactName(ctx context.Context, orgID, releaseVersion, artifactID, newName string) (*sourcemap.Artifact, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE artifacts SET name = ? WHERE id = ? AND organization_id = ? AND release_version = ?`,
		newName, artifactID, orgID, releaseVersion,
	)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, nil
	}
	return s.GetOrgArtifact(ctx, orgID, releaseVersion, artifactID)
}

// DeleteOrgArtifact removes an artifact by ID after verifying org+release ownership.
func (s *SourceMapStore) DeleteOrgArtifact(ctx context.Context, orgID, releaseVersion, artifactID string) error {
	var blobKey string
	err := s.db.QueryRowContext(ctx,
		`SELECT object_key FROM artifacts WHERE id = ? AND organization_id = ? AND release_version = ?`,
		artifactID, orgID, releaseVersion,
	).Scan(&blobKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`DELETE FROM artifacts WHERE id = ? AND organization_id = ? AND release_version = ?`,
		artifactID, orgID, releaseVersion,
	)
	if err != nil {
		return err
	}

	_ = s.blobs.Delete(ctx, blobKey)
	return nil
}
