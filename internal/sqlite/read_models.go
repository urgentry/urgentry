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
	             payload_json, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
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
		        payload_json, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
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
		        payload_json, COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events ORDER BY ingested_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebEvents(rows)
}

// OrgEvent extends WebEvent with project metadata for org-level event listing.
type OrgEvent struct {
	store.WebEvent
	ProjectID   string
	ProjectName string
}

// ListOrgEvents returns events across all projects in an organization, newest first.
func ListOrgEvents(ctx context.Context, db *sql.DB, orgSlug string, query string, sortField string, limit int) ([]OrgEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	orderCol := "e.ingested_at"
	if sortField == "timestamp" || sortField == "-timestamp" {
		orderCol = "e.occurred_at"
	}
	orderDir := "DESC"
	if len(sortField) > 0 && sortField[0] != '-' {
		orderDir = "ASC"
	}

	baseQuery := `SELECT e.event_id, e.group_id, e.title, e.message, e.level, e.platform,
	        e.culprit, e.occurred_at, e.tags_json,
	        COALESCE(e.processing_status, 'completed'), COALESCE(e.ingest_error, ''),
	        e.project_id, COALESCE(p.name, p.slug)
	 FROM events e
	 JOIN projects p ON p.id = e.project_id
	 JOIN organizations o ON o.id = p.organization_id
	 WHERE o.slug = ?`
	args := []any{orgSlug}
	if query != "" {
		baseQuery += ` AND (e.title LIKE ? OR e.message LIKE ? OR e.culprit LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	baseQuery += ` ORDER BY ` + orderCol + ` ` + orderDir + ` LIMIT ?`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OrgEvent
	for rows.Next() {
		var ev OrgEvent
		var groupID, title, message, level, platform, culprit, occurredAt, tagsJSON, processingStatus, ingestError sql.NullString
		if err := rows.Scan(&ev.EventID, &groupID, &title, &message, &level, &platform,
			&culprit, &occurredAt, &tagsJSON, &processingStatus, &ingestError,
			&ev.ProjectID, &ev.ProjectName); err != nil {
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
		GroupID:          resolved.GroupID,
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

// GetGroupEvent returns a single event within a group, verifying the event belongs to the group.
func GetGroupEvent(ctx context.Context, db *sql.DB, groupID, eventID string) (*store.WebEvent, error) {
	row := db.QueryRowContext(ctx,
		`SELECT event_id, group_id, title, message, level, platform, culprit, occurred_at, tags_json, payload_json,
		        COALESCE(processing_status, 'completed'), COALESCE(ingest_error, '')
		 FROM events WHERE group_id = ? AND event_id = ?`,
		groupID, eventID,
	)
	return scanWebEventRow(row)
}

// GroupHashRow represents a fingerprint hash for a group.
type GroupHashRow struct {
	ID          string            `json:"id"`
	LatestEvent GroupHashEventRef `json:"latestEvent"`
}

// GroupHashEventRef is a minimal event reference within a hash entry.
type GroupHashEventRef struct {
	EventID string `json:"eventID"`
}

// ListGroupHashes returns all distinct grouping keys (hashes) for a group.
// Each hash includes a reference to the latest event with that grouping key.
func ListGroupHashes(ctx context.Context, db *sql.DB, groupID string) ([]GroupHashRow, error) {
	// A group's hash is its grouping_key. Return it along with the last_event_id.
	row := db.QueryRowContext(ctx,
		`SELECT grouping_key, COALESCE(last_event_id, '') FROM groups WHERE id = ?`, groupID)
	var groupingKey, lastEventID string
	if err := row.Scan(&groupingKey, &lastEventID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return []GroupHashRow{
		{
			ID:          groupingKey,
			LatestEvent: GroupHashEventRef{EventID: lastEventID},
		},
	}, nil
}

// IssueTagDetail holds aggregated tag key information for an issue.
type IssueTagDetail struct {
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	UniqueValues int           `json:"uniqueValues"`
	TopValues    []TagValueRow `json:"topValues"`
}

// GetIssueTagDetail returns tag key info with top values for an issue.
func GetIssueTagDetail(ctx context.Context, db *sql.DB, groupID, tagKey string) (*IssueTagDetail, error) {
	path := "$." + tagKey
	query := `SELECT
		json_extract(tags_json, ?) AS tag_val,
		COUNT(*) AS cnt,
		MAX(occurred_at) AS last_seen,
		MIN(occurred_at) AS first_seen
	FROM events
	WHERE group_id = ?
	  AND json_extract(tags_json, ?) IS NOT NULL
	  AND json_extract(tags_json, ?) != ''
	GROUP BY tag_val
	ORDER BY cnt DESC`

	rows, err := db.QueryContext(ctx, query, path, groupID, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []TagValueRow
	for rows.Next() {
		var r TagValueRow
		if err := rows.Scan(&r.Value, &r.Count, &r.LastSeen, &r.FirstSeen); err != nil {
			return nil, err
		}
		r.Key = tagKey
		r.Name = r.Value
		values = append(values, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	return &IssueTagDetail{
		Key:          tagKey,
		Name:         tagKey,
		UniqueValues: len(values),
		TopValues:    values,
	}, nil
}

// ListIssueTagValues returns all values for a specific tag key within an issue's events.
func ListIssueTagValues(ctx context.Context, db *sql.DB, groupID, tagKey string) ([]TagValueRow, error) {
	path := "$." + tagKey
	query := `SELECT
		json_extract(tags_json, ?) AS tag_val,
		COUNT(*) AS cnt,
		MAX(occurred_at) AS last_seen,
		MIN(occurred_at) AS first_seen
	FROM events
	WHERE group_id = ?
	  AND json_extract(tags_json, ?) IS NOT NULL
	  AND json_extract(tags_json, ?) != ''
	GROUP BY tag_val
	ORDER BY cnt DESC`

	rows, err := db.QueryContext(ctx, query, path, groupID, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TagValueRow
	for rows.Next() {
		var r TagValueRow
		if err := rows.Scan(&r.Value, &r.Count, &r.LastSeen, &r.FirstSeen); err != nil {
			return nil, err
		}
		r.Key = tagKey
		r.Name = r.Value
		out = append(out, r)
	}
	return out, rows.Err()
}
