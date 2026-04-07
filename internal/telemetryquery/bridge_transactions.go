package telemetryquery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/discovershared"
	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func (s *bridgeService) ListRecentTransactions(ctx context.Context, orgSlug string, limit int) ([]store.DiscoverTransaction, error) {
	return s.listTransactionsByOrg(ctx, orgSlug, "", limit)
}

func (s *bridgeService) SearchTransactions(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error) {
	return s.listTransactionsByOrg(ctx, orgSlug, rawQuery, limit)
}

func (s *bridgeService) ListTransactions(ctx context.Context, projectID string, limit int) ([]*store.StoredTransaction, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceTraces, scope, telemetrybridge.FamilyTransactions); err != nil {
		return nil, err
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name,
		       COALESCE(op, ''), COALESCE(status, ''), '', COALESCE(environment, ''), COALESCE(release, ''),
		       started_at, finished_at, duration_ms, COALESCE(tags_json::text, '{}'), COALESCE(measurements_json::text, '{}')
		  FROM telemetry.transaction_facts
		 WHERE project_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`,
		projectID, discovershared.Clamp(limit, 1, 100),
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge transactions: %w", err)
	}
	defer rows.Close()
	return scanBridgeTransactions(rows)
}

func (s *bridgeService) ListTransactionsByTrace(ctx context.Context, projectID, traceID string) ([]*store.StoredTransaction, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceTraces, scope, telemetrybridge.FamilyTransactions); err != nil {
		return nil, err
	}
	if items, ok := s.cachedTraceTransactions(projectID, traceID); ok {
		return items, nil
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT id, project_id, event_id, trace_id, span_id, COALESCE(parent_span_id, ''), transaction_name,
		       COALESCE(op, ''), COALESCE(status, ''), '', COALESCE(environment, ''), COALESCE(release, ''),
		       started_at, finished_at, duration_ms, COALESCE(tags_json::text, '{}'), COALESCE(measurements_json::text, '{}')
		  FROM telemetry.transaction_facts
		 WHERE project_id = $1 AND trace_id = $2
		 ORDER BY started_at ASC, span_id ASC`,
		projectID, traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge trace transactions: %w", err)
	}
	defer rows.Close()
	items, err := scanBridgeTransactions(rows)
	if err != nil {
		return nil, err
	}
	s.storeTraceTransactions(projectID, traceID, items)
	return items, nil
}

func (s *bridgeService) ListTraceSpans(ctx context.Context, projectID, traceID string) ([]store.StoredSpan, error) {
	scope, err := s.projectScope(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceTraces, scope, telemetrybridge.FamilySpans); err != nil {
		return nil, err
	}
	if items, ok := s.cachedTraceSpans(projectID, traceID); ok {
		return items, nil
	}
	rows, err := s.bridgeDB.QueryContext(ctx, `
		SELECT id, project_id, transaction_event_id, trace_id, span_id, COALESCE(parent_span_id, ''),
		       COALESCE(op, ''), COALESCE(description, ''), COALESCE(status, ''), started_at, finished_at,
		       duration_ms, COALESCE(tags_json::text, '{}'), COALESCE(data_json::text, '{}')
		  FROM telemetry.span_facts
		 WHERE project_id = $1 AND trace_id = $2
		 ORDER BY started_at ASC, span_id ASC`,
		projectID, traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list bridge trace spans: %w", err)
	}
	defer rows.Close()
	items, err := scanBridgeSpans(rows)
	if err != nil {
		return nil, err
	}
	s.storeTraceSpans(projectID, traceID, items)
	return items, nil
}

func (s *bridgeService) listTransactionsByOrg(ctx context.Context, orgSlug, rawQuery string, limit int) ([]store.DiscoverTransaction, error) {
	scope, err := s.orgScope(ctx, orgSlug)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSurfaceFresh(ctx, QuerySurfaceDiscoverTransactions, scope, telemetrybridge.FamilyTransactions); err != nil {
		return nil, err
	}
	parsed := sqlite.ParseIssueSearch(rawQuery)
	query := `
		SELECT event_id, project_id, trace_id, span_id, transaction_name, COALESCE(op, ''), COALESCE(status, ''),
		       COALESCE(environment, ''), COALESCE(release, ''), started_at, finished_at, duration_ms
		  FROM telemetry.transaction_facts
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
	for _, term := range parsed.Terms {
		query += fmt.Sprintf(` AND (transaction_name ILIKE $%d OR op ILIKE $%d OR status ILIKE $%d OR trace_id ILIKE $%d)`, next, next, next, next)
		args = append(args, "%"+term+"%")
		next++
	}
	query += fmt.Sprintf(` ORDER BY started_at DESC LIMIT $%d`, next)
	args = append(args, discovershared.Clamp(limit, 1, 100))
	rows, err := s.bridgeDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list bridge org transactions: %w", err)
	}
	defer rows.Close()
	items := make([]store.DiscoverTransaction, 0, limit)
	projectSlugs, err := s.projectSlugMap(ctx)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item store.DiscoverTransaction
		if err := rows.Scan(&item.EventID, &item.ProjectID, &item.TraceID, &item.SpanID, &item.Transaction, &item.Op, &item.Status, &item.Environment, &item.Release, &item.Timestamp, &item.EndTimestamp, &item.DurationMS); err != nil {
			return nil, fmt.Errorf("scan bridge transaction row: %w", err)
		}
		item.ProjectSlug = projectSlugs[item.ProjectID]
		item.StartTimestamp = item.Timestamp
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanBridgeTransactions(rows *sql.Rows) ([]*store.StoredTransaction, error) {
	var items []*store.StoredTransaction
	for rows.Next() {
		var (
			item             store.StoredTransaction
			parentSpanID     string
			platform         string
			environment      string
			releaseID        string
			tagsJSON         string
			measurementsJSON string
			startedAt        time.Time
			finishedAt       time.Time
		)
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.EventID, &item.TraceID, &item.SpanID, &parentSpanID, &item.Transaction, &item.Op, &item.Status, &platform, &environment, &releaseID, &startedAt, &finishedAt, &item.DurationMS, &tagsJSON, &measurementsJSON); err != nil {
			return nil, fmt.Errorf("scan bridge transaction: %w", err)
		}
		item.ParentSpanID = parentSpanID
		item.Platform = platform
		item.Environment = environment
		item.ReleaseID = releaseID
		item.StartTimestamp = startedAt.UTC()
		item.EndTimestamp = finishedAt.UTC()
		item.Tags = sqlutil.ParseTags(tagsJSON)
		_ = json.Unmarshal([]byte(measurementsJSON), &item.Measurements)
		items = append(items, &item)
	}
	return items, rows.Err()
}

func scanBridgeSpans(rows *sql.Rows) ([]store.StoredSpan, error) {
	var items []store.StoredSpan
	for rows.Next() {
		var (
			item       store.StoredSpan
			parentSpan string
			tagsJSON   string
			dataJSON   string
			startedAt  time.Time
			finishedAt time.Time
		)
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.TransactionEventID, &item.TraceID, &item.SpanID, &parentSpan, &item.Op, &item.Description, &item.Status, &startedAt, &finishedAt, &item.DurationMS, &tagsJSON, &dataJSON); err != nil {
			return nil, fmt.Errorf("scan bridge span: %w", err)
		}
		item.ParentSpanID = parentSpan
		item.StartTimestamp = startedAt.UTC()
		item.EndTimestamp = finishedAt.UTC()
		_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
		_ = json.Unmarshal([]byte(dataJSON), &item.Data)
		items = append(items, item)
	}
	return items, rows.Err()
}
