package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"urgentry/internal/proguard"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

// ProGuardStore persists ProGuard mapping metadata in SQLite and bytes in a blob store.
type ProGuardStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

// NewProGuardStore creates a ProGuardStore.
func NewProGuardStore(db *sql.DB, blobs store.BlobStore) *ProGuardStore {
	return &ProGuardStore{db: db, blobs: blobs}
}

var _ proguard.Store = (*ProGuardStore)(nil)

// SaveMapping stores ProGuard mapping content and metadata.
func (s *ProGuardStore) SaveMapping(ctx context.Context, m *proguard.Mapping, data []byte) error {
	if m == nil {
		return errors.New("mapping is nil")
	}
	if s.blobs == nil {
		return errors.New("proguard blob store is not configured")
	}
	if m.ProjectID == "" || m.ReleaseID == "" {
		return errors.New("mapping project_id and release_id are required")
	}
	if m.UUID == "" {
		return errors.New("mapping uuid is required")
	}
	if m.ID == "" {
		m.ID = id.New()
	}
	if m.Name == "" {
		m.Name = "mapping.txt"
	}
	m.Size = int64(len(data))
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if err := ensureReleaseForOwner(ctx, s.db, m.ProjectID, m.ReleaseID); err != nil {
		return err
	}
	m.ObjectKey = proguardObjectKey(m.ProjectID, m.ReleaseID, m.UUID, m.Name)

	if err := s.blobs.Put(ctx, m.ObjectKey, data); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO debug_files
			(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at, kind, content_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'proguard', 'text/plain')`,
		m.ID, m.ProjectID, m.ReleaseID, m.UUID, m.CodeID, m.Name, m.ObjectKey, m.Size, m.Checksum,
		m.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		_ = s.blobs.Delete(ctx, m.ObjectKey)
	}
	return err
}

// GetMapping retrieves a mapping by ID.
func (s *ProGuardStore) GetMapping(ctx context.Context, id string) (*proguard.Mapping, []byte, error) {
	var m proguard.Mapping
	var objectKey string
	var createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at
		 FROM debug_files WHERE id = ? AND kind = 'proguard'`, id,
	).Scan(&m.ID, &m.ProjectID, &m.ReleaseID, &m.UUID, &m.CodeID, &m.Name, &objectKey, &m.Size, &m.Checksum, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	m.ObjectKey = objectKey
	if createdAt.Valid {
		m.CreatedAt = parseTime(createdAt.String)
	}

	data, err := s.blobs.Get(ctx, objectKey)
	if err != nil {
		return &m, nil, nil
	}
	return &m, data, nil
}

// LookupByUUID retrieves the newest mapping for a project and ProGuard UUID.
func (s *ProGuardStore) LookupByUUID(ctx context.Context, projectID, releaseVersion, uuid string) (*proguard.Mapping, []byte, error) {
	var m proguard.Mapping
	var objectKey string
	var createdAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at
		 FROM debug_files WHERE project_id = ? AND release_version = ? AND uuid = ? AND kind = 'proguard'
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		projectID, releaseVersion, uuid,
	).Scan(&m.ID, &m.ProjectID, &m.ReleaseID, &m.UUID, &m.CodeID, &m.Name, &objectKey, &m.Size, &m.Checksum, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	m.ObjectKey = objectKey
	if createdAt.Valid {
		m.CreatedAt = parseTime(createdAt.String)
	}

	data, err := s.blobs.Get(ctx, objectKey)
	if err != nil {
		return &m, nil, nil
	}
	return &m, data, nil
}

// ListByRelease returns all mappings for a project + release version.
func (s *ProGuardStore) ListByRelease(ctx context.Context, projectID, releaseVersion string) ([]*proguard.Mapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at
		 FROM debug_files WHERE project_id = ? AND release_version = ? AND kind = 'proguard'
		 ORDER BY created_at DESC, id DESC`,
		projectID, releaseVersion,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*proguard.Mapping
	for rows.Next() {
		var m proguard.Mapping
		var objectKey string
		var createdAt sql.NullString
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.ReleaseID, &m.UUID, &m.CodeID, &m.Name, &objectKey, &m.Size, &m.Checksum, &createdAt); err != nil {
			return nil, err
		}
		m.ObjectKey = objectKey
		if createdAt.Valid {
			m.CreatedAt = parseTime(createdAt.String)
		}
		result = append(result, &m)
	}
	return result, rows.Err()
}

func proguardObjectKey(projectID, releaseVersion, uuid, name string) string {
	return fmt.Sprintf("proguard/%s/%s/%s/%s",
		sanitizeKeySegment(projectID),
		sanitizeKeySegment(releaseVersion),
		sanitizeKeySegment(uuid),
		sanitizeKeySegment(name),
	)
}
