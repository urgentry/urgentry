package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/store"
)

func (s *RetentionStore) restoreArchiveRow(ctx context.Context, archive telemetryArchiveRow) error {
	switch archive.RecordType {
	case "event":
		var item archivedEvent
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived event: %w", err)
		}
		if err := s.restoreObject(ctx, archive.ArchiveKey, item.PayloadKey); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO events
				(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, user_identifier, payload_key)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, item.EventID, nullIfEmpty(item.GroupID), nullIfEmpty(item.Release), nullIfEmpty(item.Environment),
			nullIfEmpty(item.Platform), nullIfEmpty(item.Level), nullIfEmpty(item.EventType), nullIfEmpty(item.Title), nullIfEmpty(item.Culprit),
			nullIfEmpty(item.Message), item.TagsJSON, item.PayloadJSON, nullIfEmpty(item.OccurredAt), nullIfEmpty(item.IngestedAt),
			nullIfEmpty(item.UserIdentifier), nullIfEmpty(item.PayloadKey),
		); err != nil {
			return fmt.Errorf("restore event: %w", err)
		}
		if item.EventType == "profile" {
			var tags map[string]string
			if rawTags := strings.TrimSpace(item.TagsJSON); rawTags != "" && rawTags != "{}" {
				_ = json.Unmarshal([]byte(rawTags), &tags)
			}
			evt := &store.StoredEvent{
				ID:             item.ID,
				ProjectID:      item.ProjectID,
				EventID:        item.EventID,
				GroupID:        item.GroupID,
				ReleaseID:      item.Release,
				Environment:    item.Environment,
				Platform:       item.Platform,
				Level:          item.Level,
				EventType:      item.EventType,
				OccurredAt:     parseOptionalTimeString(item.OccurredAt),
				IngestedAt:     parseOptionalTimeString(item.IngestedAt),
				Message:        item.Message,
				Title:          item.Title,
				Culprit:        item.Culprit,
				Tags:           tags,
				NormalizedJSON: json.RawMessage(item.PayloadJSON),
				PayloadKey:     item.PayloadKey,
				UserIdentifier: item.UserIdentifier,
			}
			if err := materializeProfileEventWithQuerier(ctx, s.db, s.blobs, evt, evt.NormalizedJSON); err != nil {
				return fmt.Errorf("restore profile manifest: %w", err)
			}
		}
		if item.EventType == "replay" {
			if err := s.reindexReplayEvent(ctx, item.ProjectID, item.EventID); err != nil {
				return err
			}
		}
	case "attachment":
		var item archivedAttachment
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived attachment: %w", err)
		}
		if err := s.restoreObject(ctx, archive.ArchiveKey, item.ObjectKey); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO event_attachments
				(id, project_id, event_id, name, content_type, size_bytes, object_key, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, item.EventID, item.Name, nullIfEmpty(item.ContentType), item.SizeBytes, item.ObjectKey, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("restore attachment: %w", err)
		}
		if err := s.reindexReplayEvent(ctx, item.ProjectID, item.EventID); err != nil {
			return err
		}
	case "transaction":
		var item archivedTransaction
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived transaction: %w", err)
		}
		if err := s.restoreObject(ctx, archive.ArchiveKey, item.PayloadKey); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO transactions
				(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, payload_key, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, item.EventID, item.TraceID, item.SpanID, nullIfEmpty(item.ParentSpanID), item.TransactionName,
			nullIfEmpty(item.Op), nullIfEmpty(item.Status), nullIfEmpty(item.Platform), nullIfEmpty(item.Environment), nullIfEmpty(item.Release),
			item.StartTimestamp, item.EndTimestamp, item.DurationMS, item.TagsJSON, item.MeasurementsJSON, item.PayloadJSON, nullIfEmpty(item.PayloadKey), item.CreatedAt,
		); err != nil {
			return fmt.Errorf("restore transaction: %w", err)
		}
	case "span":
		var item archivedSpan
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived span: %w", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO spans
				(id, project_id, transaction_event_id, trace_id, span_id, parent_span_id, op, description, status, start_timestamp, end_timestamp, duration_ms, tags_json, data_json, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, item.TransactionEventID, item.TraceID, item.SpanID, nullIfEmpty(item.ParentSpanID), nullIfEmpty(item.Op),
			nullIfEmpty(item.Description), nullIfEmpty(item.Status), item.StartTimestamp, item.EndTimestamp, item.DurationMS, item.TagsJSON, item.DataJSON, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("restore span: %w", err)
		}
	case "outcome":
		var item archivedOutcome
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived outcome: %w", err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO outcomes
				(id, project_id, event_id, category, reason, quantity, source, release, environment, payload_json, recorded_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, nullIfEmpty(item.EventID), item.Category, item.Reason, item.Quantity, item.Source,
			nullIfEmpty(item.Release), nullIfEmpty(item.Environment), item.PayloadJSON, item.RecordedAt, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("restore outcome: %w", err)
		}
	case "debug_file":
		var item archivedDebugFile
		if err := json.Unmarshal([]byte(archive.Metadata), &item); err != nil {
			return fmt.Errorf("decode archived debug file: %w", err)
		}
		if err := s.restoreObject(ctx, archive.ArchiveKey, item.ObjectKey); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO debug_files
				(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at, kind, content_type)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO NOTHING`,
			item.ID, item.ProjectID, item.ReleaseVersion, item.UUID, nullIfEmpty(item.CodeID), item.Name, item.ObjectKey, item.SizeBytes,
			nullIfEmpty(item.Checksum), item.CreatedAt, item.Kind, nullIfEmpty(item.ContentType),
		); err != nil {
			return fmt.Errorf("restore debug file: %w", err)
		}
	default:
		return fmt.Errorf("unsupported archive record type %q", archive.RecordType)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_archives WHERE id = ?`, archive.ID); err != nil {
		return err
	}
	if s.blobs != nil && archive.ArchiveKey != "" {
		_ = s.blobs.Delete(ctx, archive.ArchiveKey)
	}
	return nil
}

func getProjectByID(ctx context.Context, db *sql.DB, projectID string) (*store.Project, error) {
	row := db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, p.name, p.platform, p.status, p.event_retention_days, p.attachment_retention_days, p.debug_file_retention_days, p.created_at, o.slug, COALESCE(t.slug, '')
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN teams t ON t.id = p.team_id
		 WHERE p.id = ?`,
		projectID,
	)
	var rec store.Project
	var platform, status, createdAt, orgValue, teamSlug sql.NullString
	if err := row.Scan(&rec.ID, &rec.Slug, &rec.Name, &platform, &status, &rec.EventRetentionDays, &rec.AttachRetentionDays, &rec.DebugRetentionDays, &createdAt, &orgValue, &teamSlug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load project by id: %w", err)
	}
	rec.Platform = nullStr(platform)
	rec.Status = nullStr(status)
	rec.DateCreated = parseTime(nullStr(createdAt))
	rec.OrgSlug = nullStr(orgValue)
	rec.TeamSlug = nullStr(teamSlug)
	return &rec, nil
}

func (s *RetentionStore) reindexReplayEvent(ctx context.Context, projectID, eventID string) error {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(eventID) == "" {
		return nil
	}
	evt, err := NewEventStore(s.db).GetEventByType(ctx, projectID, eventID, "replay")
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load replay restore target: %w", err)
	}
	if evt == nil {
		return nil
	}
	if err := NewReplayStore(s.db, s.blobs).IndexReplay(ctx, projectID, evt.EventID); err != nil {
		return fmt.Errorf("restore replay manifest: %w", err)
	}
	return nil
}

func (s *RetentionStore) restoreObject(ctx context.Context, archiveKey, objectKey string) error {
	if s.blobs == nil || archiveKey == "" || objectKey == "" {
		return nil
	}
	body, err := s.blobs.Get(ctx, archiveKey)
	if err != nil {
		return fmt.Errorf("load archived blob: %w", err)
	}
	if err := s.blobs.Put(ctx, objectKey, body); err != nil {
		return fmt.Errorf("restore archived blob: %w", err)
	}
	return nil
}

func (s *RetentionStore) archiveRecord(ctx context.Context, projectID string, surface store.TelemetrySurface, recordType, recordID, objectKey string, metadata []byte) error {
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	archiveKey := ""
	if s.blobs != nil && objectKey != "" {
		body, err := s.blobs.Get(ctx, objectKey)
		if err != nil && err != store.ErrNotFound {
			return fmt.Errorf("load blob for archive %s: %w", recordID, err)
		}
		if err == nil {
			archiveKey = telemetryArchiveBlobKey(projectID, surface, recordType, recordID)
			if err := s.blobs.Put(ctx, archiveKey, body); err != nil {
				return fmt.Errorf("write archive blob %s: %w", recordID, err)
			}
			_ = s.blobs.Delete(ctx, objectKey)
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO telemetry_archives
			(id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(), projectID, string(surface), recordType, recordID, nullIfEmpty(archiveKey), string(metadata), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert archive record %s: %w", recordID, err)
	}
	return nil
}

func (s *RetentionStore) deleteArchivedSurfacePastBoundary(ctx context.Context, projectID string, surface store.TelemetrySurface, archiveRetentionDays int) (int64, error) {
	if archiveRetentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(archiveRetentionDays) * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, record_type, record_id, COALESCE(archive_key, '')
		 FROM telemetry_archives
		 WHERE project_id = ? AND surface = ? AND restored_at IS NULL AND archived_at < ?`,
		projectID, string(surface), cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("list archived %s rows: %w", surface, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, recordType, recordID, archiveKey string
		if err := rows.Scan(&id, &recordType, &recordID, &archiveKey); err != nil {
			return 0, fmt.Errorf("scan archived %s row: %w", surface, err)
		}
		ids = append(ids, id)
		switch {
		case surface == store.TelemetrySurfaceAttachments && recordType == "attachment":
			if _, err := s.db.ExecContext(ctx, `DELETE FROM event_attachments WHERE id = ?`, recordID); err != nil {
				return 0, fmt.Errorf("delete archived attachment metadata: %w", err)
			}
		case surface == store.TelemetrySurfaceDebugFiles && recordType == "debug_file":
			if _, err := s.db.ExecContext(ctx, `DELETE FROM debug_files WHERE id = ?`, recordID); err != nil {
				return 0, fmt.Errorf("delete archived debug file metadata: %w", err)
			}
		}
		if s.blobs != nil && archiveKey != "" {
			_ = s.blobs.Delete(ctx, archiveKey)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate archived %s rows: %w", surface, err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close archived %s rows: %w", surface, err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM telemetry_archives WHERE id IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete archived %s rows: %w", surface, err)
	}
	return res.RowsAffected()
}
