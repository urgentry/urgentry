package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
)

func seedDiscoverEngineTestData(t testing.TB, db *sql.DB) {
	t.Helper()

	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES
		('proj-a', 'org-1', 'frontend', 'Frontend', 'javascript', 'active'),
		('proj-b', 'org-1', 'backend', 'Backend', 'go', 'active')`); err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id, priority, assignee) VALUES
		('grp-a', 'proj-a', 'urgentry-v1', 'grp-a', 'ImportError', 'app/main.go', 'error', 'unresolved', ?, ?, 5, 101, 1, 'alice'),
		('grp-b', 'proj-b', 'urgentry-v1', 'grp-b', 'TypeError', 'worker.go', 'error', 'resolved', ?, ?, 2, 102, 2, '')`,
		now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339),
		now.Add(-4*time.Hour).Format(time.RFC3339), now.Add(-3*time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events
		(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, payload_json, tags_json, occurred_at, ingested_at)
		VALUES
		('evt-log-0', 'proj-a', 'evt-log-0', NULL, 'frontend@1.0.0', 'production', 'javascript', 'info', 'log', 'worker warmup', 'worker warmup', 'log.go', '{"logger":"web"}', '{}', ?, ?),
		('evt-log-1', 'proj-a', 'evt-log-1', NULL, 'frontend@1.0.0', 'production', 'javascript', 'info', 'log', 'worker started', 'worker started', 'log.go', '{"logger":"web"}', '{}', ?, ?),
		('evt-log-2', 'proj-b', 'evt-log-2', NULL, 'backend@1.0.0', 'production', 'go', 'warning', 'log', 'worker slow', 'worker slow', 'log.go', '{"logger":"api"}', '{}', ?, ?),
		('evt-err-1', 'proj-a', 'evt-err-1', 'grp-a', 'frontend@1.0.0', 'production', 'javascript', 'error', 'error', 'ImportError', 'ImportError', 'app/main.go', '{"contexts":{"trace":{"trace_id":"trace-1","span_id":"span-1"}}}', '{}', ?, ?),
		('evt-err-2', 'proj-b', 'evt-err-2', 'grp-b', 'backend@1.0.0', 'staging', 'go', 'error', 'error', 'TypeError', 'TypeError', 'worker.go', '{"contexts":{"trace":{"trace_id":"trace-2","span_id":"span-2"}}}', '{}', ?, ?)
	`,
		now.Add(-110*time.Minute).Format(time.RFC3339), now.Add(-110*time.Minute).Format(time.RFC3339),
		now.Add(-55*time.Minute).Format(time.RFC3339), now.Add(-55*time.Minute).Format(time.RFC3339),
		now.Add(-15*time.Minute).Format(time.RFC3339), now.Add(-15*time.Minute).Format(time.RFC3339),
		now.Add(-45*time.Minute).Format(time.RFC3339), now.Add(-45*time.Minute).Format(time.RFC3339),
		now.Add(-175*time.Minute).Format(time.RFC3339), now.Add(-175*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO transactions
		(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, measurements_json, tags_json, created_at)
		VALUES
		('txn-1', 'proj-a', 'txn-evt-1', 'trace-1', 'span-a', '', 'checkout', 'http.server', 'ok', 'javascript', 'production', 'frontend@1.0.0', ?, ?, 120, '{}', '{}', ?),
		('txn-2', 'proj-b', 'txn-evt-2', 'trace-2', 'span-b', '', 'checkout', 'http.server', 'ok', 'go', 'production', 'backend@1.0.0', ?, ?, 240, '{}', '{}', ?),
		('txn-3', 'proj-b', 'txn-evt-3', 'trace-3', 'span-c', '', 'invoice', 'http.server', 'ok', 'go', 'production', 'backend@1.0.0', ?, ?, 80, '{}', '{}', ?)`,
		now.Add(-50*time.Minute).Format(time.RFC3339), now.Add(-49*time.Minute).Format(time.RFC3339), now.Add(-49*time.Minute).Format(time.RFC3339),
		now.Add(-20*time.Minute).Format(time.RFC3339), now.Add(-19*time.Minute).Format(time.RFC3339), now.Add(-19*time.Minute).Format(time.RFC3339),
		now.Add(-10*time.Minute).Format(time.RFC3339), now.Add(-9*time.Minute).Format(time.RFC3339), now.Add(-9*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed transactions: %v", err)
	}
}

func TestDiscoverEngineExecuteTableAggregateTransactions(t *testing.T) {
	db := openStoreTestDB(t)
	seedDiscoverEngineTestData(t, db)
	engine := NewDiscoverEngine(db)

	result, err := engine.ExecuteTable(context.Background(), discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a", "proj-b"},
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "p95", Expr: discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "p95"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ExecuteTable: %v", err)
	}
	t.Logf("rows=%+v", result.Rows)
	if len(result.Rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(result.Rows))
	}
	if result.Rows[0]["transaction"] != "checkout" {
		t.Fatalf("first row = %+v, want checkout first", result.Rows[0])
	}
	if p95 := result.Rows[0]["p95"].(float64); p95 != 240 {
		t.Fatalf("checkout p95 = %v, want 240", p95)
	}
}

func TestDiscoverEngineExecuteSeriesLogs(t *testing.T) {
	db := openStoreTestDB(t)
	seedDiscoverEngineTestData(t, db)
	engine := NewDiscoverEngine(db)

	result, err := engine.ExecuteSeries(context.Background(), discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T11:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Rollup: &discover.Rollup{Interval: "1h"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ExecuteSeries: %v", err)
	}
	if len(result.Points) != 1 {
		t.Fatalf("point count = %d, want 1", len(result.Points))
	}
}

func TestDiscoverEngineExecuteTableSortsRawLogs(t *testing.T) {
	db := openStoreTestDB(t)
	seedDiscoverEngineTestData(t, db)
	engine := NewDiscoverEngine(db)

	result, err := engine.ExecuteTable(context.Background(), discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "logger", Expr: discover.Expression{Field: "logger"}},
			{Alias: "message", Expr: discover.Expression{Field: "message"}},
			{Alias: "timestamp", Expr: discover.Expression{Field: "timestamp"}},
		},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "logger"}, Direction: "asc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ExecuteTable: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(result.Rows))
	}
	if result.Columns[0].Name != "logger" || result.Columns[1].Name != "message" {
		t.Fatalf("unexpected columns: %+v", result.Columns)
	}
	if result.Rows[0]["logger"] != "api" {
		t.Fatalf("first row = %+v, want api logger first", result.Rows[0])
	}
}

func TestDiscoverEngineExecuteTableGroupsIssuesByRelease(t *testing.T) {
	db := openStoreTestDB(t)
	seedDiscoverEngineTestData(t, db)
	engine := NewDiscoverEngine(db)

	result, err := engine.ExecuteTable(context.Background(), discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetIssues,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "release", Expr: discover.Expression{Field: "release"}},
			{Alias: "environment", Expr: discover.Expression{Field: "environment"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "release"}, {Field: "environment"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "count"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ExecuteTable: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(result.Rows))
	}
	if result.Rows[0]["release"] != "frontend@1.0.0" || result.Rows[0]["environment"] != "production" {
		t.Fatalf("first row = %+v, want seeded release/environment first", result.Rows[0])
	}
	if result.Rows[0]["count"].(int64) != 1 {
		t.Fatalf("first count = %v, want 1", result.Rows[0]["count"])
	}
}

func TestDiscoverEngineExplain(t *testing.T) {
	db := openStoreTestDB(t)
	seedDiscoverEngineTestData(t, db)
	engine := NewDiscoverEngine(db)

	plan, err := engine.Explain(discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a", "proj-b"},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 25,
	})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !strings.Contains(plan.SQL, "FROM transactions t") {
		t.Fatalf("unexpected SQL: %s", plan.SQL)
	}
	if plan.ResultLimit < 25 {
		t.Fatalf("result limit = %d, want at least 25", plan.ResultLimit)
	}
}

func TestDiscoverEngineHarnessCorpus(t *testing.T) {
	cases, err := discoverharness.LoadCases()
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	for _, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			db := openStoreTestDB(t)
			seedDiscoverEngineTestData(t, db)
			engine := NewDiscoverEngine(db)
			if err := discoverharness.RunCase(context.Background(), engine, item); err != nil {
				t.Fatal(err)
			}
		})
	}
}
