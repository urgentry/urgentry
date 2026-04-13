package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"urgentry/internal/discover"
	"urgentry/internal/discovershared"
)

type DiscoverEngine struct {
	db *sql.DB
}

func NewDiscoverEngine(db *sql.DB) *DiscoverEngine {
	return &DiscoverEngine{db: db}
}

func (e *DiscoverEngine) ExecuteTable(ctx context.Context, query discover.Query) (discover.TableResult, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.TableResult{}, err
	}
	plan, err := e.explain(query, "table")
	if err != nil {
		return discover.TableResult{}, err
	}
	rows, err := e.fetchRows(ctx, query, plan.ResultLimit)
	if err != nil {
		return discover.TableResult{}, err
	}
	return discovershared.BuildTableResult(query, cost, rows), nil
}

func (e *DiscoverEngine) ExecuteSeries(ctx context.Context, query discover.Query) (discover.SeriesResult, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.SeriesResult{}, err
	}
	plan, err := e.explain(query, "series")
	if err != nil {
		return discover.SeriesResult{}, err
	}
	rows, err := e.fetchRows(ctx, query, plan.ResultLimit)
	if err != nil {
		return discover.SeriesResult{}, err
	}
	return discovershared.BuildSeriesResult(query, cost, rows)
}

func (e *DiscoverEngine) Explain(query discover.Query) (discover.ExplainPlan, error) {
	query, _, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.ExplainPlan{}, err
	}
	return e.explain(query, discovershared.ExplainMode(query))
}

func (e *DiscoverEngine) explain(query discover.Query, mode string) (discover.ExplainPlan, error) {
	query, cost, err := discover.ValidateQuery(query)
	if err != nil {
		return discover.ExplainPlan{}, err
	}
	sqlText, args, limit, err := buildDiscoverFetchSQL(query, discovershared.ScanLimit(query))
	if err != nil {
		return discover.ExplainPlan{}, err
	}
	return discover.ExplainPlan{
		Dataset:     query.Dataset,
		Mode:        mode,
		SQL:         sqlText,
		Args:        args,
		ResultLimit: limit,
		Cost:        cost,
	}, nil
}

func (e *DiscoverEngine) fetchRows(ctx context.Context, query discover.Query, limit int) ([]discover.TableRow, error) {
	sqlText, args, _, err := buildDiscoverFetchSQL(query, limit)
	if err != nil {
		return nil, err
	}
	switch query.Dataset {
	case discover.DatasetIssues:
		rows, err := e.db.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items, err := scanDiscoverIssues(rows)
		if err != nil {
			return nil, err
		}
		return discovershared.IssueRows(items), nil
	case discover.DatasetLogs:
		rows, err := e.db.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items, err := scanDiscoverLogs(rows)
		if err != nil {
			return nil, err
		}
		return discovershared.LogRows(items), nil
	case discover.DatasetTransactions:
		rows, err := e.db.QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items, err := scanDiscoverTransactions(rows)
		if err != nil {
			return nil, err
		}
		return discovershared.TransactionRows(items), nil
	default:
		return nil, fmt.Errorf("unsupported discover dataset %q", query.Dataset)
	}
}

func buildDiscoverFetchSQL(query discover.Query, limit int) (string, []any, int, error) {
	limit = discovershared.DefaultFetchLimit(query.Limit, limit)
	switch query.Dataset {
	case discover.DatasetIssues:
		return buildIssuesFetchSQL(query, limit)
	case discover.DatasetLogs:
		return buildLogsFetchSQL(query, limit)
	case discover.DatasetTransactions:
		return buildTransactionsFetchSQL(query, limit)
	default:
		return "", nil, 0, fmt.Errorf("unsupported discover dataset %q", query.Dataset)
	}
}

func buildIssuesFetchSQL(query discover.Query, limit int) (string, []any, int, error) {
	builder := &discovershared.SQLiteArgBuilder{}
	base := `SELECT g.id, p.id, p.slug, COALESCE(p.name, p.slug), COALESCE(p.platform, ''),
		(SELECT COALESCE(MAX(e.release), '') FROM events e WHERE e.group_id = g.id),
		(SELECT COALESCE(MAX(e.environment), '') FROM events e WHERE e.group_id = g.id),
		g.title, g.culprit, g.level, g.status, g.first_seen, g.last_seen, g.times_seen,
		COALESCE(g.short_id, 0), COALESCE(g.priority, 2), COALESCE(g.assignee, '')
	FROM groups g
	JOIN projects p ON p.id = g.project_id
	JOIN organizations o ON o.id = p.organization_id`
	clauses := []string{"1=1"}
	applyScopeClauses(&clauses, builder, query)
	plan, err := discovershared.PlanFilter(query, limit, discovershared.FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     issueFieldExpr,
		TimestampExpr: "g.last_seen",
		DefaultOrder:  "g.last_seen DESC",
	})
	if err != nil {
		return "", nil, 0, err
	}
	return discovershared.AssembleSQL(base, clauses, plan), builder.Args, plan.Limit, nil
}

func buildLogsFetchSQL(query discover.Query, limit int) (string, []any, int, error) {
	builder := &discovershared.SQLiteArgBuilder{}
	base := `SELECT e.event_id, p.id, p.slug, COALESCE(e.title, ''), COALESCE(e.message, ''), COALESCE(e.level, ''),
		COALESCE(e.platform, ''), COALESCE(e.culprit, ''), COALESCE(e.environment, ''), COALESCE(e.release, ''),
		COALESCE(json_extract(e.payload_json, '$.logger'), ''),
		COALESCE(json_extract(e.payload_json, '$.contexts.trace.trace_id'), ''),
		COALESCE(json_extract(e.payload_json, '$.contexts.trace.span_id'), ''),
		COALESCE(e.occurred_at, e.ingested_at, ''), COALESCE(e.tags_json, '{}')
	FROM events e
	JOIN projects p ON p.id = e.project_id
	JOIN organizations o ON o.id = p.organization_id`
	clauses := []string{"LOWER(COALESCE(e.event_type, 'error')) = 'log'"}
	applyScopeClauses(&clauses, builder, query)
	plan, err := discovershared.PlanFilter(query, limit, discovershared.FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     logsFieldExpr,
		TimestampExpr: "COALESCE(e.occurred_at, e.ingested_at, '')",
		DefaultOrder:  "COALESCE(e.occurred_at, e.ingested_at, '') DESC",
	})
	if err != nil {
		return "", nil, 0, err
	}
	return discovershared.AssembleSQL(base, clauses, plan), builder.Args, plan.Limit, nil
}

func buildTransactionsFetchSQL(query discover.Query, limit int) (string, []any, int, error) {
	builder := &discovershared.SQLiteArgBuilder{}
	base := `SELECT t.event_id, p.id, p.slug, t.transaction_name, COALESCE(t.op, ''), COALESCE(t.status, ''),
		COALESCE(t.platform, ''), COALESCE(t.environment, ''), COALESCE(t.release, ''), t.trace_id, t.span_id,
		t.start_timestamp, t.end_timestamp, t.duration_ms, t.created_at
	FROM transactions t
	JOIN projects p ON p.id = t.project_id
	JOIN organizations o ON o.id = p.organization_id`
	clauses := []string{"1=1"}
	applyScopeClauses(&clauses, builder, query)
	plan, err := discovershared.PlanFilter(query, limit, discovershared.FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     transactionFieldExpr,
		TimestampExpr: "t.created_at",
		DefaultOrder:  "t.created_at DESC",
	})
	if err != nil {
		return "", nil, 0, err
	}
	return discovershared.AssembleSQL(base, clauses, plan), builder.Args, plan.Limit, nil
}

func applyScopeClauses(clauses *[]string, builder *discovershared.SQLiteArgBuilder, query discover.Query) {
	switch query.Scope.Kind {
	case discover.ScopeKindProject:
		*clauses = append(*clauses, "p.id = "+builder.Add(query.Scope.ProjectID))
	case discover.ScopeKindOrganization:
		*clauses = append(*clauses, "o.slug = "+builder.Add(query.Scope.Organization))
		if len(query.Scope.ProjectIDs) > 0 {
			*clauses = append(*clauses, "p.id IN ("+builder.AddAll(query.Scope.ProjectIDs)+")")
		}
	}
}
func issueFieldExpr(field string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "project":
		return "p.slug", true
	case "project.id":
		return "p.id", true
	case "issue.id":
		return "g.id", true
	case "issue.short_id":
		return "CAST(COALESCE(g.short_id, 0) AS TEXT)", true
	case "title":
		return "g.title", true
	case "culprit":
		return "g.culprit", true
	case "level":
		return "LOWER(COALESCE(g.level, ''))", true
	case "status":
		return "LOWER(COALESCE(g.status, ''))", true
	case "assignee":
		return "COALESCE(g.assignee, '')", true
	case "first_seen":
		return "g.first_seen", true
	case "last_seen", "timestamp":
		return "g.last_seen", true
	case "count":
		return "g.times_seen", true
	case "release":
		return `(SELECT COALESCE(MAX(e.release), '') FROM events e WHERE e.group_id = g.id)`, true
	case "environment":
		return `(SELECT COALESCE(MAX(e.environment), '') FROM events e WHERE e.group_id = g.id)`, true
	case "event.type":
		return `(SELECT LOWER(COALESCE(MAX(e.event_type), 'error')) FROM events e WHERE e.group_id = g.id)`, true
	default:
		return "", false
	}
}

func logsFieldExpr(field string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "project":
		return "p.slug", true
	case "project.id":
		return "p.id", true
	case "release":
		return "COALESCE(e.release, '')", true
	case "environment":
		return "COALESCE(e.environment, '')", true
	case "platform":
		return "COALESCE(e.platform, '')", true
	case "event.type":
		return "LOWER(COALESCE(e.event_type, 'error'))", true
	case "timestamp":
		return "COALESCE(e.occurred_at, e.ingested_at, '')", true
	case "event.id":
		return "e.event_id", true
	case "title":
		return "COALESCE(e.title, '')", true
	case "message":
		return "COALESCE(e.message, '')", true
	case "logger":
		return "COALESCE(json_extract(e.payload_json, '$.logger'), '')", true
	case "level":
		return "LOWER(COALESCE(e.level, ''))", true
	case "culprit":
		return "COALESCE(e.culprit, '')", true
	case "trace.id":
		return "COALESCE(json_extract(e.payload_json, '$.contexts.trace.trace_id'), '')", true
	case "span.id":
		return "COALESCE(json_extract(e.payload_json, '$.contexts.trace.span_id'), '')", true
	case "count":
		return "1", true
	default:
		return "", false
	}
}

func transactionFieldExpr(field string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "project":
		return "p.slug", true
	case "project.id":
		return "p.id", true
	case "release":
		return "COALESCE(t.release, '')", true
	case "environment":
		return "COALESCE(t.environment, '')", true
	case "platform":
		return "COALESCE(t.platform, '')", true
	case "event.type":
		return "'transaction'", true
	case "timestamp":
		return "t.created_at", true
	case "event.id":
		return "t.event_id", true
	case "transaction":
		return "t.transaction_name", true
	case "op":
		return "COALESCE(t.op, '')", true
	case "status":
		return "COALESCE(t.status, '')", true
	case "trace.id":
		return "COALESCE(t.trace_id, '')", true
	case "span.id":
		return "COALESCE(t.span_id, '')", true
	case "duration.ms":
		return "t.duration_ms", true
	case "count":
		return "1", true
	default:
		return "", false
	}
}
