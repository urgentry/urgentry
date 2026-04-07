package telemetryquery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/discovershared"
	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func (s *bridgeService) ListRecentLogs(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverLog, error) {
	return s.listLogs(ctx, orgSlug, "", limit)
}

func (s *bridgeService) SearchLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	return s.listLogs(ctx, orgSlug, rawQuery, limit)
}

func (s *bridgeService) listLogs(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, error) {
	scope, err := s.orgScope(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceDiscoverLogs, scope, telemetrybridge.FamilyLogs); err != nil {
		return nil, err
	}
	if items, ok := s.cachedLogsResult(orgSlug, rawQuery, limit); ok {
		return items, nil
	}
	parsed := sqlite.ParseIssueSearch(rawQuery)
	query := `
		SELECT id, event_id, project_id, COALESCE(trace_id, ''), COALESCE(span_id, ''), COALESCE(release, ''),
		       COALESCE(environment, ''), COALESCE(platform, ''), COALESCE(level, ''), COALESCE(logger, ''),
		       COALESCE(message, ''), timestamp, COALESCE(attributes_json::text, '{}')
		  FROM telemetry.log_facts
		 WHERE organization_id = $1`
	args := []any{scope.OrganizationID}
	next := 2
	if parsed.Release != "" {
		query += fmt.Sprintf(` AND release = $%d`, next)
		args = append(args, parsed.Release)
		next++
	}
	if parsed.Environment != "" {
		query += fmt.Sprintf(` AND environment = $%d`, next)
		args = append(args, parsed.Environment)
		next++
	}
	if parsed.Level != "" {
		query += fmt.Sprintf(` AND LOWER(level) = $%d`, next)
		args = append(args, strings.ToLower(parsed.Level))
		next++
	}
	for _, term := range parsed.Terms {
		query += fmt.Sprintf(` AND search_text ILIKE $%d`, next)
		args = append(args, "%"+term+"%")
		next++
	}
	query += fmt.Sprintf(` ORDER BY timestamp DESC LIMIT $%d`, next)
	args = append(args, discovershared.Clamp(limit, 1, 100))
	rows, err := s.bridgeDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list bridge logs: %w", err)
	}
	defer rows.Close()
	items := make([]store.DiscoverLog, 0, limit)
	projectSlugs, err := s.projectSlugMap(ctx)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			item                                        store.DiscoverLog
			id                                          string
			traceID, spanID, release, environment       string
			platform, level, logger, message, attrsJSON string
			at                                          time.Time
		)
		if err := rows.Scan(&id, &item.EventID, &item.ProjectID, &traceID, &spanID, &release, &environment, &platform, &level, &logger, &message, &at, &attrsJSON); err != nil {
			return nil, fmt.Errorf("scan bridge log row: %w", err)
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
	s.storeLogsResult(orgSlug, rawQuery, limit, items)
	return items, nil
}
