package postgrescontrol

import (
	"context"
	"database/sql"
	"strings"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type IssueReadStore struct {
	controlDB *sql.DB
	queryDB   *sql.DB
}

func NewIssueReadStore(controlDB, queryDB *sql.DB) *IssueReadStore {
	return &IssueReadStore{controlDB: controlDB, queryDB: queryDB}
}

func (s *IssueReadStore) GetIssue(ctx context.Context, id string) (*store.WebIssue, error) {
	row := s.controlDB.QueryRowContext(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(culprit, ''), COALESCE(level, ''), COALESCE(status, ''),
		       first_seen, last_seen, COALESCE(times_seen, 0),
		       COALESCE(NULLIF(assignee_user_id, ''), NULLIF(assignee_team_id, ''), ''), COALESCE(priority, 2), COALESCE(short_id, 0),
		       COALESCE(substatus, ''), COALESCE(resolved_in_release, ''), COALESCE(merged_into_group_id, '')
		  FROM groups
		 WHERE id = $1`,
		strings.TrimSpace(id),
	)
	return scanWebIssue(row)
}

func (s *IssueReadStore) SearchProjectIssues(ctx context.Context, projectID, filter, rawQuery string, limit int) ([]store.WebIssue, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	if s.queryDB == nil {
		return s.searchProjectIssuesDirect(ctx, projectID, filter, rawQuery, limit)
	}
	ids, err := sqlite.SearchProjectIssueIDs(ctx, s.queryDB, projectID, filter, rawQuery, limit)
	if err != nil {
		return nil, err
	}
	if shouldFallbackToControlIssues(filter, rawQuery) && len(ids) == 0 {
		return s.searchProjectIssuesDirect(ctx, projectID, filter, rawQuery, limit)
	}
	return s.loadIssues(ctx, ids)
}

func (s *IssueReadStore) SearchProjectIssuesPaged(ctx context.Context, projectID, filter, rawQuery string, limit, offset int) ([]store.WebIssue, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}
	if s.queryDB == nil {
		return s.searchProjectIssuesDirectPaged(ctx, projectID, filter, rawQuery, limit, offset)
	}
	ids, err := sqlite.SearchProjectIssueIDsPaged(ctx, s.queryDB, projectID, filter, rawQuery, limit, offset)
	if err != nil {
		return nil, err
	}
	if shouldFallbackToControlIssues(filter, rawQuery) && len(ids) == 0 {
		return s.searchProjectIssuesDirectPaged(ctx, projectID, filter, rawQuery, limit, offset)
	}
	return s.loadIssues(ctx, ids)
}

func (s *IssueReadStore) SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	if s.queryDB == nil {
		return s.searchDiscoverIssuesDirect(ctx, orgSlug, filter, rawQuery, limit)
	}
	refs, err := sqlite.SearchDiscoverIssueRefs(ctx, s.queryDB, orgSlug, filter, rawQuery, limit)
	if err != nil {
		return nil, err
	}
	items := make([]store.DiscoverIssue, 0, len(refs))
	for _, ref := range refs {
		issue, err := s.GetIssue(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		if issue == nil {
			continue
		}
		items = append(items, store.DiscoverIssue{
			ID:          issue.ID,
			ProjectID:   ref.ProjectID,
			ProjectSlug: ref.ProjectSlug,
			Title:       issue.Title,
			Culprit:     issue.Culprit,
			Level:       issue.Level,
			Status:      issue.Status,
			FirstSeen:   issue.FirstSeen,
			LastSeen:    issue.LastSeen,
			Count:       issue.Count,
			ShortID:     issue.ShortID,
			Priority:    issue.Priority,
			Assignee:    issue.Assignee,
		})
	}
	return items, nil
}

func (s *IssueReadStore) searchProjectIssuesDirect(ctx context.Context, projectID, filter, rawQuery string, limit int) ([]store.WebIssue, error) {
	query := `SELECT id, COALESCE(title, ''), COALESCE(culprit, ''), COALESCE(level, ''), COALESCE(status, ''),
	                 first_seen, last_seen, COALESCE(times_seen, 0),
	                 COALESCE(NULLIF(assignee_user_id, ''), NULLIF(assignee_team_id, ''), ''), COALESCE(priority, 2), COALESCE(short_id, 0),
	                 COALESCE(substatus, ''), COALESCE(resolved_in_release, ''), COALESCE(merged_into_group_id, '')
	            FROM groups
	           WHERE project_id = $1`
	args := []any{projectID}
	if status := normalizeIssueSearchStatus(filter, rawQuery); status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY last_seen DESC NULLS LAST LIMIT $` + itoa(len(args)+1)
	args = append(args, clampLimit(limit, 100))
	rows, err := s.controlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

func (s *IssueReadStore) searchProjectIssuesDirectPaged(ctx context.Context, projectID, filter, rawQuery string, limit, offset int) ([]store.WebIssue, error) {
	query := `SELECT id, COALESCE(title, ''), COALESCE(culprit, ''), COALESCE(level, ''), COALESCE(status, ''),
	                 first_seen, last_seen, COALESCE(times_seen, 0),
	                 COALESCE(NULLIF(assignee_user_id, ''), NULLIF(assignee_team_id, ''), ''), COALESCE(priority, 2), COALESCE(short_id, 0),
	                 COALESCE(substatus, ''), COALESCE(resolved_in_release, ''), COALESCE(merged_into_group_id, '')
	            FROM groups
	           WHERE project_id = $1`
	args := []any{projectID}
	if status := normalizeIssueSearchStatus(filter, rawQuery); status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY last_seen DESC NULLS LAST LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2)
	args = append(args, clampLimit(limit, 100), offset)
	rows, err := s.controlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

func (s *IssueReadStore) searchDiscoverIssuesDirect(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	query := `SELECT g.id, p.id, p.slug, COALESCE(g.title, ''), COALESCE(g.culprit, ''), COALESCE(g.level, ''), COALESCE(g.status, ''),
	                 g.first_seen, g.last_seen, COALESCE(g.times_seen, 0), COALESCE(g.short_id, 0), COALESCE(g.priority, 2),
	                 COALESCE(NULLIF(g.assignee_user_id, ''), NULLIF(g.assignee_team_id, ''), '')
	            FROM groups g
	            JOIN projects p ON p.id = g.project_id
	            JOIN organizations o ON o.id = p.organization_id
	           WHERE o.slug = $1`
	args := []any{strings.TrimSpace(orgSlug)}
	if status := normalizeIssueSearchStatus(filter, rawQuery); status != "" {
		query += ` AND g.status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY g.last_seen DESC NULLS LAST LIMIT $` + itoa(len(args)+1)
	args = append(args, clampLimit(limit, 50))
	rows, err := s.controlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscoverIssues(rows)
}

func (s *IssueReadStore) loadIssues(ctx context.Context, ids []string) ([]store.WebIssue, error) {
	items := make([]store.WebIssue, 0, len(ids))
	for _, id := range ids {
		issue, err := s.GetIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		if issue == nil {
			continue
		}
		items = append(items, *issue)
	}
	return items, nil
}

func normalizeIssueSearchStatus(filter, rawQuery string) string {
	parsed := sqlite.ParseIssueSearch(rawQuery)
	if parsed.Status != "" {
		return parsed.Status
	}
	switch strings.ToLower(strings.TrimSpace(filter)) {
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

func shouldFallbackToControlIssues(filter, rawQuery string) bool {
	return strings.TrimSpace(filter) == "" && strings.TrimSpace(rawQuery) == ""
}

func clampLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	return limit
}

func scanWebIssues(rows *sql.Rows) ([]store.WebIssue, error) {
	var items []store.WebIssue
	for rows.Next() {
		item, err := scanWebIssue(rows)
		if err != nil {
			return nil, err
		}
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, rows.Err()
}

func scanWebIssue(scanner interface{ Scan(dest ...any) error }) (*store.WebIssue, error) {
	var (
		item                                                      store.WebIssue
		title, culprit, level, status, assignee                   sql.NullString
		resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
		firstSeen, lastSeen                                       sql.NullTime
		count, priority, shortID                                  sql.NullInt64
	)
	if err := scanner.Scan(
		&item.ID, &title, &culprit, &level, &status, &firstSeen, &lastSeen, &count, &assignee, &priority, &shortID,
		&resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Title = nullString(title)
	item.Culprit = nullString(culprit)
	item.Level = nullString(level)
	item.Status = nullString(status)
	if item.Status == "" {
		item.Status = "unresolved"
	}
	item.FirstSeen = nullTime(firstSeen)
	item.LastSeen = nullTime(lastSeen)
	if count.Valid {
		item.Count = count.Int64
	}
	item.Assignee = nullString(assignee)
	if priority.Valid {
		item.Priority = int(priority.Int64)
	} else {
		item.Priority = 2
	}
	if shortID.Valid {
		item.ShortID = int(shortID.Int64)
	}
	item.ResolutionSubstatus = nullString(resolutionSubstatus)
	item.ResolvedInRelease = nullString(resolvedInRelease)
	item.MergedIntoGroupID = nullString(mergedIntoGroupID)
	return &item, nil
}

func scanDiscoverIssues(rows *sql.Rows) ([]store.DiscoverIssue, error) {
	var items []store.DiscoverIssue
	for rows.Next() {
		var (
			item                                                            store.DiscoverIssue
			projectID, projectSlug, title, culprit, level, status, assignee sql.NullString
			firstSeen, lastSeen                                             sql.NullTime
			count, shortID, priority                                        sql.NullInt64
		)
		if err := rows.Scan(
			&item.ID, &projectID, &projectSlug, &title, &culprit, &level, &status,
			&firstSeen, &lastSeen, &count, &shortID, &priority, &assignee,
		); err != nil {
			return nil, err
		}
		item.ProjectID = nullString(projectID)
		item.ProjectSlug = nullString(projectSlug)
		item.Title = nullString(title)
		item.Culprit = nullString(culprit)
		item.Level = nullString(level)
		item.Status = nullString(status)
		item.FirstSeen = nullTime(firstSeen)
		item.LastSeen = nullTime(lastSeen)
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
		item.Assignee = nullString(assignee)
		items = append(items, item)
	}
	return items, rows.Err()
}
