package telemetrybridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestProjectorResumesAndRestoresTelemetryFamilies(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}
	steps := 0
	for {
		projector := NewProjector(source, bridge)
		projector.batchSize = 1
		result, err := projector.StepFamilies(ctx, scope)
		if err != nil {
			t.Fatalf("StepFamilies: %v", err)
		}
		steps++
		if result.Done {
			break
		}
	}
	if steps < 4 {
		t.Fatalf("expected resumable multi-step sync, got %d steps", steps)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.event_facts WHERE project_id = 'proj-1'`, 4)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.log_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.outcome_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)

	if _, err := bridge.Exec(`DELETE FROM telemetry.log_facts WHERE project_id = 'proj-1'`); err != nil {
		t.Fatalf("delete log facts: %v", err)
	}
	if _, err := bridge.Exec(`DELETE FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`); err != nil {
		t.Fatalf("delete replay manifests: %v", err)
	}
	if _, err := bridge.Exec(`DELETE FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`); err != nil {
		t.Fatalf("delete replay timeline: %v", err)
	}

	projector := NewProjector(source, bridge)
	projector.batchSize = 1
	if err := projector.ResetScope(ctx, scope, FamilyLogs, FamilyReplays, FamilyReplayTimeline); err != nil {
		t.Fatalf("ResetScope: %v", err)
	}
	if err := projector.SyncFamilies(ctx, scope, FamilyLogs, FamilyReplays, FamilyReplayTimeline); err != nil {
		t.Fatalf("SyncFamilies after reset: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.log_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 2)
}

func TestProjectorStepFamiliesCrossesCompletedFamilies(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	projector := NewProjector(source, bridge)
	projector.batchSize = 128
	result, err := projector.StepFamilies(ctx, Scope{OrganizationID: "org-1", ProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("StepFamilies: %v", err)
	}
	if !result.Done {
		t.Fatalf("expected one-step sync to finish, got %+v", result)
	}
	if result.Processed < 8 {
		t.Fatalf("expected multi-family rows to sync, got %+v", result)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.event_facts WHERE project_id = 'proj-1'`, 4)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.log_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.outcome_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)
}

func TestProjectorResetScopeClearsProjectCursorsForOrganization(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	projectScope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}
	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, projectScope, FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies project scope: %v", err)
	}
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)

	if err := projector.ResetScope(ctx, Scope{OrganizationID: "org-1"}, FamilyTransactions); err != nil {
		t.Fatalf("ResetScope org: %v", err)
	}
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 0)

	if err := projector.SyncFamilies(ctx, projectScope, FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies project scope after org reset: %v", err)
	}
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
}

func openProjectorSourceDB(tb testing.TB) *sql.DB {
	tb.Helper()
	db, err := sqlite.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("sqlite.Open: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func seedProjectorSource(tb testing.TB, db *sql.DB) {
	tb.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		tb.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'backend', 'Backend', 'go', 'active')`); err != nil {
		tb.Fatalf("insert project: %v", err)
	}

	events := sqlite.NewEventStore(db)
	logPayload := json.RawMessage(`{"logger":"api","contexts":{"trace":{"trace_id":"trace-1","span_id":"span-root"}}}`)
	if err := events.SaveEvent(context.Background(), &store.StoredEvent{
		ID:             "evt-log-row",
		ProjectID:      "proj-1",
		EventID:        "evt-log",
		EventType:      "log",
		Platform:       "go",
		Level:          "info",
		OccurredAt:     time.Now().UTC().Add(-2 * time.Minute),
		IngestedAt:     time.Now().UTC().Add(-2 * time.Minute),
		Message:        "api worker started",
		Title:          "api worker started",
		Tags:           map[string]string{"environment": "production"},
		NormalizedJSON: logPayload,
	}); err != nil {
		tb.Fatalf("save log event: %v", err)
	}
	if err := events.SaveEvent(context.Background(), &store.StoredEvent{
		ID:             "evt-error-row",
		ProjectID:      "proj-1",
		EventID:        "evt-error",
		GroupID:        "grp-1",
		EventType:      "error",
		Platform:       "go",
		Level:          "error",
		OccurredAt:     time.Now().UTC().Add(-time.Minute),
		IngestedAt:     time.Now().UTC().Add(-time.Minute),
		Message:        "request failed",
		Title:          "request failed",
		Culprit:        "handler.go",
		ReleaseID:      "backend@1.2.3",
		Environment:    "production",
		Tags:           map[string]string{"environment": "production"},
		NormalizedJSON: json.RawMessage(`{"event_id":"evt-error"}`),
	}); err != nil {
		tb.Fatalf("save error event: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen) VALUES ('grp-1', 'proj-1', 'urgentry-v1', 'grp-1', 'request failed', 'handler.go', 'error', 'unresolved', ?, ?, 1)`, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		tb.Fatalf("insert group: %v", err)
	}

	traces := sqlite.NewTraceStore(db)
	if err := traces.SaveTransaction(context.Background(), &store.StoredTransaction{
		ProjectID:      "proj-1",
		EventID:        "evt-error",
		TraceID:        "trace-1",
		SpanID:         "span-root",
		Transaction:    "GET /checkout",
		Op:             "http.server",
		Status:         "ok",
		Platform:       "go",
		Environment:    "production",
		ReleaseID:      "backend@1.2.3",
		StartTimestamp: time.Now().UTC().Add(-30 * time.Second),
		EndTimestamp:   time.Now().UTC().Add(-29 * time.Second),
		DurationMS:     123.4,
		Tags:           map[string]string{"environment": "production"},
		Measurements:   map[string]store.StoredMeasurement{"lcp": {Value: 42, Unit: "millisecond"}},
		Spans: []store.StoredSpan{{
			ID:               "span-1",
			ProjectID:        "proj-1",
			TransactionEventID: "evt-error",
			TraceID:          "trace-1",
			SpanID:           "span-child",
			ParentSpanID:     "span-root",
			Op:               "db",
			Description:      "SELECT 1",
			Status:           "ok",
			StartTimestamp:   time.Now().UTC().Add(-29900 * time.Millisecond),
			EndTimestamp:     time.Now().UTC().Add(-29800 * time.Millisecond),
			DurationMS:       10,
			Tags:             map[string]string{"db.system": "sqlite"},
			Data:             map[string]any{"rows": 1},
		}},
	}); err != nil {
		tb.Fatalf("save transaction: %v", err)
	}

	if err := sqlite.NewOutcomeStore(db).SaveOutcome(context.Background(), &sqlite.Outcome{
		ID:          "outcome-1",
		ProjectID:   "proj-1",
		Category:    "error",
		Reason:      "sample_rate",
		Quantity:    1,
		Source:      "client_report",
		RecordedAt:  time.Now().UTC(),
		DateCreated: time.Now().UTC(),
	}); err != nil {
		tb.Fatalf("save outcome: %v", err)
	}

	blobs := store.NewMemoryBlobStore()
	replays := sqlite.NewReplayStore(db, blobs)
	attachments := sqlite.NewAttachmentStore(db, blobs)
	if _, err := replays.SaveEnvelopeReplay(context.Background(), "proj-1", "evt-replay", []byte(`{"event_id":"evt-replay","replay_id":"replay-1","timestamp":"2026-03-29T12:00:00Z","platform":"javascript","release":"web@1.2.3","environment":"production"}`)); err != nil {
		tb.Fatalf("save replay envelope: %v", err)
	}
	if err := attachments.SaveAttachment(context.Background(), &attachmentstore.Attachment{
		ID:          "att-replay-1",
		EventID:     "evt-replay",
		ProjectID:   "proj-1",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
	}, []byte(`{"events":[{"type":"navigation","offset_ms":0,"data":{"url":"https://app.example.com"}},{"type":"error","offset_ms":10,"data":{"event_id":"evt-error","trace_id":"trace-1","message":"request failed"}}]}`)); err != nil {
		tb.Fatalf("save replay attachment: %v", err)
	}
	if err := replays.IndexReplay(context.Background(), "proj-1", "replay-1"); err != nil {
		tb.Fatalf("index replay: %v", err)
	}

	profiles := sqlite.NewProfileStore(db, blobs)
	profilefixtures.Save(tb, profiles, "proj-1", profilefixtures.SaveRead().Spec().WithIDs("evt-profile", "profile-1").WithTrace("trace-1").WithRelease("backend@1.2.3"))
}

func assertBridgeCount(tb testing.TB, db *sql.DB, query string, want int) {
	tb.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		tb.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		tb.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}
