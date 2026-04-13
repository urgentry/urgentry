package telemetryquery

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func BenchmarkBridgeService(b *testing.B) {
	fx := newBenchmarkBridgeService(b)
	query := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: fx.orgSlug,
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
			{Alias: "p95", Expr: discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "p95"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T08:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 25,
	}

	b.Run("SearchLogs", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			items, err := fx.service.SearchLogs(fx.ctx, fx.orgSlug, "api", 50)
			if err != nil {
				b.Fatalf("SearchLogs: %v", err)
			}
			if len(items) == 0 {
				b.Fatal("SearchLogs returned no rows")
			}
		}
	})

	b.Run("ExecuteTransactionsTable", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := fx.service.ExecuteTable(fx.ctx, query)
			if err != nil {
				b.Fatalf("ExecuteTable: %v", err)
			}
			if len(result.Rows) == 0 {
				b.Fatal("ExecuteTable returned no rows")
			}
		}
	})

	b.Run("TraceDetail", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			transactions, err := fx.service.ListTransactionsByTrace(fx.ctx, fx.projectID, fx.traceID)
			if err != nil {
				b.Fatalf("ListTransactionsByTrace: %v", err)
			}
			spans, err := fx.service.ListTraceSpans(fx.ctx, fx.projectID, fx.traceID)
			if err != nil {
				b.Fatalf("ListTraceSpans: %v", err)
			}
			if len(transactions) == 0 || len(spans) == 0 {
				b.Fatalf("trace detail returned incomplete data: txns=%d spans=%d", len(transactions), len(spans))
			}
		}
	})

	b.Run("GetReplay", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			record, err := fx.service.GetReplay(fx.ctx, fx.projectID, fx.replayID)
			if err != nil {
				b.Fatalf("GetReplay: %v", err)
			}
			if record.Manifest.ReplayID == "" || len(record.Timeline) == 0 {
				b.Fatalf("GetReplay returned incomplete record: %+v", record)
			}
		}
	})

	b.Run("GetProfile", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			record, err := fx.service.GetProfile(fx.ctx, fx.projectID, fx.profileID)
			if err != nil {
				b.Fatalf("GetProfile: %v", err)
			}
			if record.Manifest.ProfileID == "" || len(record.TopFunctions) == 0 {
				b.Fatalf("GetProfile returned incomplete record: %+v", record)
			}
		}
	})
}

type benchmarkBridgeFixture struct {
	ctx       context.Context
	service   Service
	orgSlug   string
	projectID string
	traceID   string
	replayID  string
	profileID string
}

var (
	benchmarkBridgeFixtureOnce    sync.Once
	benchmarkBridgeFixtureData    benchmarkBridgeFixture
	benchmarkBridgeFixtureCleanup func()
	benchmarkBridgeCleanupOnce    sync.Once
)

func newBenchmarkBridgeService(b *testing.B) benchmarkBridgeFixture {
	b.Helper()

	benchmarkBridgeFixtureOnce.Do(func() {
		ctx := context.Background()
		source, sourceCleanup := openBenchmarkBridgeQuerySourceDB(b)
		seedBridgeDiscoverSource(b, source)
		seedBridgeBenchTraffic(b, source)

		blobs := store.NewMemoryBlobStore()
		seedBridgeBenchReplay(b, source, blobs)
		seedBridgeBenchProfiles(b, source, blobs)

		bridge, bridgeCleanup, err := migratedBridgeQueryPostgres.OpenPersistentDatabase(b, "urgentry_bridge_query_bench")
		if err != nil {
			b.Fatalf("OpenPersistentDatabase: %v", err)
		}
		benchmarkBridgeFixtureCleanup = func() {
			if bridgeCleanup != nil {
				bridgeCleanup()
			}
			if sourceCleanup != nil {
				sourceCleanup()
			}
		}
		projector := telemetrybridge.NewProjector(source, bridge)
		if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1"}, telemetrybridge.FamilyLogs, telemetrybridge.FamilyTransactions); err != nil {
			b.Fatalf("SyncFamilies org scope: %v", err)
		}
		if err := projector.SyncFamilies(ctx, telemetrybridge.Scope{OrganizationID: "org-1", ProjectID: "proj-a"}, telemetrybridge.FamilyTransactions, telemetrybridge.FamilySpans, telemetrybridge.FamilyReplays, telemetrybridge.FamilyReplayTimeline, telemetrybridge.FamilyProfiles); err != nil {
			b.Fatalf("SyncFamilies project scope: %v", err)
		}
		benchmarkBridgeFixtureData = benchmarkBridgeFixture{
			ctx:       ctx,
			service:   newBridgeTestService(source, bridge, blobs, nil),
			orgSlug:   "acme",
			projectID: "proj-a",
			traceID:   "bench-trace-000",
			replayID:  "bench-replay-00",
			profileID: "bench-profile-00",
		}
	})
	if benchmarkBridgeFixtureCleanup != nil {
		b.Cleanup(func() {
			benchmarkBridgeCleanupOnce.Do(func() {
				benchmarkBridgeFixtureCleanup()
			})
		})
	}

	return benchmarkBridgeFixtureData
}

func openBenchmarkBridgeQuerySourceDB(b *testing.B) (*sql.DB, func()) {
	b.Helper()

	dir, err := os.MkdirTemp("", "urgentry-bridge-source-*")
	if err != nil {
		b.Fatalf("MkdirTemp: %v", err)
	}
	db, err := sqlite.Open(dir)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	return db, func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}
}

func seedBridgeBenchTraffic(tb testing.TB, db *sql.DB) {
	tb.Helper()

	now := time.Date(2026, time.March, 29, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 96; i++ {
		projectID := "proj-a"
		projectPlatform := "javascript"
		release := "frontend@1.0.0"
		logger := "web"
		if i%2 == 1 {
			projectID = "proj-b"
			projectPlatform = "go"
			release = "backend@1.0.0"
			logger = "api"
		}
		occurredAt := now.Add(-time.Duration(i) * time.Minute)
		if _, err := db.Exec(`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, payload_json, tags_json, occurred_at, ingested_at)
			VALUES (?, ?, ?, NULL, ?, 'production', ?, 'info', 'log', ?, ?, 'bench.go', ?, '{}', ?, ?)`,
			fmt.Sprintf("evt-bench-log-%03d", i),
			projectID,
			fmt.Sprintf("evt-bench-log-%03d", i),
			release,
			projectPlatform,
			fmt.Sprintf("bench log %03d", i),
			fmt.Sprintf("bench log %03d", i),
			fmt.Sprintf(`{"logger":"%s","contexts":{"trace":{"trace_id":"trace-%03d","span_id":"span-%03d"}}}`, logger, i, i),
			occurredAt.Format(time.RFC3339),
			occurredAt.Format(time.RFC3339),
		); err != nil {
			tb.Fatalf("seed benchmark log %d: %v", i, err)
		}
	}

	traces := sqlite.NewTraceStore(db)
	for i := 0; i < 48; i++ {
		startedAt := now.Add(-time.Duration(i) * time.Minute)
		traceID := fmt.Sprintf("bench-trace-%03d", i)
		transaction := &store.StoredTransaction{
			ProjectID:      "proj-a",
			EventID:        fmt.Sprintf("evt-bench-txn-%03d", i),
			TraceID:        traceID,
			SpanID:         fmt.Sprintf("bench-span-root-%03d", i),
			Transaction:    fmt.Sprintf("GET /bench/%02d", i%8),
			Op:             "http.server",
			Status:         "ok",
			Platform:       "go",
			Environment:    "production",
			ReleaseID:      "backend@1.0.0",
			StartTimestamp: startedAt,
			EndTimestamp:   startedAt.Add(250 * time.Millisecond),
			DurationMS:     float64(80 + i),
			Tags:           map[string]string{"environment": "production"},
			Spans: []store.StoredSpan{
				{
					ID:                 fmt.Sprintf("bench-span-db-%03d", i),
					ProjectID:          "proj-a",
					TransactionEventID: fmt.Sprintf("evt-bench-txn-%03d", i),
					TraceID:            traceID,
					SpanID:             fmt.Sprintf("bench-span-db-%03d", i),
					ParentSpanID:       fmt.Sprintf("bench-span-root-%03d", i),
					Op:                 "db",
					Description:        "SELECT 1",
					Status:             "ok",
					StartTimestamp:     startedAt.Add(20 * time.Millisecond),
					EndTimestamp:       startedAt.Add(60 * time.Millisecond),
					DurationMS:         40,
					Tags:               map[string]string{"db.system": "sqlite"},
					Data:               map[string]any{"rows": 1},
				},
				{
					ID:                 fmt.Sprintf("bench-span-render-%03d", i),
					ProjectID:          "proj-a",
					TransactionEventID: fmt.Sprintf("evt-bench-txn-%03d", i),
					TraceID:            traceID,
					SpanID:             fmt.Sprintf("bench-span-render-%03d", i),
					ParentSpanID:       fmt.Sprintf("bench-span-root-%03d", i),
					Op:                 "template.render",
					Description:        "render page",
					Status:             "ok",
					StartTimestamp:     startedAt.Add(70 * time.Millisecond),
					EndTimestamp:       startedAt.Add(140 * time.Millisecond),
					DurationMS:         70,
				},
			},
		}
		if err := traces.SaveTransaction(context.Background(), transaction); err != nil {
			tb.Fatalf("seed benchmark transaction %d: %v", i, err)
		}
	}
}

func seedBridgeBenchReplay(tb testing.TB, db *sql.DB, blobs store.BlobStore) {
	tb.Helper()

	replays := sqlite.NewReplayStore(db, blobs)
	attachments := sqlite.NewAttachmentStore(db, blobs)
	for i := 0; i < 8; i++ {
		replayID := fmt.Sprintf("bench-replay-%02d", i)
		eventID := fmt.Sprintf("evt-bench-replay-%02d", i)
		payload := fmt.Sprintf(`{"event_id":"%s","replay_id":"%s","timestamp":"2026-03-29T12:00:00Z","platform":"javascript","release":"web@1.2.3","environment":"production","contexts":{"trace":{"trace_id":"bench-trace-%03d"}}}`, eventID, replayID, i)
		if _, err := replays.SaveEnvelopeReplay(context.Background(), "proj-a", eventID, []byte(payload)); err != nil {
			tb.Fatalf("SaveEnvelopeReplay %d: %v", i, err)
		}
		if err := attachments.SaveAttachment(context.Background(), &attachmentstore.Attachment{
			ID:          fmt.Sprintf("att-bench-replay-%02d", i),
			EventID:     eventID,
			ProjectID:   "proj-a",
			Name:        "segment-1.rrweb",
			ContentType: "application/json",
		}, []byte(`{"events":[{"type":"navigation","offset_ms":0,"data":{"url":"https://app.example.com"}},{"type":"error","offset_ms":10,"data":{"event_id":"evt-error","trace_id":"bench-trace-000","message":"request failed"}}]}`)); err != nil {
			tb.Fatalf("SaveAttachment %d: %v", i, err)
		}
		if err := replays.IndexReplay(context.Background(), "proj-a", replayID); err != nil {
			tb.Fatalf("IndexReplay %d: %v", i, err)
		}
	}
}

func seedBridgeBenchProfiles(tb testing.TB, db *sql.DB, blobs store.BlobStore) {
	tb.Helper()

	profiles := sqlite.NewProfileStore(db, blobs)
	for i := 0; i < 8; i++ {
		spec := profilefixtures.SaveRead().Spec().
			WithIDs(fmt.Sprintf("evt-bench-profile-%02d", i), fmt.Sprintf("bench-profile-%02d", i)).
			WithTrace(fmt.Sprintf("bench-trace-%03d", i)).
			WithRelease("backend@1.0.0").
			WithTimestamp(time.Date(2026, time.March, 29, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute))
		profilefixtures.Save(tb, profiles, "proj-a", spec)
	}
}
