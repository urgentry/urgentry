package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/store"
)

type activeTelemetryArchive struct {
	ArchiveKey string
}

func latestActiveTelemetryArchive(ctx context.Context, db *sql.DB, projectID, recordType, recordID string) (*activeTelemetryArchive, error) {
	var archive activeTelemetryArchive
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(archive_key, '')
		 FROM telemetry_archives
		 WHERE project_id = ? AND record_type = ? AND record_id = ? AND restored_at IS NULL
		 ORDER BY archived_at DESC
		 LIMIT 1`,
		projectID, recordType, recordID,
	).Scan(&archive.ArchiveKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load telemetry archive: %w", err)
	}
	return &archive, nil
}

func restoreArchivedBlob(ctx context.Context, db *sql.DB, blobs store.BlobStore, projectID, recordType, recordID, objectKey string) error {
	if blobs == nil || objectKey == "" {
		return nil
	}
	archive, err := latestActiveTelemetryArchive(ctx, db, projectID, recordType, recordID)
	if err != nil {
		return err
	}
	if archive == nil || archive.ArchiveKey == "" {
		return store.ErrNotFound
	}
	body, err := blobs.Get(ctx, archive.ArchiveKey)
	if err != nil {
		return fmt.Errorf("load archived blob: %w", err)
	}
	if err := blobs.Put(ctx, objectKey, body); err != nil {
		return fmt.Errorf("restore archived blob: %w", err)
	}
	_ = blobs.Delete(ctx, archive.ArchiveKey)
	_, err = db.ExecContext(ctx,
		`UPDATE telemetry_archives
		 SET restored_at = ?
		 WHERE project_id = ? AND record_type = ? AND record_id = ? AND archive_key = ? AND restored_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339),
		projectID,
		recordType,
		recordID,
		archive.ArchiveKey,
	)
	if err != nil {
		return fmt.Errorf("mark telemetry archive restored: %w", err)
	}
	return nil
}

func telemetryArchiveBlobKey(projectID string, surface store.TelemetrySurface, recordType, recordID string) string {
	return "archives/" + sanitizeKeySegment(projectID) + "/" + string(surface) + "/" + sanitizeKeySegment(recordType) + "/" + sanitizeKeySegment(recordID)
}
