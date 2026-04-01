package sqlite

import (
	"context"
	"database/sql"

	"urgentry/internal/store"
)

func ListProjectIssues(ctx context.Context, db *sql.DB, projectID string, limit int) ([]store.WebIssue, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, title, culprit, level, status, first_seen, last_seen, times_seen, assignee, priority, short_id, resolution_substatus, resolved_in_release, merged_into_group_id
		 FROM groups WHERE project_id = ? ORDER BY last_seen DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

func GetIssue(ctx context.Context, db *sql.DB, id string) (*store.WebIssue, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, title, culprit, level, status, first_seen, last_seen, times_seen, assignee, priority, short_id, resolution_substatus, resolved_in_release, merged_into_group_id
		 FROM groups WHERE id = ?`, id)
	return scanWebIssueRow(row)
}

func ListGroupEvents(ctx context.Context, db *sql.DB, groupID string, limit int) ([]store.WebEvent, error) {
	q := `SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json,
	             COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
	      FROM events WHERE group_id = ? ORDER BY ingested_at DESC`
	args := []any{groupID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebEvents(rows)
}

func GetLatestGroupEvent(ctx context.Context, db *sql.DB, groupID string) (*store.WebEvent, error) {
	row := db.QueryRowContext(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json, payload_json,
		        COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE group_id = ? ORDER BY ingested_at DESC LIMIT 1`,
		groupID,
	)
	return scanWebEventRow(row)
}

func ListProjectEvents(ctx context.Context, db *sql.DB, projectID string, limit int) ([]store.WebEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json,
		        COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE project_id = ? ORDER BY ingested_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebEvents(rows)
}

func ListRecentEvents(ctx context.Context, db *sql.DB, limit int) ([]store.WebEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json,
		        COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events ORDER BY ingested_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebEvents(rows)
}

func GetProjectEvent(ctx context.Context, db *sql.DB, projectID, eventID string) (*store.WebEvent, error) {
	row := db.QueryRowContext(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json, payload_json,
		        COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE project_id = ? AND event_id = ?`, projectID, eventID)
	return scanWebEventRow(row)
}
