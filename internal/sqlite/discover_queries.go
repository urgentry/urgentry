package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

type DiscoverIssueRef struct {
	ID          string
	ProjectID   string
	ProjectSlug string
}

func SearchDiscoverIssues(ctx context.Context, db *sql.DB, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	query, args := buildDiscoverIssueSearchQuery(orgSlug, store.DiscoverIssueSearchOptions{
		Filter: filter,
		Query:  rawQuery,
		Limit:  limit,
	}, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscoverIssues(rows)
}

func SearchDiscoverIssuesWithOptions(ctx context.Context, db *sql.DB, orgSlug string, opts store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error) {
	query, args := buildDiscoverIssueSearchQuery(orgSlug, opts, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscoverIssues(rows)
}

func SearchDiscoverIssueRefs(ctx context.Context, db *sql.DB, orgSlug, filter, rawQuery string, limit int) ([]DiscoverIssueRef, error) {
	query, args := buildDiscoverIssueRefSearchQuery(orgSlug, filter, rawQuery, limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DiscoverIssueRef, 0, limit)
	for rows.Next() {
		var item DiscoverIssueRef
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ProjectSlug); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func SearchLogs(ctx context.Context, db *sql.DB, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	query, args := buildDiscoverLogSearchQuery(orgSlug, rawQuery, limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscoverLogs(rows)
}

func ListRecentLogs(ctx context.Context, db *sql.DB, orgSlug string, limit int) ([]store.DiscoverLog, error) {
	return SearchLogs(ctx, db, orgSlug, "", limit)
}

func SearchTransactions(ctx context.Context, db *sql.DB, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error) {
	query, args := buildDiscoverTransactionSearchQuery(orgSlug, rawQuery, limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscoverTransactions(rows)
}

func ListRecentTransactions(ctx context.Context, db *sql.DB, orgSlug string, limit int) ([]store.DiscoverTransaction, error) {
	return SearchTransactions(ctx, db, orgSlug, "", limit)
}

func buildDiscoverIssueSearchQuery(orgSlug string, opts store.DiscoverIssueSearchOptions, offset int) (string, []any) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	clauses := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(orgSlug) != "" {
		clauses = append(clauses, "o.slug = ?")
		args = append(args, strings.TrimSpace(orgSlug))
	}
	if strings.TrimSpace(opts.ProjectID) != "" {
		clauses = append(clauses, "p.id = ?")
		args = append(args, strings.TrimSpace(opts.ProjectID))
	}
	issueClauses, issueArgs := buildIssueSearchClauses("", opts.Filter, opts.Query, opts.Environment, time.Time{})
	clauses = append(clauses, issueClauses[1:]...)
	args = append(args, issueArgs...)
	query := `SELECT g.id, p.id, p.slug, COALESCE(p.name, ''), COALESCE(p.platform, ''),
	        (SELECT COALESCE(MAX(e.release), '') FROM events e WHERE e.group_id = g.id),
	        (SELECT COALESCE(MAX(e.environment), '') FROM events e WHERE e.group_id = g.id),
	        g.title, g.culprit, g.level, g.status, g.first_seen, g.last_seen, g.times_seen,
	        COALESCE(g.short_id, 0), COALESCE(g.priority, 2), COALESCE(g.assignee, '')
	 FROM groups g
	 JOIN projects p ON p.id = g.project_id
	 JOIN organizations o ON o.id = p.organization_id
	 WHERE ` + strings.Join(clauses, " AND ") + `
	 ORDER BY ` + sqlutil.SortToOrderBy(normalizeDiscoverIssueSort(opts.Sort), "g.") + `
	 LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func normalizeDiscoverIssueSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "priority":
		return "priority"
	case "freq", "events":
		return "events"
	case "new", "first_seen":
		return "first_seen"
	case "date", "last_seen", "":
		return "last_seen"
	default:
		return "last_seen"
	}
}

func buildDiscoverIssueRefSearchQuery(orgSlug, filter, rawQuery string, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = 50
	}
	clauses := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(orgSlug) != "" {
		clauses = append(clauses, "o.slug = ?")
		args = append(args, strings.TrimSpace(orgSlug))
	}
	issueClauses, issueArgs := buildIssueSearchClauses("", filter, rawQuery, "", time.Time{})
	clauses = append(clauses, issueClauses[1:]...)
	args = append(args, issueArgs...)
	query := `SELECT g.id, p.id, p.slug
	 FROM groups g
	 JOIN projects p ON p.id = g.project_id
	 JOIN organizations o ON o.id = p.organization_id
	 WHERE ` + strings.Join(clauses, " AND ") + `
	 ORDER BY ` + sqlutil.SortToOrderBy("last_seen", "g.") + `
	 LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func buildDiscoverLogSearchQuery(orgSlug, rawQuery string, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = 50
	}
	parsed := ParseIssueSearch(rawQuery)
	clauses := []string{"LOWER(COALESCE(e.event_type, 'error')) = 'log'"}
	args := []any{}
	if strings.TrimSpace(orgSlug) != "" {
		clauses = append(clauses, "o.slug = ?")
		args = append(args, strings.TrimSpace(orgSlug))
	}
	if parsed.EventType != "" && parsed.EventType != "log" {
		clauses = append(clauses, "1 = 0")
	}
	if parsed.Release != "" {
		clauses = append(clauses, "COALESCE(e.release, '') = ?")
		args = append(args, parsed.Release)
	}
	if parsed.Environment != "" {
		clauses = append(clauses, "COALESCE(e.environment, '') = ?")
		args = append(args, parsed.Environment)
	}
	if parsed.Level != "" {
		clauses = append(clauses, "LOWER(COALESCE(e.level, '')) = ?")
		args = append(args, parsed.Level)
	}
	for _, term := range parsed.Terms {
		like := "%" + sqlutil.EscapeLike(term) + "%"
		clauses = append(clauses, `(
			e.title LIKE ? ESCAPE '\'
			OR e.message LIKE ? ESCAPE '\'
			OR e.culprit LIKE ? ESCAPE '\'
			OR COALESCE(json_extract(e.payload_json, '$.logger'), '') LIKE ? ESCAPE '\'
		)`)
		args = append(args, like, like, like, like)
	}
	query := `SELECT e.event_id, p.id, p.slug, COALESCE(e.title, ''), COALESCE(e.message, ''), COALESCE(e.level, ''),
	        COALESCE(e.platform, ''), COALESCE(e.culprit, ''), COALESCE(e.environment, ''), COALESCE(e.release, ''),
	        COALESCE(json_extract(e.payload_json, '$.logger'), ''),
	        COALESCE(json_extract(e.payload_json, '$.contexts.trace.trace_id'), ''),
	        COALESCE(json_extract(e.payload_json, '$.contexts.trace.span_id'), ''),
	        COALESCE(e.occurred_at, ''), COALESCE(e.tags_json, '{}')
	 FROM events e
	 JOIN projects p ON p.id = e.project_id
	 JOIN organizations o ON o.id = p.organization_id
	 WHERE ` + strings.Join(clauses, " AND ") + `
	 ORDER BY e.ingested_at DESC
	 LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func buildDiscoverTransactionSearchQuery(orgSlug, rawQuery string, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = 50
	}
	parsed := ParseIssueSearch(rawQuery)
	clauses := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(orgSlug) != "" {
		clauses = append(clauses, "o.slug = ?")
		args = append(args, strings.TrimSpace(orgSlug))
	}
	if parsed.EventType != "" && parsed.EventType != "transaction" {
		clauses = append(clauses, "1 = 0")
	}
	if parsed.Release != "" {
		clauses = append(clauses, "COALESCE(t.release, '') = ?")
		args = append(args, parsed.Release)
	}
	if parsed.Environment != "" {
		clauses = append(clauses, "COALESCE(t.environment, '') = ?")
		args = append(args, parsed.Environment)
	}
	if parsed.Level != "" {
		clauses = append(clauses, "LOWER(COALESCE(t.status, '')) = ?")
		args = append(args, parsed.Level)
	}
	for _, term := range parsed.Terms {
		like := "%" + sqlutil.EscapeLike(term) + "%"
		clauses = append(clauses, `(
			t.transaction_name LIKE ? ESCAPE '\'
			OR t.op LIKE ? ESCAPE '\'
			OR t.status LIKE ? ESCAPE '\'
			OR t.trace_id LIKE ? ESCAPE '\'
			OR p.slug LIKE ? ESCAPE '\'
		)`)
		args = append(args, like, like, like, like, like)
	}
	query := `SELECT t.event_id, p.id, p.slug, t.transaction_name, COALESCE(t.op, ''), COALESCE(t.status, ''),
	        COALESCE(t.platform, ''), COALESCE(t.environment, ''), COALESCE(t.release, ''), t.trace_id, t.span_id,
	        t.start_timestamp, t.end_timestamp, t.duration_ms, t.created_at
	 FROM transactions t
	 JOIN projects p ON p.id = t.project_id
	 JOIN organizations o ON o.id = p.organization_id
	 WHERE ` + strings.Join(clauses, " AND ") + `
	 ORDER BY t.created_at DESC
	 LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func scanDiscoverIssues(rows *sql.Rows) ([]store.DiscoverIssue, error) {
	var items []store.DiscoverIssue
	for rows.Next() {
		var (
			item                          store.DiscoverIssue
			release, environment          sql.NullString
			title, culprit, level, status sql.NullString
			firstSeen, lastSeen, assignee sql.NullString
			count, shortID, priority      sql.NullInt64
			projectID, projectSlug        sql.NullString
			projectName, projectPlatform  sql.NullString
		)
		if err := rows.Scan(&item.ID, &projectID, &projectSlug, &projectName, &projectPlatform, &release, &environment, &title, &culprit, &level, &status, &firstSeen, &lastSeen, &count, &shortID, &priority, &assignee); err != nil {
			return nil, err
		}
		item.ProjectID = sqlutil.NullStr(projectID)
		item.ProjectSlug = sqlutil.NullStr(projectSlug)
		item.ProjectName = sqlutil.NullStr(projectName)
		item.ProjectPlatform = sqlutil.NullStr(projectPlatform)
		item.Release = sqlutil.NullStr(release)
		item.Environment = sqlutil.NullStr(environment)
		item.Title = sqlutil.NullStr(title)
		item.Culprit = sqlutil.NullStr(culprit)
		item.Level = sqlutil.NullStr(level)
		item.Status = sqlutil.NullStr(status)
		item.FirstSeen = sqlutil.ParseDBTime(sqlutil.NullStr(firstSeen))
		item.LastSeen = sqlutil.ParseDBTime(sqlutil.NullStr(lastSeen))
		item.Assignee = sqlutil.NullStr(assignee)
		if count.Valid {
			item.Count = count.Int64
		}
		if shortID.Valid {
			item.ShortID = int(shortID.Int64)
		}
		if priority.Valid {
			item.Priority = int(priority.Int64)
		} else {
			item.Priority = 2
		}
		_ = projectID
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanDiscoverLogs(rows *sql.Rows) ([]store.DiscoverLog, error) {
	var items []store.DiscoverLog
	for rows.Next() {
		var (
			item                                           store.DiscoverLog
			projectID, projectSlug, title, message         sql.NullString
			level, platform, culprit, environment, release sql.NullString
			logger, traceID, spanID, occurredAt, tagsJSON  sql.NullString
		)
		if err := rows.Scan(&item.EventID, &projectID, &projectSlug, &title, &message, &level, &platform, &culprit, &environment, &release, &logger, &traceID, &spanID, &occurredAt, &tagsJSON); err != nil {
			return nil, err
		}
		item.ProjectID = sqlutil.NullStr(projectID)
		item.ProjectSlug = sqlutil.NullStr(projectSlug)
		item.Title = sqlutil.NullStr(title)
		item.Message = sqlutil.NullStr(message)
		item.Level = sqlutil.NullStr(level)
		item.Platform = sqlutil.NullStr(platform)
		item.Culprit = sqlutil.NullStr(culprit)
		item.Environment = sqlutil.NullStr(environment)
		item.Release = sqlutil.NullStr(release)
		item.Logger = sqlutil.NullStr(logger)
		item.TraceID = sqlutil.NullStr(traceID)
		item.SpanID = sqlutil.NullStr(spanID)
		item.Timestamp = sqlutil.ParseDBTime(sqlutil.NullStr(occurredAt))
		item.Tags = sqlutil.ParseTags(sqlutil.NullStr(tagsJSON))
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanDiscoverTransactions(rows *sql.Rows) ([]store.DiscoverTransaction, error) {
	var items []store.DiscoverTransaction
	for rows.Next() {
		var (
			item                                            store.DiscoverTransaction
			projectID, projectSlug, transaction, op, status sql.NullString
			platform, environment, release                  sql.NullString
			traceID, spanID, createdAt                      sql.NullString
			startTimestamp, endTimestamp                    sql.NullString
			durationMS                                      sql.NullFloat64
		)
		if err := rows.Scan(&item.EventID, &projectID, &projectSlug, &transaction, &op, &status, &platform, &environment, &release, &traceID, &spanID, &startTimestamp, &endTimestamp, &durationMS, &createdAt); err != nil {
			return nil, err
		}
		item.ProjectID = sqlutil.NullStr(projectID)
		item.ProjectSlug = sqlutil.NullStr(projectSlug)
		item.Transaction = sqlutil.NullStr(transaction)
		item.Op = sqlutil.NullStr(op)
		item.Status = sqlutil.NullStr(status)
		item.Platform = sqlutil.NullStr(platform)
		item.Environment = sqlutil.NullStr(environment)
		item.Release = sqlutil.NullStr(release)
		item.TraceID = sqlutil.NullStr(traceID)
		item.SpanID = sqlutil.NullStr(spanID)
		item.StartTimestamp = sqlutil.ParseDBTime(sqlutil.NullStr(startTimestamp))
		item.EndTimestamp = sqlutil.ParseDBTime(sqlutil.NullStr(endTimestamp))
		item.DurationMS = durationMS.Float64
		item.Timestamp = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		items = append(items, item)
	}
	return items, rows.Err()
}
