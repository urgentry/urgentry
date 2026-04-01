package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// ListIssueEvents returns events for a group, newest first.
func (s *WebStore) ListIssueEvents(ctx context.Context, groupID string, limit int) ([]store.WebEvent, error) {
	return ListGroupEvents(ctx, s.db, groupID, limit)
}

// ListRecentEvents returns the most recent events across all projects.
func (s *WebStore) ListRecentEvents(ctx context.Context, limit int) ([]store.WebEvent, error) {
	return ListRecentEvents(ctx, s.db, limit)
}

// GetEvent returns a single event by event_id.
func (s *WebStore) GetEvent(ctx context.Context, eventID string) (*store.WebEvent, error) {
	return s.queryWebEvent(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit,
		        occurred_at, tags_json, payload_json, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE event_id = ?`, eventID)
}

// GetEventAtOffset returns the event at the given offset for a group,
// ordered by ingested_at DESC (offset 0 = latest).
func (s *WebStore) GetEventAtOffset(ctx context.Context, groupID string, offset int) (*store.WebEvent, error) {
	return s.queryWebEvent(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit,
		        occurred_at, tags_json, payload_json, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE group_id = ? ORDER BY ingested_at DESC LIMIT 1 OFFSET ?`,
		groupID, offset)
}

// CountEventsForGroup returns the total number of events for a group.
func (s *WebStore) CountEventsForGroup(ctx context.Context, groupID string) (int, error) {
	return s.count(ctx, "SELECT COUNT(*) FROM events WHERE group_id = ?", groupID)
}

// CountDistinctUsersForGroup returns the number of distinct users for a group.
func (s *WebStore) CountDistinctUsersForGroup(ctx context.Context, groupID string) (int, error) {
	return s.count(ctx,
		`SELECT COUNT(DISTINCT user_identifier) FROM events WHERE group_id = ? AND user_identifier != ''`,
		groupID,
	)
}

// ListRecentLogs returns the most recent log events across an organization.
func (s *WebStore) ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error) {
	return ListRecentLogs(ctx, s.db, orgSlug, limit)
}

// SearchLogs returns log events matching the discover query subset.
func (s *WebStore) SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	return SearchLogs(ctx, s.db, orgSlug, rawQuery, limit)
}

// ListRecentTransactions returns the most recent transaction rows across an organization.
func (s *WebStore) ListRecentTransactions(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverTransaction, error) {
	return ListRecentTransactions(ctx, s.db, orgSlug, limit)
}

// SearchTransactions returns transaction rows matching the discover query subset.
func (s *WebStore) SearchTransactions(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error) {
	return SearchTransactions(ctx, s.db, orgSlug, rawQuery, limit)
}

func scanWebEvents(rows *sql.Rows) ([]store.WebEvent, error) {
	var events []store.WebEvent
	for rows.Next() {
		var ev store.WebEvent
		var groupID, title, message, level, platform, culprit, occurredAt, tagsJSON, processingStatus, ingestError sql.NullString
		if err := rows.Scan(&ev.EventID, &groupID, &title, &message, &level, &platform,
			&culprit, &occurredAt, &tagsJSON, &processingStatus, &ingestError); err != nil {
			return nil, err
		}
		ev.GroupID = sqlutil.NullStr(groupID)
		ev.Title = sqlutil.NullStr(title)
		ev.Message = sqlutil.NullStr(message)
		ev.Level = sqlutil.NullStr(level)
		ev.Platform = sqlutil.NullStr(platform)
		ev.Culprit = sqlutil.NullStr(culprit)
		ev.Timestamp = sqlutil.ParseDBTime(sqlutil.NullStr(occurredAt))
		ev.Tags = sqlutil.ParseTags(sqlutil.NullStr(tagsJSON))
		ev.ProcessingStatus = store.EventProcessingStatus(sqlutil.NullStr(processingStatus))
		ev.IngestError = sqlutil.NullStr(ingestError)
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *WebStore) queryWebEvent(ctx context.Context, query string, args ...any) (*store.WebEvent, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	return scanWebEventRow(row)
}

func scanWebEventRow(row *sql.Row) (*store.WebEvent, error) {
	var ev store.WebEvent
	var groupID, title, message, level, platform, culprit, occurredAt, tagsJSON, payloadJSON, processingStatus, ingestError sql.NullString
	err := row.Scan(&ev.EventID, &groupID, &title, &message, &level, &platform,
		&culprit, &occurredAt, &tagsJSON, &payloadJSON, &processingStatus, &ingestError)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ev.GroupID = sqlutil.NullStr(groupID)
	ev.Title = sqlutil.NullStr(title)
	ev.Message = sqlutil.NullStr(message)
	ev.Level = sqlutil.NullStr(level)
	ev.Platform = sqlutil.NullStr(platform)
	ev.Culprit = sqlutil.NullStr(culprit)
	ev.Timestamp = sqlutil.ParseDBTime(sqlutil.NullStr(occurredAt))
	ev.Tags = sqlutil.ParseTags(sqlutil.NullStr(tagsJSON))
	ev.NormalizedJSON = sqlutil.NullStr(payloadJSON)
	ev.ProcessingStatus = store.EventProcessingStatus(sqlutil.NullStr(processingStatus))
	ev.IngestError = sqlutil.NullStr(ingestError)
	return &ev, nil
}

// FirstEventAt returns the timestamp of the first ingested event, if any.
func (s *WebStore) FirstEventAt(ctx context.Context) (*time.Time, error) {
	var firstAt sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(ingested_at) FROM events`).Scan(&firstAt); err != nil {
		return nil, err
	}
	if !firstAt.Valid || firstAt.String == "" {
		return nil, nil
	}
	ts := sqlutil.ParseDBTime(firstAt.String)
	if ts.IsZero() {
		return nil, nil
	}
	return &ts, nil
}

// CountErrorLevelEvents returns error+fatal event count.
func (s *WebStore) CountErrorLevelEvents(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE level IN ('error', 'fatal')`).Scan(&count)
	return count, err
}

// ListEventAttachments returns event attachment metadata, newest first.
func (s *WebStore) ListEventAttachments(ctx context.Context, eventID string) ([]store.EventAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, content_type, size_bytes, created_at
		 FROM event_attachments
		 WHERE event_id = ?
		 ORDER BY created_at DESC, id DESC`,
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []store.EventAttachment
	for rows.Next() {
		var item store.EventAttachment
		var contentType, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &contentType, &item.Size, &createdAt); err != nil {
			return nil, err
		}
		item.ContentType = sqlutil.NullStr(contentType)
		item.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		attachments = append(attachments, item)
	}
	return attachments, rows.Err()
}
