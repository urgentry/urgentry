package telemetryquery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

// --- No projection ever ran ---

func TestBridgeDiscoverLogsFailWithoutProjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	// No projection run -- bridge tables are empty.
	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	_, err := service.ExecuteTable(ctx, discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "message", Expr: discover.Expression{Field: "message"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err == nil {
		t.Fatal("expected discover logs query to fail when no projection has ever run")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
	if stale.Surface != QuerySurfaceDiscoverLogs {
		t.Fatalf("stale surface = %q, want discover_logs", stale.Surface)
	}
}

func TestBridgeDiscoverTransactionsFailWithoutProjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	_, err := service.ExecuteTable(ctx, discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a", "proj-b"},
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err == nil {
		t.Fatal("expected discover transactions query to fail when no projection has ever run")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
	if stale.Surface != QuerySurfaceDiscoverTransactions {
		t.Fatalf("stale surface = %q, want discover_transactions", stale.Surface)
	}
}

func TestBridgeDiscoverSeriesFailWithoutProjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	_, err := service.ExecuteSeries(ctx, discover.Query{
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
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Rollup: &discover.Rollup{Interval: "1h"},
		Limit:  10,
	})
	if err == nil {
		t.Fatal("expected discover series query to fail when no projection has ever run")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
}

// --- Stale projection: cursor exists but lag exceeds fail-closed budget ---

func TestBridgeDiscoverLogsStaleProjectionExceedsBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	// Verify the query works when projection is fresh.
	logsQuery := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "message", Expr: discover.Expression{Field: "message"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	}
	result, err := service.ExecuteTable(ctx, logsQuery)
	if err != nil {
		t.Fatalf("ExecuteTable (fresh): %v", err)
	}
	if len(result.Rows) == 0 {
		t.Fatal("ExecuteTable (fresh) returned no rows")
	}

	// Age the cursor past the fail-closed budget (600s for discover_logs).
	now := time.Now().UTC()
	if _, err := bridge.Exec(
		`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE cursor_family = 'logs'`,
		now.Add(-11*time.Minute),
	); err != nil {
		t.Fatalf("age cursor: %v", err)
	}

	// Query should now fail with BridgeStaleError.
	_, err = service.ExecuteTable(ctx, logsQuery)
	if err == nil {
		t.Fatal("expected discover logs query to fail after cursor aged past fail-closed budget")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
	if stale.Surface != QuerySurfaceDiscoverLogs {
		t.Fatalf("stale surface = %q, want discover_logs", stale.Surface)
	}
}

func TestBridgeDiscoverTransactionsStaleProjectionExceedsBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	txnQuery := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a", "proj-b"},
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	}

	// Fresh projection should succeed.
	result, err := service.ExecuteTable(ctx, txnQuery)
	if err != nil {
		t.Fatalf("ExecuteTable (fresh): %v", err)
	}
	if len(result.Rows) == 0 {
		t.Fatal("ExecuteTable (fresh) returned no rows")
	}

	// Age cursor past fail-closed budget.
	now := time.Now().UTC()
	if _, err := bridge.Exec(
		`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE cursor_family = 'transactions'`,
		now.Add(-11*time.Minute),
	); err != nil {
		t.Fatalf("age cursor: %v", err)
	}

	_, err = service.ExecuteTable(ctx, txnQuery)
	if err == nil {
		t.Fatal("expected discover transactions query to fail after cursor aged past fail-closed budget")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
	if stale.Surface != QuerySurfaceDiscoverTransactions {
		t.Fatalf("stale surface = %q, want discover_transactions", stale.Surface)
	}
}

func TestBridgeDiscoverSeriesStaleProjectionExceedsBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	seriesQuery := discover.Query{
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
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Rollup: &discover.Rollup{Interval: "1h"},
		Limit:  10,
	}

	// Fresh: should succeed.
	result, err := service.ExecuteSeries(ctx, seriesQuery)
	if err != nil {
		t.Fatalf("ExecuteSeries (fresh): %v", err)
	}
	if len(result.Points) == 0 {
		t.Fatal("ExecuteSeries (fresh) returned no points")
	}

	// Age cursor.
	now := time.Now().UTC()
	if _, err := bridge.Exec(
		`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE cursor_family = 'logs'`,
		now.Add(-11*time.Minute),
	); err != nil {
		t.Fatalf("age cursor: %v", err)
	}

	_, err = service.ExecuteSeries(ctx, seriesQuery)
	if err == nil {
		t.Fatal("expected discover series query to fail after cursor aged past fail-closed budget")
	}
	var stale *BridgeStaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected BridgeStaleError, got %T: %v", err, err)
	}
}

// --- Short staleness is tolerated under serve-stale mode ---

func TestBridgeDiscoverLogsServeStaleToleratesShortLag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	// Insert a new source row after projection (creates pending state).
	now := time.Now().UTC()
	if _, err := source.Exec(`INSERT INTO events
		(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, payload_json, tags_json, occurred_at, ingested_at)
		VALUES ('evt-log-late', 'proj-a', 'evt-log-late', NULL, 'frontend@1.0.0', 'production', 'javascript', 'info', 'log', 'late arrival', 'late arrival', 'log.go', '{"logger":"web"}', '{}', ?, ?)`,
		now.Add(-30*time.Second).Format(time.RFC3339),
		now.Add(-30*time.Second).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed late log: %v", err)
	}

	// Cursor is slightly stale but within budget -- discover_logs uses
	// serve_stale mode with 120s stale budget and 600s fail-closed.
	logsQuery := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "message", Expr: discover.Expression{Field: "message"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	}
	result, err := service.ExecuteTable(ctx, logsQuery)
	if err != nil {
		t.Fatalf("ExecuteTable with short lag should succeed under serve-stale: %v", err)
	}
	// Should still return the projected rows (the late arrival is NOT projected).
	if len(result.Rows) == 0 {
		t.Fatal("ExecuteTable returned no rows despite valid projection data")
	}
}

// --- Harness corpus cases against bridge with project-scoped projection ---

func TestBridgeDiscoverHarnessProjectScopeTransactions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	// Sync only for proj-a at project scope, not org scope.
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-a"}, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	// Project-scoped discover query for proj-a's transactions.
	result, err := service.ExecuteTable(ctx, discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:      discover.ScopeKindProject,
			ProjectID: "proj-a",
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "count"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ExecuteTable project scope: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 transaction group for proj-a, got %d: %+v", len(result.Rows), result.Rows)
	}
	if result.Rows[0]["transaction"] != "checkout" {
		t.Fatalf("expected checkout, got %v", result.Rows[0]["transaction"])
	}
}

// --- Explain routes through bridge SQL for logs and transactions ---

func TestBridgeDiscoverExplainReturnsPostgresSQL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	logsPlan, err := service.Explain(discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetLogs,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "message", Expr: discover.Expression{Field: "message"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Explain logs: %v", err)
	}
	if logsPlan.Dataset != discover.DatasetLogs {
		t.Fatalf("plan dataset = %q, want logs", logsPlan.Dataset)
	}
	if !strings.Contains(logsPlan.SQL, "telemetry.log_facts") {
		t.Fatalf("explain SQL missing telemetry.log_facts: %s", logsPlan.SQL)
	}

	txnPlan, err := service.Explain(discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a"},
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Explain transactions: %v", err)
	}
	if txnPlan.Dataset != discover.DatasetTransactions {
		t.Fatalf("plan dataset = %q, want transactions", txnPlan.Dataset)
	}
	if !strings.Contains(txnPlan.SQL, "telemetry.transaction_facts") {
		t.Fatalf("explain SQL missing telemetry.transaction_facts: %s", txnPlan.SQL)
	}
}

// --- Harness corpus: run all log/transaction cases through bridge and verify snapshots match ---

func TestBridgeDiscoverHarnessCorpusAfterRecovery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)

	// Sync logs and transactions at org scope.
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	// Age the cursor slightly, but keep it within the serve-stale budget.
	now := time.Now().UTC()
	if _, err := bridge.Exec(
		`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE cursor_family IN ('logs', 'transactions')`,
		now.Add(-90*time.Second),
	); err != nil {
		t.Fatalf("age cursors: %v", err)
	}

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)
	cases, err := discoverharness.LoadCases()
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	for _, item := range cases {
		if item.Query.Dataset != discover.DatasetLogs && item.Query.Dataset != discover.DatasetTransactions {
			continue
		}
		item := item
		// Override explain expectations for bridge SQL.
		switch item.Query.Dataset {
		case discover.DatasetLogs:
			item.ExplainContains = []string{"FROM telemetry.log_facts l"}
		case discover.DatasetTransactions:
			item.ExplainContains = []string{"FROM telemetry.transaction_facts t"}
		}
		t.Run(item.Name+"_stale_within_budget", func(t *testing.T) {
			if err := discoverharness.RunCase(ctx, service, item); err != nil {
				t.Fatal(err)
			}
		})
	}
}

