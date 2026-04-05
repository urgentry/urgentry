package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/search"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// IssueSearchQuery is the supported typed-search subset for issue queries.
type IssueSearchQuery struct {
	Terms       []string
	Status      string
	Release     string
	Environment string
	Level       string
	EventType   string
}

// ParseIssueSearch parses the supported Sentry-like issue query tokens.
// It delegates to the search package for full structured syntax support
// (negation, has:/!has:, tag filters, quoted strings) while preserving
// backward-compatible field mapping for all existing callers.
func ParseIssueSearch(raw string) IssueSearchQuery {
	f := search.Parse(raw)
	return IssueSearchQuery{
		Terms:       f.Terms,
		Status:      f.Status,
		Release:     f.Release,
		Environment: f.Environment,
		Level:       f.Level,
		EventType:   f.EventType,
	}
}

// ParseIssueSearchFull returns the full structured filter from the search
// package, supporting all operators including negation, has/!has, assigned,
// and tag-value filters. Used by buildIssueSearchClauses for SQL generation.
func ParseIssueSearchFull(raw string) search.Filter {
	return search.Parse(raw)
}

// SearchIssues returns issue rows matching the typed-search query.
func SearchIssues(ctx context.Context, db *sql.DB, filter, rawQuery string, limit int) ([]store.WebIssue, error) {
	query, args := buildIssueSearchListQuery("", filter, rawQuery, "", "last_seen", limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssuesWithCapacity(rows, limit)
}

// SearchProjectIssues returns project-scoped issue rows matching the typed-search query.
func SearchProjectIssues(ctx context.Context, db *sql.DB, projectID, filter, rawQuery string, limit int) ([]store.WebIssue, error) {
	query, args := buildIssueSearchListQuery(projectID, filter, rawQuery, "", "last_seen", limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssuesWithCapacity(rows, limit)
}

// SearchProjectIssuesPaged returns project-scoped issue rows with explicit
// DB-level LIMIT and OFFSET. Request limit+1 rows to detect whether a next
// page exists without a separate COUNT query.
func SearchProjectIssuesPaged(ctx context.Context, db *sql.DB, projectID, filter, rawQuery string, limit, offset int) ([]store.WebIssue, error) {
	query, args := buildIssueSearchListQuery(projectID, filter, rawQuery, "", "last_seen", limit, offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssuesWithCapacity(rows, limit)
}

func SearchProjectIssueIDs(ctx context.Context, db *sql.DB, projectID, filter, rawQuery string, limit int) ([]string, error) {
	query, args := buildIssueSearchIDQuery(projectID, filter, rawQuery, "", "last_seen", limit, 0)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SearchProjectIssueIDsPaged is like SearchProjectIssueIDs but with an
// explicit offset for DB-level pagination.
func SearchProjectIssueIDsPaged(ctx context.Context, db *sql.DB, projectID, filter, rawQuery string, limit, offset int) ([]string, error) {
	query, args := buildIssueSearchIDQuery(projectID, filter, rawQuery, "", "last_seen", limit, offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func buildIssueSearchListQuery(projectID, filter, rawQuery, environment, sort string, limit, offset int) (string, []any) {
	return buildIssueSearchListQuerySince(projectID, filter, rawQuery, environment, sort, time.Time{}, limit, offset)
}

func buildIssueSearchListQuerySince(projectID, filter, rawQuery, environment, sort string, since time.Time, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT g.id, g.title, g.culprit, g.level, g.status, g.first_seen, g.last_seen,
	        g.times_seen, g.assignee, g.priority, g.short_id, COALESCE(g.resolution_substatus, ''), COALESCE(g.resolved_in_release, ''), COALESCE(g.merged_into_group_id, '')
	 FROM groups g
	 WHERE `
	clauses, args := buildIssueSearchClauses(projectID, filter, rawQuery, environment, since)
	query += strings.Join(clauses, " AND ")
	query += ` ORDER BY ` + sqlutil.SortToOrderBy(sort, "g.")
	query += ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func buildIssueSearchIDQuery(projectID, filter, rawQuery, environment, sort string, limit, offset int) (string, []any) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT g.id FROM groups g WHERE `
	clauses, args := buildIssueSearchClauses(projectID, filter, rawQuery, environment, time.Time{})
	query += strings.Join(clauses, " AND ")
	query += ` ORDER BY ` + sqlutil.SortToOrderBy(sort, "g.")
	query += ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return query, args
}

func buildIssueSearchCountQuery(projectID, filter, rawQuery, environment string) (string, []any) {
	return buildIssueSearchCountQuerySince(projectID, filter, rawQuery, environment, time.Time{})
}

func buildIssueSearchCountQuerySince(projectID, filter, rawQuery, environment string, since time.Time) (string, []any) {
	clauses, args := buildIssueSearchClauses(projectID, filter, rawQuery, environment, since)
	return `SELECT COUNT(*) FROM groups g WHERE ` + strings.Join(clauses, " AND "), args
}

func buildIssueSearchClauses(projectID, filter, rawQuery, selectedEnvironment string, since time.Time) ([]string, []any) {
	parsed := ParseIssueSearchFull(rawQuery)
	clauses := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		clauses = append(clauses, "g.project_id = ?")
		args = append(args, strings.TrimSpace(projectID))
	}

	// Apply the UI filter tab as a fallback if the query doesn't set is:.
	status := parsed.Status
	if status == "" {
		status = normalizeIssueSearchStatus(filter)
	}

	// Merge environment from the selected dropdown with query-parsed environment.
	environment, impossible := resolveIssueSearchEnvironment(selectedEnvironment, parsed.Environment)
	if impossible {
		clauses = append(clauses, "1 = 0")
		return clauses, args
	}

	// Override filter status and environment on the parsed filter before SQL generation,
	// so the search.ToSQL function handles them uniformly.
	parsed.Status = status
	parsed.Environment = environment

	sc := search.ToSQL(parsed, search.SQLite, "g", sqlutil.EscapeLike)
	clauses = append(clauses, sc.Clauses...)
	args = append(args, sc.Args...)

	if !since.IsZero() {
		clauses = append(clauses, "g.last_seen >= ?")
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	return clauses, args
}

func normalizeIssueSearchStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "unresolved", "open":
		return "unresolved"
	case "resolved", "closed":
		return "resolved"
	case "ignored":
		return "ignored"
	default:
		return ""
	}
}

func resolveIssueSearchEnvironment(selected, fromQuery string) (string, bool) {
	selected = strings.TrimSpace(selected)
	fromQuery = strings.TrimSpace(fromQuery)
	switch {
	case selected == "" && fromQuery == "":
		return "", false
	case selected == "":
		return fromQuery, false
	case fromQuery == "":
		return selected, false
	case strings.EqualFold(selected, fromQuery):
		return selected, false
	default:
		return "", true
	}
}
