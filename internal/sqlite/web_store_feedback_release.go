package sqlite

import (
	"context"
	"database/sql"

	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

// ListFeedback returns recent user feedback rows.
func (s *WebStore) ListFeedback(ctx context.Context, limit int) ([]store.FeedbackRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, email, comments, event_id, group_id, created_at
		 FROM user_feedback ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []store.FeedbackRow
	for rows.Next() {
		var item store.FeedbackRow
		var name, email, comments, eventID, groupID, createdAt sql.NullString
		if err := rows.Scan(&item.ID, &name, &email, &comments, &eventID, &groupID, &createdAt); err != nil {
			return nil, err
		}
		item.Name = sqlutil.NullStr(name)
		item.Email = sqlutil.NullStr(email)
		item.Comments = sqlutil.NullStr(comments)
		item.EventID = sqlutil.NullStr(eventID)
		item.GroupID = sqlutil.NullStr(groupID)
		item.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetFeedback returns a single feedback row by ID.
func (s *WebStore) GetFeedback(ctx context.Context, id string) (*store.FeedbackRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, email, comments, event_id, group_id, created_at
		 FROM user_feedback WHERE id = ?`, id)
	var item store.FeedbackRow
	var name, email, comments, eventID, groupID, createdAt sql.NullString
	if err := row.Scan(&item.ID, &name, &email, &comments, &eventID, &groupID, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Name = sqlutil.NullStr(name)
	item.Email = sqlutil.NullStr(email)
	item.Comments = sqlutil.NullStr(comments)
	item.EventID = sqlutil.NullStr(eventID)
	item.GroupID = sqlutil.NullStr(groupID)
	item.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
	return &item, nil
}

// ListReleases returns release list rows with health stats.
func (s *WebStore) ListReleases(ctx context.Context, limit int) ([]store.ReleaseRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.version, r.created_at, COALESCE(e.cnt, 0),
		        COALESCE(SUM(rs.quantity), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'errored' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'crashed' THEN rs.quantity ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN rs.status = 'abnormal' THEN rs.quantity ELSE 0 END), 0),
		        COUNT(DISTINCT CASE WHEN rs.distinct_id IS NOT NULL AND rs.distinct_id != '' THEN rs.distinct_id END),
		        MAX(rs.created_at)
		 FROM releases r
		 LEFT JOIN (
		     SELECT release, COUNT(*) AS cnt FROM events WHERE release != '' GROUP BY release
		 ) e ON e.release = r.version
		 LEFT JOIN release_sessions rs ON rs.release_version = r.version
		 GROUP BY r.version, r.created_at, e.cnt
		 ORDER BY r.created_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var releases []store.ReleaseRow
	for rows.Next() {
		var rel store.ReleaseRow
		var createdAt, lastSessionAt sql.NullString
		if err := rows.Scan(&rel.Version, &createdAt, &rel.EventCount, &rel.SessionCount, &rel.ErroredSessions, &rel.CrashedSessions, &rel.AbnormalSessions, &rel.AffectedUsers, &lastSessionAt); err != nil {
			return nil, err
		}
		rel.CreatedAt = sqlutil.ParseDBTime(sqlutil.NullStr(createdAt))
		rel.LastSessionAt = sqlutil.ParseDBTime(sqlutil.NullStr(lastSessionAt))
		if rel.SessionCount > 0 {
			bad := rel.CrashedSessions + rel.AbnormalSessions
			if bad < 0 {
				bad = 0
			}
			if bad > rel.SessionCount {
				bad = rel.SessionCount
			}
			rel.CrashFreeRate = (float64(rel.SessionCount-bad) / float64(rel.SessionCount)) * 100
		} else {
			rel.CrashFreeRate = 100
		}
		releases = append(releases, rel)
	}
	return releases, rows.Err()
}
