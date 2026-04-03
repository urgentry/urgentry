package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// ListIssues returns paginated issues with the total filtered count.
// When opts.Filter is "all" or empty, all statuses are included.
// The second return value is the filtered count for pagination.
func (s *WebStore) ListIssues(ctx context.Context, opts store.IssueListOpts) ([]store.WebIssue, int, error) {
	issues, err := s.queryIssues(ctx, opts)
	if err != nil {
		return nil, 0, err
	}
	count, err := s.countFilteredIssues(ctx, opts)
	if err != nil {
		return nil, 0, err
	}
	return issues, count, nil
}

func (s *WebStore) queryIssues(ctx context.Context, opts store.IssueListOpts) ([]store.WebIssue, error) {
	query, args := buildIssueSearchListQuerySince("", opts.Filter, opts.Query, opts.Environment, opts.Sort, opts.Since, opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

func (s *WebStore) countFilteredIssues(ctx context.Context, opts store.IssueListOpts) (int, error) {
	q, args := buildIssueSearchCountQuerySince("", opts.Filter, opts.Query, opts.Environment, opts.Since)
	return s.count(ctx, q, args...)
}

// GetIssue returns a single issue by ID.
func (s *WebStore) GetIssue(ctx context.Context, id string) (*store.WebIssue, error) {
	iss, err := GetIssue(ctx, s.db, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return iss, err
}

// GetIssues returns a batch of issues keyed by ID.
func (s *WebStore) GetIssues(ctx context.Context, ids []string) (map[string]store.WebIssue, error) {
	cleaned := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 {
		return map[string]store.WebIssue{}, nil
	}
	holders := strings.TrimSuffix(strings.Repeat("?,", len(cleaned)), ",")
	args := make([]any, 0, len(cleaned))
	for _, id := range cleaned {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, culprit, level, status, first_seen, last_seen, times_seen,
		        assignee, priority, short_id, COALESCE(resolution_substatus, ''),
		        COALESCE(resolved_in_release, ''), COALESCE(merged_into_group_id, '')
		   FROM groups
		  WHERE id IN (`+holders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanWebIssues(rows)
	if err != nil {
		return nil, err
	}
	result := make(map[string]store.WebIssue, len(items))
	for _, item := range items {
		result[item.ID] = item
	}
	return result, nil
}

// ListIssueComments returns the newest comments for an issue.
func (s *WebStore) ListIssueComments(ctx context.Context, groupID string, limit int) ([]store.IssueComment, error) {
	return NewGroupStore(s.db).ListIssueComments(ctx, groupID, limit)
}

// ListIssueActivity returns the newest timeline entries for an issue.
func (s *WebStore) ListIssueActivity(ctx context.Context, groupID string, limit int) ([]store.IssueActivityEntry, error) {
	return NewGroupStore(s.db).ListIssueActivity(ctx, groupID, limit)
}

// GetIssueWorkflowState returns per-user workflow state for an issue.
func (s *WebStore) GetIssueWorkflowState(ctx context.Context, groupID, userID string) (store.IssueWorkflowState, error) {
	return NewGroupStore(s.db).GetIssueWorkflowState(ctx, groupID, userID)
}

// BatchIssueWorkflowStates returns per-user workflow state for multiple issues in a single query.
func (s *WebStore) BatchIssueWorkflowStates(ctx context.Context, groupIDs []string, userID string) (map[string]store.IssueWorkflowState, error) {
	return NewGroupStore(s.db).BatchIssueWorkflowStates(ctx, groupIDs, userID)
}

// ListSimilarIssues returns nearby issues in the same project based on title and culprit overlap.
func (s *WebStore) ListSimilarIssues(ctx context.Context, groupID string, limit int) ([]store.WebIssue, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx,
		`WITH current_issue AS (
			SELECT project_id,
			       COALESCE(title, '') AS title,
			       COALESCE(culprit, '') AS culprit,
			       LOWER(TRIM(CASE
			           WHEN instr(COALESCE(title, ''), ':') > 0 THEN substr(COALESCE(title, ''), 1, instr(COALESCE(title, ''), ':') - 1)
			           ELSE COALESCE(title, '')
			       END)) AS title_prefix
			  FROM groups
			 WHERE id = ?
		),
		ranked AS (
			SELECT g.id, g.title, g.culprit, g.level, g.status, g.first_seen, g.last_seen, g.times_seen,
			       g.assignee, g.priority, g.short_id,
			       COALESCE(g.resolution_substatus, '') AS resolution_substatus,
			       COALESCE(g.resolved_in_release, '') AS resolved_in_release,
			       COALESCE(g.merged_into_group_id, '') AS merged_into_group_id,
			       (CASE
			            WHEN LOWER(TRIM(COALESCE(g.culprit, ''))) != '' AND LOWER(TRIM(COALESCE(g.culprit, ''))) = LOWER(TRIM(c.culprit)) THEN 4
			            ELSE 0
			        END
			        + CASE
			            WHEN LOWER(TRIM(COALESCE(g.title, ''))) = LOWER(TRIM(c.title)) AND LOWER(TRIM(c.title)) != '' THEN 3
			            ELSE 0
			          END
			        + CASE
			            WHEN LOWER(TRIM(CASE
			                    WHEN instr(COALESCE(g.title, ''), ':') > 0 THEN substr(COALESCE(g.title, ''), 1, instr(COALESCE(g.title, ''), ':') - 1)
			                    ELSE COALESCE(g.title, '')
			                END)) = c.title_prefix
			                 AND c.title_prefix != '' THEN 2
			            ELSE 0
			          END
			        + CASE
			            WHEN LOWER(TRIM(COALESCE(g.culprit, ''))) != ''
			                 AND LOWER(TRIM(c.culprit)) != ''
			                 AND (
			                     LOWER(TRIM(COALESCE(g.culprit, ''))) LIKE '%' || LOWER(TRIM(c.culprit)) || '%'
			                     OR LOWER(TRIM(c.culprit)) LIKE '%' || LOWER(TRIM(COALESCE(g.culprit, ''))) || '%'
			                 ) THEN 1
			            ELSE 0
			          END) AS match_score
			  FROM groups g
			  JOIN current_issue c ON c.project_id = g.project_id
			 WHERE g.id != ?
			   AND COALESCE(g.merged_into_group_id, '') = ''
		)
		SELECT id, title, culprit, level, status, first_seen, last_seen, times_seen,
		       assignee, priority, short_id, resolution_substatus, resolved_in_release, merged_into_group_id
		  FROM ranked
		 WHERE match_score > 0
		 ORDER BY match_score DESC, last_seen DESC
		 LIMIT ?`,
		groupID, groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

// ListMergedChildIssues returns issues merged into the given parent issue.
func (s *WebStore) ListMergedChildIssues(ctx context.Context, groupID string, limit int) ([]store.WebIssue, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, culprit, level, status, first_seen, last_seen, times_seen,
		        assignee, priority, short_id, COALESCE(resolution_substatus, ''),
		        COALESCE(resolved_in_release, ''), COALESCE(merged_into_group_id, '')
		   FROM groups
		  WHERE COALESCE(merged_into_group_id, '') = ?
		  ORDER BY last_seen DESC
		  LIMIT ?`,
		groupID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebIssues(rows)
}

// CountSearchGroups counts groups matching a search query, optionally filtered by status.
func (s *WebStore) CountSearchGroups(ctx context.Context, filter, search string) (int, error) {
	q, args := buildIssueSearchCountQuery("", filter, search, "")
	return s.count(ctx, q, args...)
}

// CountSearchGroupsForEnvironment counts groups matching a search query within one environment.
func (s *WebStore) CountSearchGroupsForEnvironment(ctx context.Context, env, filter, search string) (int, error) {
	q, args := buildIssueSearchCountQuery("", filter, search, env)
	return s.count(ctx, q, args...)
}

// ListTagFacets returns tag key/value/count tuples for a group.
func (s *WebStore) ListTagFacets(ctx context.Context, groupID string) ([]store.TagFacet, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT json_each.key, json_each.value, COUNT(*) as cnt
		 FROM events, json_each(tags_json)
		 WHERE group_id = ? AND tags_json IS NOT NULL AND tags_json != ''
		 GROUP BY json_each.key, json_each.value
		 ORDER BY cnt DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facets []store.TagFacet
	for rows.Next() {
		var f store.TagFacet
		if err := rows.Scan(&f.Key, &f.Value, &f.Count); err != nil {
			return nil, err
		}
		facets = append(facets, f)
	}
	return facets, rows.Err()
}

// TagDistribution computes the top tag value per key with percentages.
func (s *WebStore) TagDistribution(ctx context.Context, groupID string) ([]store.TagDist, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT json_each.key, json_each.value, COUNT(*) as cnt
		 FROM events, json_each(tags_json)
		 WHERE group_id = ? AND tags_json IS NOT NULL AND tags_json != ''
		 GROUP BY json_each.key, json_each.value
		 ORDER BY json_each.key, cnt DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type kvc struct {
		key, value string
		count      int
	}
	var all []kvc
	for rows.Next() {
		var k, v string
		var c int
		if err := rows.Scan(&k, &v, &c); err != nil {
			return nil, err
		}
		all = append(all, kvc{k, v, c})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	keyTotals := make(map[string]int)
	for _, t := range all {
		keyTotals[t.key] += t.count
	}

	seen := make(map[string]bool)
	var result []store.TagDist
	hueIdx := 0
	hues := []int{260, 160, 30, 340, 200, 80, 300, 120}
	for _, t := range all {
		if seen[t.key] {
			continue
		}
		seen[t.key] = true
		pct := 0
		if keyTotals[t.key] > 0 {
			pct = (t.count * 100) / keyTotals[t.key]
		}
		result = append(result, store.TagDist{
			Key:     t.key,
			Value:   t.value,
			Percent: pct,
			Color:   hues[hueIdx%len(hues)],
		})
		hueIdx++
	}
	return result, nil
}

// SearchIssues returns the top issue matches for typed search and command palette routes.
func (s *WebStore) SearchIssues(ctx context.Context, rawQuery string, limit int) ([]store.WebIssue, error) {
	return SearchIssues(ctx, s.db, "", rawQuery, limit)
}

// SearchDiscoverIssues returns org-scoped issue rows with project metadata.
func (s *WebStore) SearchDiscoverIssues(ctx context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	return SearchDiscoverIssues(ctx, s.db, orgSlug, filter, rawQuery, limit)
}

func (s *WebStore) SearchDiscoverIssuesWithOptions(ctx context.Context, orgSlug string, opts store.DiscoverIssueSearchOptions) ([]store.DiscoverIssue, error) {
	return SearchDiscoverIssuesWithOptions(ctx, s.db, orgSlug, opts)
}

// EventChartData returns event counts per day for the last N days.
func (s *WebStore) EventChartData(ctx context.Context, groupID string, days int) ([]store.ChartPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT date(occurred_at) as day, COUNT(*) as cnt
		 FROM events
		 WHERE group_id = ? AND occurred_at >= date('now', ? || ' days')
		 GROUP BY day ORDER BY day`,
		groupID, fmt.Sprintf("-%d", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dayCounts := make(map[string]int)
	for rows.Next() {
		var day string
		var cnt int
		if err := rows.Scan(&day, &cnt); err != nil {
			return nil, err
		}
		dayCounts[day] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	points := make([]store.ChartPoint, days)
	maxCount := 1
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i))
		dayStr := d.Format("2006-01-02")
		cnt := dayCounts[dayStr]
		if cnt > maxCount {
			maxCount = cnt
		}
		points[i] = store.ChartPoint{Day: dayStr, Count: cnt}
	}

	for i := range points {
		points[i].Height = (points[i].Count * 100) / maxCount
		if points[i].Count > 0 && points[i].Height < 2 {
			points[i].Height = 2
		}
	}
	return points, nil
}

// IssueDiffBase returns first and latest issue-diff comparison rows for a group.
func (s *WebStore) IssueDiffBase(ctx context.Context, groupID string) (*store.IssueDiffBase, *store.IssueDiffBase, error) {
	scan := func(order string) (*store.IssueDiffBase, error) {
		row := s.db.QueryRowContext(ctx,
			`SELECT level, release, environment, user_identifier
			 FROM events WHERE group_id = ? ORDER BY ingested_at `+order+` LIMIT 1`, groupID)
		var level, release, environment, userIdentifier sql.NullString
		if err := row.Scan(&level, &release, &environment, &userIdentifier); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
		return &store.IssueDiffBase{
			Level:          sqlutil.NullStr(level),
			Release:        sqlutil.NullStr(release),
			Environment:    sqlutil.NullStr(environment),
			UserIdentifier: sqlutil.NullStr(userIdentifier),
		}, nil
	}
	first, err := scan("ASC")
	if err != nil || first == nil {
		return first, nil, err
	}
	latest, err := scan("DESC")
	if err != nil {
		return nil, nil, err
	}
	return first, latest, nil
}

func scanWebIssues(rows *sql.Rows) ([]store.WebIssue, error) {
	var issues []store.WebIssue
	for rows.Next() {
		var iss store.WebIssue
		var title, culprit, level, status, assignee sql.NullString
		var resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
		var firstSeen, lastSeen sql.NullString
		var count sql.NullInt64
		var priority sql.NullInt64
		var shortID sql.NullInt64
		if err := rows.Scan(&iss.ID, &title, &culprit, &level, &status,
			&firstSeen, &lastSeen, &count, &assignee, &priority, &shortID, &resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID); err != nil {
			return nil, err
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
		issues = append(issues, iss)
	}
	return issues, rows.Err()
}

func scanWebIssueRow(row *sql.Row) (*store.WebIssue, error) {
	var iss store.WebIssue
	var title, culprit, level, status, assignee sql.NullString
	var resolutionSubstatus, resolvedInRelease, mergedIntoGroupID sql.NullString
	var firstSeen, lastSeen sql.NullString
	var count sql.NullInt64
	var priority sql.NullInt64
	var shortID sql.NullInt64
	err := row.Scan(&iss.ID, &title, &culprit, &level, &status,
		&firstSeen, &lastSeen, &count, &assignee, &priority, &shortID, &resolutionSubstatus, &resolvedInRelease, &mergedIntoGroupID)
	if err != nil {
		return nil, err
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
	return &iss, nil
}
