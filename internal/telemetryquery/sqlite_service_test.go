package telemetryquery

import (
	"context"
	"strings"
	"testing"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestSQLiteServiceReadsAcrossSurfaces(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, db)
	seedBridgeBenchTraffic(t, db)

	blobs := store.NewMemoryBlobStore()
	seedBridgeBenchReplay(t, db, blobs)
	seedBridgeBenchProfiles(t, db, blobs)

	service := NewSQLiteService(db, blobs)

	logs, err := service.SearchLogs(ctx, "acme", "api", 50)
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) == 0 || logs[0].Logger == "" {
		t.Fatalf("unexpected logs: %+v", logs)
	}

	recentLogs, err := service.ListRecentLogs(ctx, "acme", 10)
	if err != nil {
		t.Fatalf("ListRecentLogs: %v", err)
	}
	if len(recentLogs) == 0 {
		t.Fatal("ListRecentLogs returned no rows")
	}

	transactions, err := service.ListRecentTransactions(ctx, "acme", 50)
	if err != nil {
		t.Fatalf("ListRecentTransactions: %v", err)
	}
	if len(transactions) == 0 || transactions[0].Transaction == "" {
		t.Fatalf("unexpected transactions: %+v", transactions)
	}

	searchTransactions, err := service.SearchTransactions(ctx, "acme", "GET /bench/00", 50)
	if err != nil {
		t.Fatalf("SearchTransactions: %v", err)
	}
	if len(searchTransactions) == 0 {
		t.Fatal("SearchTransactions returned no rows")
	}

	tableQuery := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "count"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 25,
	}
	table, err := service.ExecuteTable(ctx, tableQuery)
	if err != nil {
		t.Fatalf("ExecuteTable: %v", err)
	}
	if len(table.Rows) == 0 {
		t.Fatal("ExecuteTable returned no rows")
	}

	series, err := service.ExecuteSeries(ctx, discover.Query{
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
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Rollup: &discover.Rollup{Interval: "1h"},
		Limit:  24,
	})
	if err != nil {
		t.Fatalf("ExecuteSeries: %v", err)
	}
	if len(series.Points) == 0 {
		t.Fatal("ExecuteSeries returned no points")
	}

	plan, err := service.Explain(tableQuery)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if plan.Dataset != discover.DatasetTransactions || !strings.Contains(plan.SQL, "transactions") {
		t.Fatalf("unexpected explain plan: %+v", plan)
	}

	traceTransactions, err := service.ListTransactions(ctx, "proj-a", 50)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(traceTransactions) == 0 {
		t.Fatal("ListTransactions returned no rows")
	}

	byTrace, err := service.ListTransactionsByTrace(ctx, "proj-a", "bench-trace-000")
	if err != nil {
		t.Fatalf("ListTransactionsByTrace: %v", err)
	}
	if len(byTrace) == 0 || byTrace[0].EventID == "" {
		t.Fatalf("unexpected trace transactions: %+v", byTrace)
	}

	spans, err := service.ListTraceSpans(ctx, "proj-a", "bench-trace-000")
	if err != nil {
		t.Fatalf("ListTraceSpans: %v", err)
	}
	if len(spans) == 0 || spans[0].SpanID == "" {
		t.Fatalf("unexpected spans: %+v", spans)
	}

	replays, err := service.ListReplays(ctx, "proj-a", 20)
	if err != nil {
		t.Fatalf("ListReplays: %v", err)
	}
	if len(replays) == 0 || replays[0].ReplayID == "" {
		t.Fatalf("unexpected replays: %+v", replays)
	}

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
	if len(timeline) == 0 || timeline[0].Kind != "error" {
		t.Fatalf("unexpected replay timeline: %+v", timeline)
	}

	profiles, err := service.ListProfiles(ctx, "proj-a", 20)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) == 0 || profiles[0].ProfileID == "" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}

	profile, err := service.GetProfile(ctx, "proj-a", "bench-profile-00")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.Manifest.ProfileID != "bench-profile-00" || len(profile.TopFunctions) == 0 {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	traceProfiles, err := service.FindProfilesByTrace(ctx, "proj-a", "bench-trace-000", 10)
	if err != nil {
		t.Fatalf("FindProfilesByTrace: %v", err)
	}
	if len(traceProfiles) == 0 {
		t.Fatal("FindProfilesByTrace returned no rows")
	}

	highlights, err := service.ListReleaseProfileHighlights(ctx, "proj-a", "backend@1.0.0", 10)
	if err != nil {
		t.Fatalf("ListReleaseProfileHighlights: %v", err)
	}
	if len(highlights) == 0 {
		t.Fatal("ListReleaseProfileHighlights returned no rows")
	}

	related, err := service.FindRelatedProfile(ctx, "proj-a", "bench-trace-000", "checkout", "backend@1.0.0")
	if err != nil {
		t.Fatalf("FindRelatedProfile: %v", err)
	}
	if related == nil {
		t.Fatal("FindRelatedProfile returned nil")
	}

	topDown, err := service.QueryTopDown(ctx, "proj-a", store.ProfileQueryFilter{ProfileID: "bench-profile-00"})
	if err != nil {
		t.Fatalf("QueryTopDown: %v", err)
	}
	if topDown.TotalWeight == 0 {
		t.Fatalf("unexpected top-down tree: %+v", topDown)
	}

	hotPath, err := service.QueryHotPath(ctx, "proj-a", store.ProfileQueryFilter{ProfileID: "bench-profile-00"})
	if err != nil {
		t.Fatalf("QueryHotPath: %v", err)
	}
	if len(hotPath.Frames) == 0 {
		t.Fatalf("unexpected hot path: %+v", hotPath)
	}

	comparison, err := service.CompareProfiles(ctx, "proj-a", store.ProfileComparisonFilter{
		BaselineProfileID:  "bench-profile-00",
		CandidateProfileID: "bench-profile-01",
	})
	if err != nil {
		t.Fatalf("CompareProfiles: %v", err)
	}
	if comparison.BaselineProfileID != "bench-profile-00" || comparison.CandidateProfileID != "bench-profile-01" {
		t.Fatalf("unexpected comparison: %+v", comparison)
	}
}

func TestSQLiteServiceListOrgReplays(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openBridgeQuerySourceDB(t)
	seedBridgeDiscoverSource(t, db)

	blobs := store.NewMemoryBlobStore()
	replays := sqlite.NewReplayStore(db, blobs)
	for _, item := range []struct {
		projectID string
		eventID   string
		replayID  string
		timestamp string
	}{
		{projectID: "proj-a", eventID: "evt-org-replay-a", replayID: "org-replay-a", timestamp: "2026-03-29T12:00:00Z"},
		{projectID: "proj-b", eventID: "evt-org-replay-b", replayID: "org-replay-b", timestamp: "2026-03-29T12:10:00Z"},
	} {
		payload := []byte(`{"event_id":"` + item.eventID + `","replay_id":"` + item.replayID + `","timestamp":"` + item.timestamp + `"}`)
		if _, err := replays.SaveEnvelopeReplay(ctx, item.projectID, item.eventID, payload); err != nil {
			t.Fatalf("SaveEnvelopeReplay(%s): %v", item.replayID, err)
		}
		if err := replays.IndexReplay(ctx, item.projectID, item.replayID); err != nil {
			t.Fatalf("IndexReplay(%s): %v", item.replayID, err)
		}
	}

	service := NewSQLiteService(db, blobs)
	items, err := service.ListOrgReplays(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("ListOrgReplays: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ReplayID != "org-replay-b" || items[0].ProjectID != "proj-b" {
		t.Fatalf("first org replay = %+v, want org-replay-b/proj-b", items[0])
	}
	if items[1].ReplayID != "org-replay-a" || items[1].ProjectID != "proj-a" {
		t.Fatalf("second org replay = %+v, want org-replay-a/proj-a", items[1])
	}
}

func TestSQLiteServiceUsesInjectedIssueSearchStore(t *testing.T) {
	t.Parallel()

	db := openBridgeQuerySourceDB(t)
	blobs := store.NewMemoryBlobStore()
	issues := &stubIssueSearchStore{
		result: []store.DiscoverIssue{{ID: "issue-1", Title: "synthetic issue"}},
	}
	service := newSQLiteService(db, blobs, issues)

	items, err := service.SearchDiscoverIssues(context.Background(), "acme", "all", "synthetic", 25)
	if err != nil {
		t.Fatalf("SearchDiscoverIssues: %v", err)
	}
	if len(items) != 1 || items[0].ID != "issue-1" {
		t.Fatalf("unexpected issues: %+v", items)
	}
	if issues.orgSlug != "acme" || issues.rawQuery != "synthetic" || issues.limit != 25 {
		t.Fatalf("unexpected issue search call: %+v", issues)
	}
}

type stubIssueSearchStore struct {
	orgSlug  string
	filter   string
	rawQuery string
	limit    int
	result   []store.DiscoverIssue
}

func (s *stubIssueSearchStore) SearchDiscoverIssues(_ context.Context, orgSlug, filter, rawQuery string, limit int) ([]store.DiscoverIssue, error) {
	s.orgSlug = orgSlug
	s.filter = filter
	s.rawQuery = rawQuery
	s.limit = limit
	return s.result, nil
}
