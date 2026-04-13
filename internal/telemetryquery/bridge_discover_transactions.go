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

func buildBridgeTransactionsFetchSQL(query discover.Query, state bridgeDiscoverContext, limit int) (string, []any, int, error) {
	builder := &bridgeSQLBuilder{}
	base := `SELECT t.event_id, t.project_id, COALESCE(t.transaction_name, ''), COALESCE(t.op, ''), COALESCE(t.status, ''),
		COALESCE(t.environment, ''), COALESCE(t.release, ''), COALESCE(t.trace_id, ''), COALESCE(t.span_id, ''),
		t.started_at, t.duration_ms, COALESCE(e.platform, '')
	FROM telemetry.transaction_facts t
	LEFT JOIN telemetry.event_facts e ON e.project_id = t.project_id AND e.event_id = t.event_id`
	clauses := []string{`t.organization_id = ` + builder.Add(state.organizationID)}
	applyBridgeScopeClauses(query, builder, "t.project_id", &clauses)
	plan, err := discovershared.PlanFilter(query, limit, discovershared.FilterPlanConfig{
		Builder:             builder,
		FieldExpr:           bridgeTransactionFieldExpr,
		TimestampExpr:       "t.started_at",
		DefaultOrder:        "t.started_at DESC",
		Override:            bridgeProjectOverride(builder, state, "t.project_id"),
		CaseInsensitiveLike: true,
	})
	if err != nil {
		return "", nil, 0, err
	}
	return discovershared.AssembleSQL(base, clauses, plan), builder.Args, plan.Limit, nil
}

func bridgeTransactionFieldExpr(field string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "event.id":
		return "t.event_id", true
	case "project":
		return "t.project_id", true
	case "transaction":
		return "COALESCE(t.transaction_name, '')", true
	case "op":
		return "COALESCE(t.op, '')", true
	case "status":
		return "COALESCE(t.status, '')", true
	case "environment":
		return "COALESCE(t.environment, '')", true
	case "release":
		return "COALESCE(t.release, '')", true
	case "trace", "trace.id":
		return "COALESCE(t.trace_id, '')", true
	case "span", "span.id":
		return "COALESCE(t.span_id, '')", true
	case "timestamp", "started_at":
		return "t.started_at", true
	case "duration", "duration.ms":
		return "t.duration_ms", true
	case "platform":
		return "COALESCE(e.platform, '')", true
	case "count":
		return "1", true
	default:
		return "", false
	}
}

func scanBridgeDiscoverTransactionRows(rows *sql.Rows, projectSlugByID map[string]string) ([]discover.TableRow, error) {
	var items []discover.TableRow
	for rows.Next() {
		var (
			eventID     string
			projectID   string
			transaction string
			op          string
			status      string
			environment string
			release     string
			traceID     string
			spanID      string
			timestamp   time.Time
			durationMS  float64
			platform    string
		)
		if err := rows.Scan(&eventID, &projectID, &transaction, &op, &status, &environment, &release, &traceID, &spanID, &timestamp, &durationMS, &platform); err != nil {
			return nil, fmt.Errorf("scan bridge discover transaction row: %w", err)
		}
		items = append(items, discovershared.TransactionRow(store.DiscoverTransaction{
			EventID:     eventID,
			ProjectID:   projectID,
			ProjectSlug: projectSlugByID[projectID],
			Transaction: transaction,
			Op:          op,
			Status:      status,
			Platform:    platform,
			Environment: environment,
			Release:     release,
			TraceID:     traceID,
			SpanID:      spanID,
			Timestamp:   timestamp.UTC(),
			DurationMS:  durationMS,
		}))
	}
	return items, rows.Err()
}
