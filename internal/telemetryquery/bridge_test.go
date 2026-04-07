package telemetryquery

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
	profilefixtures "urgentry/internal/testfixtures/profiles"
	"urgentry/internal/testpostgres"
)

var bridgeQueryPostgres = testpostgres.NewProvider("urgentry-bridgequery")
var migratedBridgeQueryPostgres = bridgeQueryPostgres.NewTemplate("urgentry-bridgequery-migrated", func(db *sql.DB) error {
	return telemetrybridge.Migrate(context.Background(), db, telemetrybridge.BackendPostgres)
})

func newBridgeTestService(source, bridge *sql.DB, blobs store.BlobStore, issueSearch IssueSearchStore) Service {
	webStore := sqlite.NewWebStore(source)
	if issueSearch == nil {
		issueSearch = webStore
	}
	return NewService(source, bridge, Dependencies{
		Blobs:       blobs,
		IssueSearch: issueSearch,
		Web:         webStore,
		Discover:    sqlite.NewDiscoverEngine(source),
		Traces:      sqlite.NewTraceStore(source),
		Replays:     sqlite.NewReplayStore(source, blobs),
		Profiles:    telemetrybridge.NewProfileReadStore(bridge, blobs),
		Projector:   telemetrybridge.NewProjector(source, bridge),
	})
}

func TestBridgeTransactionsRequireExplicitProjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)

	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)
	if _, err := service.ListTransactions(ctx, "proj-a", 10); err == nil {
		t.Fatal("expected transactions query to fail before projection runs")
	} else {
		var stale *BridgeStaleError
		if !errors.As(err, &stale) {
			t.Fatalf("expected BridgeStaleError, got %v", err)
		}
	}

	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-a"}, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	items, err := service.ListTransactions(ctx, "proj-a", 10)
	if err != nil {
		t.Fatalf("ListTransactions after projection: %v", err)
	}
	if len(items) != 1 || items[0].EventID != "txn-evt-1" {
		t.Fatalf("unexpected projected transactions: %+v", items)
	}
}

func TestBridgeTransactionsServeShortStalenessButFailWhenLagAgesOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)

	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-a"}, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}
	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	now := time.Now().UTC()
	if _, err := source.Exec(`INSERT INTO transactions
		(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, measurements_json, tags_json, created_at)
		VALUES ('txn-4', 'proj-a', 'txn-evt-4', 'trace-4', 'span-d', '', 'cart', 'http.server', 'ok', 'javascript', 'production', 'frontend@1.0.1', ?, ?, 64, '{}', '{}', ?)`,
		now.Add(-time.Minute).Format(time.RFC3339),
		now.Add(-30*time.Second).Format(time.RFC3339),
		now.Add(-30*time.Second).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed stale transaction: %v", err)
	}

	items, err := service.ListTransactions(ctx, "proj-a", 10)
	if err != nil {
		t.Fatalf("ListTransactions with short lag: %v", err)
	}
	if len(items) != 1 || items[0].EventID != "txn-evt-1" {
		t.Fatalf("unexpected projected transactions while stale: %+v", items)
	}

	if _, err := bridge.Exec(`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE cursor_family = 'transactions' AND scope_kind = 'project' AND scope_id = 'proj-a'`, now.Add(-11*time.Minute)); err != nil {
		t.Fatalf("age cursor: %v", err)
	}

	if _, err := service.ListTransactions(ctx, "proj-a", 10); err == nil {
		t.Fatal("expected transactions query to fail once lag exceeded the fail-closed budget")
	} else {
		var stale *BridgeStaleError
		if !errors.As(err, &stale) {
			t.Fatalf("expected BridgeStaleError, got %v", err)
		}
	}
}

func TestBridgeListOrgReplays(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	blobs := store.NewMemoryBlobStore()
	seedBridgeBenchReplay(t, source, blobs)
	replays := sqlite.NewReplayStore(source, blobs)
	if _, err := replays.SaveEnvelopeReplay(ctx, "proj-b", "evt-bridge-org-replay", []byte(`{"event_id":"evt-bridge-org-replay","replay_id":"bridge-org-replay","timestamp":"2026-03-29T12:10:00Z"}`)); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := replays.IndexReplay(ctx, "proj-b", "bridge-org-replay"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyReplays); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, blobs, nil)
	items, err := service.ListOrgReplays(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("ListOrgReplays: %v", err)
	}
	if len(items) != 9 {
		t.Fatalf("len(items) = %d, want 9", len(items))
	}
	if items[0].ReplayID != "bridge-org-replay" || items[0].ProjectID != "proj-b" {
		t.Fatalf("first org replay = %+v, want bridge-org-replay/proj-b", items[0])
	}
	if items[1].ProjectID != "proj-a" {
		t.Fatalf("second org replay = %+v, want proj-a replay", items[1])
	}
}

func TestBridgeGetReplayAndFilterTimelineByPane(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	blobs := store.NewMemoryBlobStore()
	seedBridgeBenchReplay(t, source, blobs)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-a"}, telemetrybridge.FamilyReplays, telemetrybridge.FamilyReplayTimeline); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, blobs, nil)
	replay, err := service.GetReplay(ctx, "proj-a", "bench-replay-00")
	if err != nil {
		t.Fatalf("GetReplay: %v", err)
	}
	if replay.Manifest.ReplayID != "bench-replay-00" || len(replay.Timeline) == 0 {
		t.Fatalf("unexpected replay: %+v", replay)
	}

	timeline, err := service.ListReplayTimeline(ctx, "proj-a", "bench-replay-00", store.ReplayTimelineFilter{Pane: "errors", Limit: 20})
	if err != nil {
		t.Fatalf("ListReplayTimeline: %v", err)
	}
	if len(timeline) == 0 {
		t.Fatal("expected pane-filtered replay timeline rows")
	}
	for _, item := range timeline {
		if item.Pane != "errors" {
			t.Fatalf("timeline pane = %q, want errors", item.Pane)
		}
		if item.Kind != "error" {
			t.Fatalf("timeline kind = %q, want error", item.Kind)
		}
	}
}

func TestBridgeDiscoverHarnessLogsAndTransactions(t *testing.T) {
	t.Parallel()

	source := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, source)

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(context.Background(), telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs, telemetrybridge.FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
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
		switch item.Query.Dataset {
		case discover.DatasetLogs:
			item.ExplainContains = []string{"FROM telemetry.log_facts l"}
		case discover.DatasetTransactions:
			item.ExplainContains = []string{"FROM telemetry.transaction_facts t"}
		}
		t.Run(item.Name, func(t *testing.T) {
			if err := discoverharness.RunCase(context.Background(), service, item); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBridgeDiscoverExecuteTableSortsRawLogs(t *testing.T) {
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
	result, err := service.ExecuteTable(ctx, discover.Query{
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
	if result.Rows[0]["logger"] != "api" {
		t.Fatalf("first row = %+v, want api logger first", result.Rows[0])
	}
}

func TestBridgeProfilesReadAndQueryViews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	seedBridgeProfileSource(t, source)

	blobs := store.NewMemoryBlobStore()
	profiles := sqlite.NewProfileStore(source, blobs)
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.IOHeavy().Spec().WithIDs("evt-profile-query-a", "profile-query-a"))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.CPUHeavy().Spec().WithIDs("evt-profile-query-b", "profile-query-b"))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.DBHeavy().Spec().WithIDs("evt-profile-link-a", "profile-link-a").WithDuration(42000000))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.SaveRead().Spec().
		WithIDs("evt-profile-link-c", "profile-link-c").
		WithTransaction("GET /payments").
		WithTrace("trace-other").
		WithRelease("backend@2.0.0").
		WithDuration(51000000).
		WithBody([]byte(`{"frames":[{"function":"payments","filename":"payments.go","lineno":9}],"samples":[{"frames":[0],"weight":5}]}`)))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.MalformedEmpty().Spec().WithIDs("evt-profile-invalid", "profile-invalid").WithTransaction("broken"))

	if _, err := source.Exec(`UPDATE events SET payload_json = '' WHERE project_id = 'proj-1' AND event_id = 'evt-profile-query-a'`); err != nil {
		t.Fatalf("clear source profile payload_json: %v", err)
	}

	bridge := openMigratedBridgeQueryTestDatabase(t)
	projector := telemetrybridge.NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-1"}, telemetrybridge.FamilyProfiles); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	service := newBridgeTestService(source, bridge, blobs, nil)

	items, err := service.ListProfiles(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(items) != 5 || items[0].FrameCount == 0 || items[0].FunctionCount == 0 {
		t.Fatalf("unexpected bridge profile manifests: %+v", items)
	}

	record, err := service.GetProfile(ctx, "proj-1", "profile-query-a")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if record.Manifest.ProfileID != "profile-query-a" || len(record.RawPayload) == 0 || len(record.TopFrames) == 0 || record.TopFrames[0].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected bridge profile detail: %+v", record)
	}

	byTrace, err := service.FindProfilesByTrace(ctx, "proj-1", "0123456789abcdef0123456789abcdef", 10)
	if err != nil {
		t.Fatalf("FindProfilesByTrace: %v", err)
	}
	foundTraceProfile := false
	for _, item := range byTrace {
		if item.ProfileID == "profile-link-a" && item.TopFunction == "dbQuery" {
			foundTraceProfile = true
		}
	}
	if !foundTraceProfile {
		t.Fatalf("missing expected bridge trace profile: %+v", byTrace)
	}

	highlights, err := service.ListReleaseProfileHighlights(ctx, "proj-1", "backend@1.2.3", 10)
	if err != nil {
		t.Fatalf("ListReleaseProfileHighlights: %v", err)
	}
	if len(highlights) < 1 || highlights[0].ProfileID != "profile-link-a" || highlights[0].TopFunction != "dbQuery" {
		t.Fatalf("unexpected bridge release highlights: %+v", highlights)
	}

	related, err := service.FindRelatedProfile(ctx, "proj-1", "trace-other", "GET /payments", "backend@2.0.0")
	if err != nil {
		t.Fatalf("FindRelatedProfile: %v", err)
	}
	if related == nil || related.ProfileID != "profile-link-c" || related.TopFunction != "payments" {
		t.Fatalf("unexpected bridge related profile: %+v", related)
	}

	topDown, err := service.QueryTopDown(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryTopDown: %v", err)
	}
	if topDown.TotalWeight != 8 || len(topDown.Root.Children) != 1 || topDown.Root.Children[0].Name != "rootHandler @ app.go:1" {
		t.Fatalf("unexpected bridge top-down tree: %+v", topDown)
	}

	bottomUp, err := service.QueryBottomUp(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryBottomUp: %v", err)
	}
	if bottomUp.TotalWeight != 8 || len(bottomUp.Root.Children) != 2 || bottomUp.Root.Children[0].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected bridge bottom-up tree: %+v", bottomUp)
	}

	flamegraph, err := service.QueryFlamegraph(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-query-a", MaxDepth: 2})
	if err != nil {
		t.Fatalf("QueryFlamegraph: %v", err)
	}
	if !flamegraph.Truncated || len(flamegraph.Root.Children) != 1 {
		t.Fatalf("unexpected bridge flamegraph: %+v", flamegraph)
	}

	hotPath, err := service.QueryHotPath(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryHotPath: %v", err)
	}
	if len(hotPath.Frames) != 3 || hotPath.Frames[2].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected bridge hot path: %+v", hotPath)
	}

	comparison, err := service.CompareProfiles(ctx, "proj-1", store.ProfileComparisonFilter{
		BaselineProfileID:  "profile-query-a",
		CandidateProfileID: "profile-query-b",
	})
	if err != nil {
		t.Fatalf("CompareProfiles: %v", err)
	}
	if comparison.DurationDeltaNS != 3000000 || len(comparison.TopRegressions) == 0 || comparison.TopRegressions[0].Name != "scoreRules" {
		t.Fatalf("unexpected bridge comparison: %+v", comparison)
	}

	if _, err := service.QueryTopDown(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-invalid"}); err == nil {
		t.Fatal("expected invalid bridge profile query to fail")
	}
}

func TestBridgeEmptyFamiliesDoNotFailWithoutProjectionCursors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openBridgeQuerySourceDB(t)
	if _, err := source.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := source.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'backend', 'Backend', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	bridge := openMigratedBridgeQueryTestDatabase(t)
	service := newBridgeTestService(source, bridge, store.NewMemoryBlobStore(), nil)

	logs, err := service.ListRecentLogs(ctx, "acme", 10)
	if err != nil {
		t.Fatalf("ListRecentLogs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("len(logs) = %d, want 0", len(logs))
	}

	searchedLogs, err := service.SearchLogs(ctx, "acme", "worker", 10)
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(searchedLogs) != 0 {
		t.Fatalf("len(searchedLogs) = %d, want 0", len(searchedLogs))
	}

	replays, err := service.ListReplays(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListReplays: %v", err)
	}
	if len(replays) != 0 {
		t.Fatalf("len(replays) = %d, want 0", len(replays))
	}

	profiles, err := service.ListProfiles(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("len(profiles) = %d, want 0", len(profiles))
	}
}

func openBridgeQuerySourceDB(tb testing.TB) *sql.DB {
	tb.Helper()
	db, err := sqlite.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("sqlite.Open: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func seedBridgeDiscoverSource(t testing.TB, db *sql.DB) {
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
	if _, err := db.Exec(`INSERT INTO events
		(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, payload_json, tags_json, occurred_at, ingested_at)
		VALUES
		('evt-log-0', 'proj-a', 'evt-log-0', NULL, 'frontend@1.0.0', 'production', 'javascript', 'info', 'log', 'worker warmup', 'worker warmup', 'log.go', '{"logger":"web"}', '{}', ?, ?),
		('evt-log-1', 'proj-a', 'evt-log-1', NULL, 'frontend@1.0.0', 'production', 'javascript', 'info', 'log', 'worker started', 'worker started', 'log.go', '{"logger":"web"}', '{}', ?, ?),
		('evt-log-2', 'proj-b', 'evt-log-2', NULL, 'backend@1.0.0', 'production', 'go', 'warning', 'log', 'worker slow', 'worker slow', 'log.go', '{"logger":"api"}', '{}', ?, ?)`,
		now.Add(-110*time.Minute).Format(time.RFC3339), now.Add(-110*time.Minute).Format(time.RFC3339),
		now.Add(-55*time.Minute).Format(time.RFC3339), now.Add(-55*time.Minute).Format(time.RFC3339),
		now.Add(-15*time.Minute).Format(time.RFC3339), now.Add(-15*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed log events: %v", err)
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

func seedBridgeProfileSource(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'backend', 'Backend', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func openMigratedBridgeQueryTestDatabase(tb testing.TB) *sql.DB {
	tb.Helper()
	return migratedBridgeQueryPostgres.OpenDatabase(tb, "urgentry_bridge_query")
}
