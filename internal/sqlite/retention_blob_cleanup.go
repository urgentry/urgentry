package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/store"
)

func (s *RetentionStore) deleteAttachmentsOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) { //nolint:dupl
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.object_key
		 FROM event_attachments a
		 LEFT JOIN events e
		   ON e.project_id = a.project_id
		  AND e.event_id = a.event_id
		 WHERE a.project_id = ? AND a.created_at < ? AND COALESCE(e.event_type, '') != 'replay'`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list old attachments: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, objectKey string
		if err := rows.Scan(&id, &objectKey); err != nil {
			return 0, fmt.Errorf("scan old attachment: %w", err)
		}
		ids = append(ids, id)
		if s.blobs != nil && objectKey != "" {
			_ = s.blobs.Delete(ctx, objectKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate old attachments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close old attachments: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM event_attachments WHERE id IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old attachments: %w", err)
	}
	return res.RowsAffected()
}

func (s *RetentionStore) archiveOldAttachments(ctx context.Context, projectID string, surface store.TelemetrySurface, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.project_id, a.event_id, a.name, COALESCE(a.content_type, ''), a.size_bytes, a.object_key, a.created_at
		 FROM event_attachments a
		 LEFT JOIN events e
		   ON e.project_id = a.project_id
		  AND e.event_id = a.event_id
		 WHERE a.project_id = ? AND a.created_at < ? AND COALESCE(e.event_type, '') != 'replay'`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list attachments to archive: %w", err)
	}
	defer rows.Close()

	var attachments []archivedAttachment
	for rows.Next() {
		var item archivedAttachment
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.Name, &item.ContentType, &item.SizeBytes, &item.ObjectKey, &item.CreatedAt); err != nil {
			return 0, fmt.Errorf("scan attachment archive candidate: %w", err)
		}
		attachments = append(attachments, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate attachment archive candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close attachment archive candidates: %w", err)
	}

	var archived int64
	for _, item := range attachments {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, surface, "attachment", item.ID, item.ObjectKey, payload); err != nil {
			return 0, err
		}
		archived++
	}
	return archived, nil
}

func (s *RetentionStore) deleteDebugFilesOlderThan(ctx context.Context, projectID string, retentionDays int) (int64, error) { //nolint:dupl
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, object_key FROM debug_files WHERE project_id = ? AND created_at < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list old debug files: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, objectKey string
		if err := rows.Scan(&id, &objectKey); err != nil {
			return 0, fmt.Errorf("scan old debug file: %w", err)
		}
		ids = append(ids, id)
		if s.blobs != nil && objectKey != "" {
			_ = s.blobs.Delete(ctx, objectKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate old debug files: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close old debug files: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM debug_files WHERE id IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old debug files: %w", err)
	}
	return res.RowsAffected()
}

func (s *RetentionStore) archiveOldDebugFiles(ctx context.Context, projectID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, release_version, COALESCE(uuid, ''), COALESCE(code_id, ''), name, object_key, size_bytes, COALESCE(checksum, ''), created_at, kind, COALESCE(content_type, '')
		 FROM debug_files
		 WHERE project_id = ? AND created_at < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list debug files to archive: %w", err)
	}
	defer rows.Close()

	var files []archivedDebugFile
	for rows.Next() {
		var item archivedDebugFile
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ReleaseVersion, &item.UUID, &item.CodeID, &item.Name, &item.ObjectKey, &item.SizeBytes, &item.Checksum, &item.CreatedAt, &item.Kind, &item.ContentType); err != nil {
			return 0, fmt.Errorf("scan debug archive candidate: %w", err)
		}
		files = append(files, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate debug archive candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close debug archive candidates: %w", err)
	}

	var archived int64
	for _, item := range files {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, store.TelemetrySurfaceDebugFiles, "debug_file", item.ID, item.ObjectKey, payload); err != nil {
			return 0, err
		}
		archived++
	}
	return archived, nil
}
