package telemetrycolumnar

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

type LogStore struct {
	source           *sql.DB
	columnar         *sql.DB
	projectSlugsMu   sync.Mutex
	projectSlugsByID map[string]string
}

func NewLogStore(source, columnar *sql.DB) *LogStore {
	return &LogStore{source: source, columnar: columnar}
}

func (s *LogStore) UpsertLogs(ctx context.Context, facts []telemetrybridge.ColumnarLogFact) error {
	if s == nil || s.columnar == nil || len(facts) == 0 {
		return nil
	}
	version := uint64(time.Now().UTC().UnixMilli())
	for _, fact := range facts {
		if _, err := s.columnar.ExecContext(ctx, `
			INSERT INTO telemetry_log_facts
				(id, organization_id, project_id, event_id, trace_id, span_id, release, environment, platform, level, logger, message, search_text, timestamp, attributes_json, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fact.ID,
			fact.OrganizationID,
			fact.ProjectID,
			fact.EventID,
			fact.TraceID,
			fact.SpanID,
			fact.Release,
			fact.Environment,
			fact.Platform,
			fact.Level,
			fact.Logger,
			fact.Message,
			fact.SearchText,
			fact.Timestamp,
			fact.AttributesJSON,
			version,
		); err != nil {
			return fmt.Errorf("insert columnar log fact %s: %w", fact.ID, err)
		}
	}
	return nil
}

func (s *LogStore) ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error) {
	return s.queryLogs(ctx, orgSlug, "", limit)
}

func (s *LogStore) SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	return s.queryLogs(ctx, orgSlug, rawQuery, limit)
}

func (s *LogStore) queryLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	orgID, err := s.organizationID(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	projectSlugs, err := s.projectSlugMap(ctx)
	if err != nil {
		return nil, err
	}
	sqlText, args := buildLogQuery(orgID, sqlite.ParseIssueSearch(rawQuery), limit)
	rows, err := s.columnar.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("query columnar logs: %w", err)
	}
	defer rows.Close()

	items := make([]store.DiscoverLog, 0, limit)
	for rows.Next() {
		var (
			item                                        store.DiscoverLog
			traceID, spanID, release, environment       string
			platform, level, logger, message, attrsJSON string
			at                                          time.Time
		)
		if err := rows.Scan(&item.EventID, &item.ProjectID, &traceID, &spanID, &release, &environment, &platform, &level, &logger, &message, &at, &attrsJSON); err != nil {
			return nil, fmt.Errorf("scan columnar log row: %w", err)
		}
		item.ProjectSlug = projectSlugs[item.ProjectID]
		item.TraceID = traceID
		item.SpanID = spanID
		item.Release = release
		item.Environment = environment
		item.Platform = platform
		item.Level = level
		item.Logger = logger
		item.Message = message
		item.Title = message
		item.Timestamp = at.UTC()
		item.Tags = sqlutil.ParseTags(attrsJSON)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func buildLogQuery(orgID string, parsed sqlite.IssueSearchQuery, limit int) (string, []any) {
	limit = clamp(limit, 1, 100)
	query := `
		SELECT event_id, project_id, trace_id, span_id, release, environment, platform, level, logger, message, timestamp, attributes_json
		  FROM telemetry_log_facts FINAL
		 WHERE organization_id = ?`
	args := []any{orgID}
	if parsed.Release != "" {
		query += ` AND release = ?`
		args = append(args, parsed.Release)
	}
	if parsed.Environment != "" {
		query += ` AND environment = ?`
		args = append(args, parsed.Environment)
	}
	if parsed.Level != "" {
		query += ` AND lower(level) = ?`
		args = append(args, strings.ToLower(parsed.Level))
	}
	for _, term := range parsed.Terms {
		query += ` AND positionCaseInsensitive(search_text, ?) > 0`
		args = append(args, term)
	}
	query += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, limit)
	return strings.TrimSpace(query), args
}

func (s *LogStore) organizationID(ctx context.Context, orgSlug string) (string, error) {
	var orgID string
	if err := s.source.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = ?`, orgSlug).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("resolve organization id: %w", err)
	}
	return orgID, nil
}

func (s *LogStore) projectSlugMap(ctx context.Context) (map[string]string, error) {
	s.projectSlugsMu.Lock()
	if s.projectSlugsByID != nil {
		cached := s.projectSlugsByID
		s.projectSlugsMu.Unlock()
		return cached, nil
	}
	s.projectSlugsMu.Unlock()

	rows, err := s.source.QueryContext(ctx, `SELECT id, slug FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("list project slugs: %w", err)
	}
	defer rows.Close()

	items := map[string]string{}
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, fmt.Errorf("scan project slug: %w", err)
		}
		items[id] = slug
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.projectSlugsMu.Lock()
	s.projectSlugsByID = items
	s.projectSlugsMu.Unlock()
	return items, nil
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
