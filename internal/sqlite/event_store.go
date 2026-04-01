package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// EventStore is a SQLite-backed implementation of store.EventStore.
type EventStore struct {
	db *sql.DB
}

// NewEventStore creates an EventStore backed by the given database.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db}
}

// SaveEvent persists a normalized event. Duplicate (project_id, event_id)
// pairs are silently ignored via INSERT OR IGNORE.
func (s *EventStore) SaveEvent(ctx context.Context, evt *store.StoredEvent) error {
	if evt.EventType == "" {
		evt.EventType = "error"
	}
	if evt.ProcessingStatus == "" {
		evt.ProcessingStatus = store.EventProcessingStatusCompleted
	}
	tagsJSON, _ := json.Marshal(evt.Tags)
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type,
			 title, culprit, message, tags_json, payload_json, occurred_at, user_identifier, payload_key, processing_status, ingest_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.ID, evt.ProjectID, evt.EventID, evt.GroupID, evt.ReleaseID, evt.Environment,
		evt.Platform, evt.Level, evt.EventType, evt.Title, evt.Culprit, evt.Message,
		string(tagsJSON), string(evt.NormalizedJSON),
		evt.OccurredAt.UTC().Format(time.RFC3339),
		evt.UserIdentifier,
		evt.PayloadKey,
		string(evt.ProcessingStatus),
		evt.IngestError,
	)
	return err
}

// UpsertEvent persists a normalized event into a known row ID.
func (s *EventStore) UpsertEvent(ctx context.Context, evt *store.StoredEvent) error {
	if evt.EventType == "" {
		evt.EventType = "error"
	}
	if evt.ProcessingStatus == "" {
		evt.ProcessingStatus = store.EventProcessingStatusCompleted
	}
	tagsJSON, _ := json.Marshal(evt.Tags)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type,
			 title, culprit, message, tags_json, payload_json, occurred_at, user_identifier, payload_key, processing_status, ingest_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			event_id = excluded.event_id,
			group_id = excluded.group_id,
			release = excluded.release,
			environment = excluded.environment,
			platform = excluded.platform,
			level = excluded.level,
			event_type = excluded.event_type,
			title = excluded.title,
			culprit = excluded.culprit,
			message = excluded.message,
			tags_json = excluded.tags_json,
			payload_json = excluded.payload_json,
			occurred_at = excluded.occurred_at,
			user_identifier = excluded.user_identifier,
			payload_key = excluded.payload_key,
			processing_status = excluded.processing_status,
			ingest_error = excluded.ingest_error`,
		evt.ID, evt.ProjectID, evt.EventID, evt.GroupID, evt.ReleaseID, evt.Environment,
		evt.Platform, evt.Level, evt.EventType, evt.Title, evt.Culprit, evt.Message,
		string(tagsJSON), string(evt.NormalizedJSON),
		evt.OccurredAt.UTC().Format(time.RFC3339),
		evt.UserIdentifier,
		evt.PayloadKey,
		string(evt.ProcessingStatus),
		evt.IngestError,
	)
	return err
}

func (s *EventStore) UpdateProcessingStatus(ctx context.Context, eventRowID string, status store.EventProcessingStatus, ingestError string) error {
	if strings.TrimSpace(eventRowID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE events
		    SET processing_status = ?, ingest_error = ?
		  WHERE id = ?`,
		string(status), strings.TrimSpace(ingestError), eventRowID,
	)
	return err
}

func (s *EventStore) DeleteEvent(ctx context.Context, eventRowID string) error {
	if strings.TrimSpace(eventRowID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, eventRowID)
	return err
}

// CountDistinctUsers returns the number of distinct user identifiers for a project since the given time.
func (s *EventStore) CountDistinctUsers(ctx context.Context, projectID string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT user_identifier) FROM events
		 WHERE project_id = ? AND user_identifier != '' AND occurred_at >= ?`,
		projectID, since.Format(time.RFC3339),
	).Scan(&count)
	return count, err
}

// CountDistinctUsersAll returns the total number of distinct user identifiers for a project.
func (s *EventStore) CountDistinctUsersAll(ctx context.Context, projectID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT user_identifier) FROM events
		 WHERE project_id = ? AND user_identifier != ''`,
		projectID,
	).Scan(&count)
	return count, err
}

// GetEvent retrieves a single event by project and event ID.
func (s *EventStore) GetEvent(ctx context.Context, projectID, eventID string) (*store.StoredEvent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE project_id = ? AND event_id = ?`,
		projectID, eventID,
	)
	return scanEvent(row)
}

// GetEventByRowID retrieves a single event by its internal row ID.
func (s *EventStore) GetEventByRowID(ctx context.Context, eventRowID string) (*store.StoredEvent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE id = ?`,
		eventRowID,
	)
	return scanEvent(row)
}

// GetEventByType retrieves a single event by project, event ID, and event type.
func (s *EventStore) GetEventByType(ctx context.Context, projectID, eventID, eventType string) (*store.StoredEvent, error) {
	query := `SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events
		 WHERE project_id = ? AND COALESCE(event_type, 'error') = ? AND event_id = ?`
	args := []any{projectID, eventType, eventID}
	switch eventType {
	case "replay":
		query = `SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events
		 WHERE project_id = ? AND COALESCE(event_type, 'error') = ?
		   AND (event_id = ? OR COALESCE(json_extract(payload_json, '$.replay_id'), '') = ?)`
		args = []any{projectID, eventType, eventID, eventID}
	case "profile":
		query = `SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events
		 WHERE project_id = ? AND COALESCE(event_type, 'error') = ?
		   AND (event_id = ? OR COALESCE(json_extract(payload_json, '$.profile_id'), '') = ?)`
		args = []any{projectID, eventType, eventID, eventID}
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	return scanEvent(row)
}

// ListEvents returns events for a project, ordered by ingested_at descending.
func (s *EventStore) ListEvents(ctx context.Context, projectID string, opts store.ListOpts) ([]*store.StoredEvent, error) {
	return s.listEvents(ctx, projectID, "", opts)
}

// ListEventsByType returns events for a project filtered by the given event type.
func (s *EventStore) ListEventsByType(ctx context.Context, projectID, eventType string, opts store.ListOpts) ([]*store.StoredEvent, error) {
	return s.listEvents(ctx, projectID, eventType, opts)
}

func (s *EventStore) listEvents(ctx context.Context, projectID, eventType string, opts store.ListOpts) ([]*store.StoredEvent, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query := `SELECT id, project_id, event_id, group_id, release, environment, platform,
				level, COALESCE(event_type, 'error'), title, culprit, message, tags_json, payload_json, occurred_at, ingested_at, COALESCE(user_identifier, ''), payload_key, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
			  FROM events WHERE project_id = ?`
	args := []any{projectID}

	if eventType != "" {
		query += ` AND COALESCE(event_type, 'error') = ?`
		args = append(args, eventType)
	}

	if opts.Cursor != "" {
		query += ` AND ingested_at < (SELECT ingested_at FROM events WHERE event_id = ? AND project_id = ?)`
		args = append(args, opts.Cursor, projectID)
	}

	switch opts.Sort {
	case "occurred_at_asc":
		query += ` ORDER BY occurred_at ASC`
	default:
		query += ` ORDER BY ingested_at DESC`
	}

	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*store.StoredEvent
	for rows.Next() {
		evt, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, evt)
	}
	return events, rows.Err()
}

func scanEvent(row *sql.Row) (*store.StoredEvent, error) {
	var evt store.StoredEvent
	var groupID, release, environment, platform, level, eventType, title, culprit, message sql.NullString
	var tagsJSON, payloadJSON, occurredAt, ingestedAt, userIdentifier, payloadKey, processingStatus, ingestError sql.NullString
	err := row.Scan(
		&evt.ID, &evt.ProjectID, &evt.EventID, &groupID,
		&release, &environment, &platform,
		&level, &eventType, &title, &culprit, &message,
		&tagsJSON, &payloadJSON, &occurredAt, &ingestedAt, &userIdentifier, &payloadKey, &processingStatus, &ingestError,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	evt.GroupID = nullStr(groupID)
	evt.ReleaseID = nullStr(release)
	evt.Environment = nullStr(environment)
	evt.Platform = nullStr(platform)
	evt.Level = nullStr(level)
	evt.EventType = nullStr(eventType)
	evt.Title = nullStr(title)
	evt.Culprit = nullStr(culprit)
	evt.Message = nullStr(message)
	evt.NormalizedJSON = json.RawMessage(nullStr(payloadJSON))
	evt.UserIdentifier = nullStr(userIdentifier)
	evt.PayloadKey = nullStr(payloadKey)
	evt.ProcessingStatus = store.EventProcessingStatus(nullStr(processingStatus))
	evt.IngestError = nullStr(ingestError)
	if t, e := time.Parse(time.RFC3339, nullStr(occurredAt)); e == nil {
		evt.OccurredAt = t
	}
	if t, e := time.Parse(time.RFC3339, nullStr(ingestedAt)); e == nil {
		evt.IngestedAt = t
	} else if t, e := time.Parse("2006-01-02 15:04:05", nullStr(ingestedAt)); e == nil {
		evt.IngestedAt = t
	}
	evt.Tags = sqlutil.ParseTags(nullStr(tagsJSON))
	return &evt, nil
}

func scanEventRows(rows *sql.Rows) (*store.StoredEvent, error) {
	var evt store.StoredEvent
	var groupID, release, environment, platform, level, eventType, title, culprit, message sql.NullString
	var tagsJSON, payloadJSON, occurredAt, ingestedAt, userIdentifier, payloadKey, processingStatus, ingestError sql.NullString
	err := rows.Scan(
		&evt.ID, &evt.ProjectID, &evt.EventID, &groupID,
		&release, &environment, &platform,
		&level, &eventType, &title, &culprit, &message,
		&tagsJSON, &payloadJSON, &occurredAt, &ingestedAt, &userIdentifier, &payloadKey, &processingStatus, &ingestError,
	)
	if err != nil {
		return nil, err
	}
	evt.GroupID = nullStr(groupID)
	evt.ReleaseID = nullStr(release)
	evt.Environment = nullStr(environment)
	evt.Platform = nullStr(platform)
	evt.Level = nullStr(level)
	evt.EventType = nullStr(eventType)
	evt.Title = nullStr(title)
	evt.Culprit = nullStr(culprit)
	evt.Message = nullStr(message)
	evt.NormalizedJSON = json.RawMessage(nullStr(payloadJSON))
	evt.UserIdentifier = nullStr(userIdentifier)
	evt.PayloadKey = nullStr(payloadKey)
	evt.ProcessingStatus = store.EventProcessingStatus(nullStr(processingStatus))
	evt.IngestError = nullStr(ingestError)
	if t, e := time.Parse(time.RFC3339, nullStr(occurredAt)); e == nil {
		evt.OccurredAt = t
	}
	if t, e := time.Parse(time.RFC3339, nullStr(ingestedAt)); e == nil {
		evt.IngestedAt = t
	} else if t, e := time.Parse("2006-01-02 15:04:05", nullStr(ingestedAt)); e == nil {
		evt.IngestedAt = t
	}
	evt.Tags = sqlutil.ParseTags(nullStr(tagsJSON))
	return &evt, nil
}
