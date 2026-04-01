package blob

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

type Locator struct {
	ProjectID  string
	RecordType string
	RecordID   string
	ObjectKey  string
}

type Resolver struct {
	db    *sql.DB
	blobs store.BlobStore
}

func NewResolver(db *sql.DB, blobs store.BlobStore) Resolver {
	return Resolver{db: db, blobs: blobs}
}

func Attachment(projectID, attachmentID, objectKey string) Locator {
	return Locator{
		ProjectID:  strings.TrimSpace(projectID),
		RecordType: "attachment",
		RecordID:   strings.TrimSpace(attachmentID),
		ObjectKey:  strings.TrimSpace(objectKey),
	}
}

func DebugFile(projectID, fileID, objectKey string) Locator {
	return Locator{
		ProjectID:  strings.TrimSpace(projectID),
		RecordType: "debug_file",
		RecordID:   strings.TrimSpace(fileID),
		ObjectKey:  strings.TrimSpace(objectKey),
	}
}

func EventPayload(projectID, eventRowID, objectKey string) Locator {
	return Locator{
		ProjectID:  strings.TrimSpace(projectID),
		RecordType: "event",
		RecordID:   strings.TrimSpace(eventRowID),
		ObjectKey:  strings.TrimSpace(objectKey),
	}
}

func SourceMap(projectID, artifactID, objectKey string) Locator {
	return Locator{
		ProjectID:  strings.TrimSpace(projectID),
		RecordType: "source_map",
		RecordID:   strings.TrimSpace(artifactID),
		ObjectKey:  strings.TrimSpace(objectKey),
	}
}

func (r Resolver) Read(ctx context.Context, loc Locator) ([]byte, error) {
	if r.blobs == nil || strings.TrimSpace(loc.ObjectKey) == "" {
		return nil, store.ErrNotFound
	}
	body, err := r.blobs.Get(ctx, loc.ObjectKey)
	if err == nil {
		return body, nil
	}
	if restoreErr := r.restore(ctx, loc); restoreErr != nil {
		return nil, restoreErr
	}
	body, err = r.blobs.Get(ctx, loc.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("load blob %s: %w", loc.ObjectKey, err)
	}
	return body, nil
}

func (r Resolver) restore(ctx context.Context, loc Locator) error {
	if r.db == nil || strings.TrimSpace(loc.ProjectID) == "" || strings.TrimSpace(loc.RecordType) == "" || strings.TrimSpace(loc.RecordID) == "" {
		return store.ErrNotFound
	}
	var archiveKey string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(archive_key, '')
		 FROM telemetry_archives
		 WHERE project_id = ? AND record_type = ? AND record_id = ? AND restored_at IS NULL
		 ORDER BY archived_at DESC
		 LIMIT 1`,
		loc.ProjectID,
		loc.RecordType,
		loc.RecordID,
	).Scan(&archiveKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return store.ErrNotFound
		}
		return fmt.Errorf("load archived blob locator: %w", err)
	}
	archiveKey = strings.TrimSpace(archiveKey)
	if archiveKey == "" {
		return store.ErrNotFound
	}
	body, err := r.blobs.Get(ctx, archiveKey)
	if err != nil {
		return fmt.Errorf("load archived blob %s: %w", archiveKey, err)
	}
	if err := r.blobs.Put(ctx, loc.ObjectKey, body); err != nil {
		return fmt.Errorf("restore archived blob %s: %w", loc.ObjectKey, err)
	}
	_ = r.blobs.Delete(ctx, archiveKey)
	if _, err := r.db.ExecContext(ctx,
		`UPDATE telemetry_archives
		 SET restored_at = ?
		 WHERE project_id = ? AND record_type = ? AND record_id = ? AND archive_key = ? AND restored_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339),
		loc.ProjectID,
		loc.RecordType,
		loc.RecordID,
		archiveKey,
	); err != nil {
		return fmt.Errorf("mark archived blob restored: %w", err)
	}
	return nil
}
