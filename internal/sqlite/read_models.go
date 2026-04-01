package sqlite

import (
	"context"
	"database/sql"

	"urgentry/internal/sqlutil"
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

// ResolvedEvent holds the result of resolving an event ID across all projects.
type ResolvedEvent struct {
	EventID     string
	GroupID     string
	ProjectSlug string
	OrgSlug     string
	Event       store.WebEvent
}

// ResolveEventID looks up an event by event_id across all projects within an org,
// returning the project slug, group ID, and event details.
func ResolveEventID(ctx context.Context, db *sql.DB, orgSlug, eventID string) (*ResolvedEvent, error) {
	var resolved ResolvedEvent
	var groupID, title, message, level, platform, culprit, occurredAt, tagsJSON, payloadJSON, processingStatus, ingestError sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT e.event_id, e.group_id, p.slug, o.slug,
		        e.title, e.message, e.level, e.platform, e.culprit, e.occurred_at,
		        e.tags_json, e.payload_json,
		        COALESCE(e.processing_status, 'completed'), COALESCE(e.ingest_error, '')
		 FROM events e
		 JOIN projects p ON p.id = e.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND e.event_id = ?
		 LIMIT 1`,
		orgSlug, eventID,
	).Scan(&resolved.EventID, &groupID, &resolved.ProjectSlug, &resolved.OrgSlug,
		&title, &message, &level, &platform, &culprit, &occurredAt,
		&tagsJSON, &payloadJSON, &processingStatus, &ingestError)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	resolved.GroupID = sqlutil.NullStr(groupID)
	resolved.Event = store.WebEvent{
		EventID:          resolved.EventID,
		GroupID:           resolved.GroupID,
		Title:            sqlutil.NullStr(title),
		Message:          sqlutil.NullStr(message),
		Level:            sqlutil.NullStr(level),
		Platform:         sqlutil.NullStr(platform),
		Culprit:          sqlutil.NullStr(culprit),
		NormalizedJSON:   sqlutil.NullStr(payloadJSON),
		ProcessingStatus: store.EventProcessingStatus(sqlutil.NullStr(processingStatus)),
		IngestError:      sqlutil.NullStr(ingestError),
	}
	if t := sqlutil.NullStr(occurredAt); t != "" {
		resolved.Event.Timestamp = sqlutil.ParseDBTime(t)
	}
	resolved.Event.Tags = sqlutil.ParseTags(sqlutil.NullStr(tagsJSON))
	return &resolved, nil
}

// ResolveShortID looks up an issue by its numeric short_id within an org.
func ResolveShortID(ctx context.Context, db *sql.DB, orgSlug string, shortID int) (*store.WebIssue, string, error) {
	row := db.QueryRowContext(ctx,
		`SELECT g.id, g.title, g.culprit, g.level, g.status, g.first_seen, g.last_seen, g.times_seen,
		        g.assignee, g.priority, g.short_id, g.resolution_substatus, g.resolved_in_release, g.merged_into_group_id,
		        p.slug
		 FROM groups g
		 JOIN projects p ON p.id = g.project_id
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND g.short_id = ?`,
		orgSlug, shortID,
	)
	issue, projectSlug, err := scanWebIssueWithProjectSlug(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}
	return issue, projectSlug, nil
}

func scanWebIssueWithProjectSlug(row *sql.Row) (*store.WebIssue, string, error) {
	var iss store.WebIssue
	var title, culprit, level, status, assignee sql.NullString
	var resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
	var firstSeen, lastSeen sql.NullString
	var count sql.NullInt64
	var priority sql.NullInt64
	var shortID sql.NullInt64
	var projectSlug string
	err := row.Scan(&iss.ID, &title, &culprit, &level, &status,
		&firstSeen, &lastSeen, &count, &assignee, &priority, &shortID,
		&resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID,
		&projectSlug)
	if err != nil {
		return nil, "", err
	}
	iss.Title = sqlutil.NullStr(title)
	iss.Culprit = sqlutil.NullStr(culprit)
	iss.Level = sqlutil.NullStr(level)
	iss.Status = sqlutil.NullStr(status)
	if iss.Status == "" {
		iss.Status = "unresolved"
	}
	iss.FirstSeen = sqlutil.ParseDBTime(sqlutil.NullStr(firstSeen))
	iss.LastSeen = sqlutil.ParseDBTime(sqlutil.NullStr(lastSeen))
	if count.Valid {
		iss.Count = count.Int64
	}
	iss.Assignee = sqlutil.NullStr(assignee)
	if priority.Valid {
		iss.Priority = int(priority.Int64)
	} else {
		iss.Priority = 2
	}
	if shortID.Valid {
		iss.ShortID = int(shortID.Int64)
	}
	iss.ResolutionSubstatus = sqlutil.NullStr(resolutionSubstatus)
	iss.ResolvedInRelease = sqlutil.NullStr(resolvedInRelease)
	iss.MergedIntoGroupID = sqlutil.NullStr(mergedIntoGroupID)
	return &iss, projectSlug, nil
}
