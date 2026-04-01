package telemetryquery

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discovershared"
	"urgentry/internal/store"
)

func buildBridgeLogsFetchSQL(query discover.Query, state bridgeDiscoverContext, limit int) (string, []any, int, error) {
	builder := &bridgeSQLBuilder{}
	base := `SELECT l.event_id, l.project_id, COALESCE(e.title, l.message, ''), COALESCE(l.message, ''), COALESCE(l.level, ''),
		COALESCE(l.platform, ''), COALESCE(e.culprit, ''), COALESCE(l.environment, ''), COALESCE(l.release, ''),
		COALESCE(l.logger, ''), COALESCE(l.trace_id, ''), COALESCE(l.span_id, ''), l.timestamp
	FROM telemetry.log_facts l
	LEFT JOIN telemetry.event_facts e ON e.id = l.id`
	clauses := []string{`l.organization_id = ` + builder.Add(state.organizationID)}
	applyBridgeScopeClauses(query, builder, "l.project_id", &clauses)
	plan, err := discovershared.PlanFilter(query, limit, discovershared.FilterPlanConfig{
		Builder:             builder,
		FieldExpr:           bridgeLogsFieldExpr,
		TimestampExpr:       "l.timestamp",
		DefaultOrder:        "l.timestamp DESC",
		Override:            bridgeProjectOverride(builder, state, "l.project_id"),
		CaseInsensitiveLike: true,
	})
	if err != nil {
		return "", nil, 0, err
	}
	return discovershared.AssembleSQL(base, clauses, plan), builder.Args, plan.Limit, nil
}

func bridgeLogsFieldExpr(field string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "event.id":
		return "l.event_id", true
	case "project":
		return "l.project_id", true
	case "title":
		return "COALESCE(e.title, l.message, '')", true
	case "message":
		return "COALESCE(l.message, '')", true
	case "level":
		return "COALESCE(l.level, '')", true
	case "platform":
		return "COALESCE(l.platform, '')", true
	case "culprit":
		return "COALESCE(e.culprit, '')", true
	case "environment":
		return "COALESCE(l.environment, '')", true
	case "release":
		return "COALESCE(l.release, '')", true
	case "logger":
		return "COALESCE(l.logger, '')", true
	case "trace", "trace.id":
		return "COALESCE(l.trace_id, '')", true
	case "span", "span.id":
		return "COALESCE(l.span_id, '')", true
	case "timestamp":
		return "l.timestamp", true
	case "count":
		return "1", true
	default:
		return "", false
	}
}

func scanBridgeDiscoverLogRows(rows *sql.Rows, projectSlugByID map[string]string) ([]discover.TableRow, error) {
	var items []discover.TableRow
	for rows.Next() {
		var (
			eventID     string
			projectID   string
			title       string
			message     string
			level       string
			platform    string
			culprit     string
			environment string
			release     string
			logger      string
			traceID     string
			spanID      string
			timestamp   time.Time
		)
		if err := rows.Scan(&eventID, &projectID, &title, &message, &level, &platform, &culprit, &environment, &release, &logger, &traceID, &spanID, &timestamp); err != nil {
			return nil, fmt.Errorf("scan bridge discover log row: %w", err)
		}
		items = append(items, discovershared.LogRow(store.DiscoverLog{
			EventID:     eventID,
			ProjectID:   projectID,
			ProjectSlug: projectSlugByID[projectID],
			Title:       title,
			Message:     message,
			Level:       level,
			Platform:    platform,
			Culprit:     culprit,
			Environment: environment,
			Release:     release,
			Logger:      logger,
			TraceID:     traceID,
			SpanID:      spanID,
			Timestamp:   timestamp.UTC(),
		}))
	}
	return items, rows.Err()
}
