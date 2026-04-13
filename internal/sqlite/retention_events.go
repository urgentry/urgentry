package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/store"
)

func (s *RetentionStore) deleteEventsByTypeOlderThan(ctx context.Context, projectID string, retentionDays int, deleteAttachments bool, eventTypes ...string) (int64, error) {
	if retentionDays <= 0 || len(eventTypes) == 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, COALESCE(payload_key, '')
		 FROM events
		 WHERE project_id = ? AND ingested_at < ? AND COALESCE(event_type, 'error') IN (`+placeholders(len(eventTypes))+`)`,
		append([]any{projectID, cutoff}, stringArgs(eventTypes)...)...,
	)
	if err != nil {
		return 0, fmt.Errorf("list old events: %w", err)
	}
	defer rows.Close()

	var rowIDs []string
	var eventIDs []string
	for rows.Next() {
		var rowID, eventID, payloadKey string
		if err := rows.Scan(&rowID, &eventID, &payloadKey); err != nil {
			return 0, fmt.Errorf("scan old event: %w", err)
		}
		rowIDs = append(rowIDs, rowID)
		eventIDs = append(eventIDs, eventID)
		if s.blobs != nil && payloadKey != "" {
			_ = s.blobs.Delete(ctx, payloadKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate old events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close old events: %w", err)
	}
	if len(rowIDs) == 0 {
		return 0, nil
	}
	if deleteAttachments {
		if _, err := s.deleteAttachmentsForEventIDs(ctx, projectID, eventIDs); err != nil {
			return 0, err
		}
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM events WHERE id IN (`+placeholders(len(rowIDs))+`)`,
		stringArgs(rowIDs)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete old events: %w", err)
	}
	return res.RowsAffected()
}

func (s *RetentionStore) archiveEventsByTypeOlderThan(ctx context.Context, projectID string, surface store.TelemetrySurface, includeAttachments bool, retentionDays int, eventTypes ...string) (int64, error) {
	if retentionDays <= 0 || len(eventTypes) == 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, COALESCE(group_id, ''), COALESCE(release, ''), COALESCE(environment, ''), COALESCE(platform, ''), COALESCE(level, ''), COALESCE(event_type, 'error'),
		        COALESCE(title, ''), COALESCE(culprit, ''), COALESCE(message, ''), COALESCE(tags_json, '{}'), COALESCE(payload_json, '{}'), COALESCE(occurred_at, ''), COALESCE(ingested_at, ''), COALESCE(user_identifier, ''), COALESCE(payload_key, '')
		 FROM events
		 WHERE project_id = ? AND ingested_at < ? AND COALESCE(event_type, 'error') IN (`+placeholders(len(eventTypes))+`)`,
		append([]any{projectID, cutoff}, stringArgs(eventTypes)...)...,
	)
	if err != nil {
		return 0, fmt.Errorf("list %s events to archive: %w", surface, err)
	}
	defer rows.Close()

	var candidates []archivedEvent
	for rows.Next() {
		var item archivedEvent
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.GroupID, &item.Release, &item.Environment, &item.Platform, &item.Level, &item.EventType, &item.Title, &item.Culprit, &item.Message, &item.TagsJSON, &item.PayloadJSON, &item.OccurredAt, &item.IngestedAt, &item.UserIdentifier, &item.PayloadKey); err != nil {
			return 0, fmt.Errorf("scan %s event archive candidate: %w", surface, err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate %s event archive candidates: %w", surface, err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close %s event archive candidates: %w", surface, err)
	}

	var archived int64
	for _, item := range candidates {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, surface, "event", item.ID, item.PayloadKey, payload); err != nil {
			return 0, err
		}
		if includeAttachments {
			if _, err := s.archiveAttachmentsForEvent(ctx, projectID, surface, item.EventID); err != nil {
				return 0, err
			}
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, item.ID); err != nil {
			return 0, fmt.Errorf("delete archived event: %w", err)
		}
		archived++
	}
	return archived, nil
}

func (s *RetentionStore) archiveAttachmentsForEvent(ctx context.Context, projectID string, surface store.TelemetrySurface, eventID string) (int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, name, COALESCE(content_type, ''), size_bytes, object_key, created_at
		 FROM event_attachments
		 WHERE project_id = ? AND event_id = ?`,
		projectID, eventID,
	)
	if err != nil {
		return 0, fmt.Errorf("list attachments for event archive: %w", err)
	}
	defer rows.Close()

	var candidates []archivedAttachment
	for rows.Next() {
		var item archivedAttachment
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.Name, &item.ContentType, &item.SizeBytes, &item.ObjectKey, &item.CreatedAt); err != nil {
			return 0, fmt.Errorf("scan attachment archive candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate attachment archive candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close attachment archive candidates: %w", err)
	}

	var archived int64
	for _, item := range candidates {
		payload, _ := json.Marshal(item)
		if err := s.archiveRecord(ctx, projectID, surface, "attachment", item.ID, item.ObjectKey, payload); err != nil {
			return 0, err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM event_attachments WHERE id = ?`, item.ID); err != nil {
			return 0, fmt.Errorf("delete archived attachment: %w", err)
		}
		archived++
	}
	return archived, nil
}

func (s *RetentionStore) deleteAttachmentsForEventIDs(ctx context.Context, projectID string, eventIDs []string) (int64, error) {
	if len(eventIDs) == 0 {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, object_key FROM event_attachments
		 WHERE project_id = ? AND event_id IN (`+placeholders(len(eventIDs))+`)`,
		append([]any{projectID}, stringArgs(eventIDs)...)...,
	)
	if err != nil {
		return 0, fmt.Errorf("list attachments for events: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, objectKey string
		if err := rows.Scan(&id, &objectKey); err != nil {
			return 0, fmt.Errorf("scan attachment for event delete: %w", err)
		}
		ids = append(ids, id)
		if s.blobs != nil && objectKey != "" {
			_ = s.blobs.Delete(ctx, objectKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate attachments for events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close attachments for events: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM event_attachments WHERE id IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete attachments for events: %w", err)
	}
	return res.RowsAffected()
}
