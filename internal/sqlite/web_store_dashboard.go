package sqlite

import (
	"context"
	"strings"
	"time"

	"urgentry/internal/store"
)

// DashboardSummary returns the dashboard counters needed for the landing page
// in one round-trip instead of several independent count queries.
func (s *WebStore) DashboardSummary(ctx context.Context, now time.Time) (store.DashboardSummary, error) {
	now = now.UTC()
	currentStart := now.Add(-24 * time.Hour).Format(time.RFC3339)
	previousStart := now.Add(-48 * time.Hour).Format(time.RFC3339)

	var summary store.DashboardSummary
	err := s.db.QueryRowContext(ctx,
		`SELECT
			(SELECT COUNT(*) FROM events),
			(SELECT COUNT(*) FROM groups WHERE status = 'unresolved'),
			(SELECT COUNT(*) FROM events WHERE occurred_at >= ?),
			(SELECT COUNT(*) FROM events WHERE occurred_at >= ? AND occurred_at < ?),
			(SELECT COUNT(*) FROM groups WHERE status = 'unresolved' AND first_seen >= ?),
			(SELECT COUNT(*) FROM groups WHERE status = 'unresolved' AND first_seen >= ? AND first_seen < ?),
			(SELECT COUNT(DISTINCT user_identifier) FROM events WHERE user_identifier != ''),
			(SELECT COUNT(DISTINCT user_identifier) FROM events WHERE user_identifier != '' AND occurred_at >= ?),
			(SELECT COUNT(DISTINCT user_identifier) FROM events WHERE user_identifier != '' AND occurred_at >= ? AND occurred_at < ?)`,
		currentStart,
		previousStart, currentStart,
		currentStart,
		previousStart, currentStart,
		currentStart,
		previousStart, currentStart,
	).Scan(
		&summary.TotalEvents,
		&summary.UnresolvedGroups,
		&summary.EventsCurrent,
		&summary.EventsPrevious,
		&summary.ErrorsCurrent,
		&summary.ErrorsPrevious,
		&summary.UsersTotal,
		&summary.UsersCurrent,
		&summary.UsersPrevious,
	)
	return summary, err
}

// ListBurningIssues returns the top unresolved issues with the steepest recent
// event growth over the previous 4-day window.
func (s *WebStore) ListBurningIssues(ctx context.Context, now time.Time, limit int) ([]store.BurningIssueSummary, error) {
	if limit <= 0 {
		return nil, nil
	}
	now = now.UTC()
	currentStart := now.Add(-4 * 24 * time.Hour).Format(time.RFC3339)
	previousStart := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)

	rows, err := s.db.QueryContext(ctx,
		`WITH issue_activity AS (
			SELECT
				g.id,
				g.title,
				g.last_seen,
				COALESCE(SUM(CASE WHEN e.occurred_at >= ? THEN 1 ELSE 0 END), 0) AS curr_count,
				COALESCE(SUM(CASE WHEN e.occurred_at >= ? AND e.occurred_at < ? THEN 1 ELSE 0 END), 0) AS prev_count
			FROM groups g
			LEFT JOIN events e ON e.group_id = g.id AND e.occurred_at >= ?
			WHERE g.status = 'unresolved'
			GROUP BY g.id, g.title, g.last_seen
		)
		SELECT
			id,
			title,
			CASE
				WHEN prev_count = 0 THEN CASE WHEN curr_count = 0 THEN 0 ELSE 100 END
				ELSE ((curr_count - prev_count) * 100) / prev_count
			END AS change_pct
		FROM issue_activity
		ORDER BY change_pct DESC, last_seen DESC
		LIMIT ?`,
		currentStart,
		previousStart, currentStart,
		previousStart,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]store.BurningIssueSummary, 0, limit)
	for rows.Next() {
		var item store.BurningIssueSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.Change); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *WebStore) CountEvents(ctx context.Context) (int, error) {
	return s.count(ctx, "SELECT COUNT(*) FROM events")
}

func (s *WebStore) CountGroups(ctx context.Context) (int, error) {
	return s.count(ctx, "SELECT COUNT(*) FROM groups")
}

func (s *WebStore) CountGroupsByStatus(ctx context.Context, status string) (int, error) {
	return s.count(ctx, "SELECT COUNT(*) FROM groups WHERE status = ?", status)
}

func (s *WebStore) CountGroupsSince(ctx context.Context, since time.Time, status string) (int, error) {
	ts := since.Format(time.RFC3339)
	if status != "" {
		return s.count(ctx,
			`SELECT COUNT(*) FROM groups WHERE first_seen >= ? AND status = ?`,
			ts, status,
		)
	}
	return s.count(ctx, `SELECT COUNT(*) FROM groups WHERE first_seen >= ?`, ts)
}

func (s *WebStore) CountGroupsForEnvironment(ctx context.Context, env, status string) (int, error) {
	q := `SELECT COUNT(DISTINCT g.id) FROM groups g
	      INNER JOIN events e ON e.group_id = g.id
	      WHERE e.environment = ?`
	args := []any{env}
	if status != "" {
		q += ` AND g.status = ?`
		args = append(args, status)
	}
	var count int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&count)
	return count, err
}

func (s *WebStore) CountAllGroupsByStatus(ctx context.Context) (total, unresolved, resolved, ignored int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM groups GROUP BY status`)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return 0, 0, 0, 0, err
		}
		total += cnt
		switch status {
		case "unresolved", "":
			unresolved += cnt
		case "resolved":
			resolved += cnt
		case "ignored":
			ignored += cnt
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0, err
	}
	return total, unresolved, resolved, ignored, nil
}

func (s *WebStore) CountAllGroupsForEnvironment(ctx context.Context, env string) (total, unresolved, resolved, ignored int, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(g.status, ''), COUNT(DISTINCT g.id)
		 FROM groups g
		 JOIN events e ON e.group_id = g.id
		 WHERE e.environment = ?
		 GROUP BY COALESCE(g.status, '')`,
		env,
	)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return 0, 0, 0, 0, err
		}
		total += cnt
		switch status {
		case "unresolved", "":
			unresolved += cnt
		case "resolved":
			resolved += cnt
		case "ignored":
			ignored += cnt
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0, err
	}
	return total, unresolved, resolved, ignored, nil
}

func (s *WebStore) CountEventsSince(ctx context.Context, since time.Time) (int, error) {
	return s.count(ctx, "SELECT COUNT(*) FROM events WHERE occurred_at >= ?", since.Format(time.RFC3339))
}

func (s *WebStore) CountDistinctUsers(ctx context.Context) (int, error) {
	return s.count(ctx, `SELECT COUNT(DISTINCT user_identifier) FROM events WHERE user_identifier != ''`)
}

func (s *WebStore) CountDistinctUsersSince(ctx context.Context, since time.Time) (int, error) {
	return s.count(ctx,
		`SELECT COUNT(DISTINCT user_identifier) FROM events
		 WHERE user_identifier != '' AND occurred_at >= ?`,
		since.Format(time.RFC3339),
	)
}

func (s *WebStore) ListEnvironments(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT environment FROM events
		 WHERE environment IS NOT NULL AND environment != ''
		 ORDER BY environment`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var envs []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
}

func (s *WebStore) BatchUserCounts(ctx context.Context, groupIDs []string) (map[string]int, error) {
	if len(groupIDs) == 0 {
		return make(map[string]int), nil
	}
	placeholders := make([]string, len(groupIDs))
	args := make([]any, len(groupIDs))
	for i, id := range groupIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT group_id, COUNT(DISTINCT user_identifier) as user_count
	      FROM events
	      WHERE group_id IN (` + strings.Join(placeholders, ",") + `) AND user_identifier != ''
	      GROUP BY group_id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int, len(groupIDs))
	for rows.Next() {
		var gid string
		var cnt int
		if err := rows.Scan(&gid, &cnt); err != nil {
			return nil, err
		}
		counts[gid] = cnt
	}
	return counts, rows.Err()
}

func (s *WebStore) BatchSparklines(ctx context.Context, groupIDs []string, buckets int, window time.Duration) (map[string][]int, error) {
	return s.batchSparklines(ctx, groupIDs, buckets, window, time.Now().UTC())
}

func (s *WebStore) batchSparklines(ctx context.Context, groupIDs []string, buckets int, window time.Duration, now time.Time) (map[string][]int, error) {
	if len(groupIDs) == 0 || buckets <= 0 {
		return make(map[string][]int), nil
	}

	placeholders := make([]string, len(groupIDs))
	args := make([]any, 0, len(groupIDs)+2)
	args = append(args, now.Format(time.RFC3339))
	bucketWidth := window.Hours() / float64(buckets)
	args = append(args, bucketWidth)
	for i, id := range groupIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	cutoff := now.Add(-window).Format(time.RFC3339)
	args = append(args, cutoff)

	q := `SELECT group_id,
	             CAST((julianday(?) - julianday(occurred_at)) / (? / 24.0) AS INTEGER) as bucket,
	             COUNT(*) as cnt
	      FROM events
	      WHERE group_id IN (` + strings.Join(placeholders, ",") + `) AND occurred_at >= ?
	      GROUP BY group_id, bucket`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]int, len(groupIDs))
	for _, gid := range groupIDs {
		result[gid] = make([]int, buckets)
	}
	for rows.Next() {
		var gid string
		var bucket, cnt int
		if err := rows.Scan(&gid, &bucket, &cnt); err != nil {
			return nil, err
		}
		if bucket >= buckets {
			bucket = buckets - 1
		}
		if bucket < 0 {
			continue
		}
		if bars, ok := result[gid]; ok {
			idx := buckets - 1 - bucket
			if idx >= 0 && idx < buckets {
				bars[idx] = cnt
			}
		}
	}
	return result, rows.Err()
}
