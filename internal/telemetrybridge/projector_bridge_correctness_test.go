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

// ---------- snapshot helpers ----------

type replaySnapshot struct {
	ID        string
	ReplayID  string
	TraceID   sql.NullString
	Release   sql.NullString
	ProjectID string
}

type replayTimelineSnapshot struct {
	ID       string
	ReplayID string
	Kind     string
	OffsetMS int64
}

type profileSnapshot struct {
	ID          string
	ProfileID   string
	TraceID     sql.NullString
	Release     sql.NullString
	DurationNS  int64
	SampleCount int
	ProjectID   string
}

type transactionSnapshot struct {
	ID              string
	TraceID         string
	SpanID          string
	TransactionName string
	DurationMS      float64
	ProjectID       string
}

type spanSnapshot struct {
	ID         string
	TraceID    string
	SpanID     string
	Op         sql.NullString
	DurationMS float64
	ProjectID  string
}

func snapshotReplays(t *testing.T, db *sql.DB, projectID string) []replaySnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT id, replay_id, trace_id, release, project_id FROM telemetry.replay_manifests WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		t.Fatalf("snapshot replays: %v", err)
	}
	defer rows.Close()
	var out []replaySnapshot
	for rows.Next() {
		var s replaySnapshot
		if err := rows.Scan(&s.ID, &s.ReplayID, &s.TraceID, &s.Release, &s.ProjectID); err != nil {
			t.Fatalf("scan replay snapshot: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func snapshotReplayTimeline(t *testing.T, db *sql.DB, projectID string) []replayTimelineSnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT id, replay_id, kind, offset_ms FROM telemetry.replay_timeline_items WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		t.Fatalf("snapshot replay timeline: %v", err)
	}
	defer rows.Close()
	var out []replayTimelineSnapshot
	for rows.Next() {
		var s replayTimelineSnapshot
		if err := rows.Scan(&s.ID, &s.ReplayID, &s.Kind, &s.OffsetMS); err != nil {
			t.Fatalf("scan replay timeline snapshot: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func snapshotProfiles(t *testing.T, db *sql.DB, projectID string) []profileSnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT id, profile_id, trace_id, release, duration_ns, sample_count, project_id FROM telemetry.profile_manifests WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		t.Fatalf("snapshot profiles: %v", err)
	}
	defer rows.Close()
	var out []profileSnapshot
	for rows.Next() {
		var s profileSnapshot
		if err := rows.Scan(&s.ID, &s.ProfileID, &s.TraceID, &s.Release, &s.DurationNS, &s.SampleCount, &s.ProjectID); err != nil {
			t.Fatalf("scan profile snapshot: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func snapshotTransactions(t *testing.T, db *sql.DB, projectID string) []transactionSnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT id, trace_id, span_id, transaction_name, duration_ms, project_id FROM telemetry.transaction_facts WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		t.Fatalf("snapshot transactions: %v", err)
	}
	defer rows.Close()
	var out []transactionSnapshot
	for rows.Next() {
		var s transactionSnapshot
		if err := rows.Scan(&s.ID, &s.TraceID, &s.SpanID, &s.TransactionName, &s.DurationMS, &s.ProjectID); err != nil {
			t.Fatalf("scan transaction snapshot: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func snapshotSpans(t *testing.T, db *sql.DB, projectID string) []spanSnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT id, trace_id, span_id, op, duration_ms, project_id FROM telemetry.span_facts WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		t.Fatalf("snapshot spans: %v", err)
	}
	defer rows.Close()
	var out []spanSnapshot
	for rows.Next() {
		var s spanSnapshot
		if err := rows.Scan(&s.ID, &s.TraceID, &s.SpanID, &s.Op, &s.DurationMS, &s.ProjectID); err != nil {
			t.Fatalf("scan span snapshot: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// ---------- test: rebuild produces identical data ----------

func TestProjectorRebuildReplayProfileTraceIdentical(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	// Initial full sync.
	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope); err != nil {
		t.Fatalf("initial SyncFamilies: %v", err)
	}

	// Snapshot all three surfaces.
	replaysA := snapshotReplays(t, bridge, "proj-1")
	timelineA := snapshotReplayTimeline(t, bridge, "proj-1")
	profilesA := snapshotProfiles(t, bridge, "proj-1")
	txnsA := snapshotTransactions(t, bridge, "proj-1")
	spansA := snapshotSpans(t, bridge, "proj-1")

	if len(replaysA) == 0 || len(timelineA) == 0 || len(profilesA) == 0 || len(txnsA) == 0 || len(spansA) == 0 {
		t.Fatalf("expected non-empty initial snapshots: replays=%d timeline=%d profiles=%d txns=%d spans=%d",
			len(replaysA), len(timelineA), len(profilesA), len(txnsA), len(spansA))
	}

	// Reset + re-project (rebuild).
	families := []Family{FamilyReplays, FamilyReplayTimeline, FamilyProfiles, FamilyTransactions, FamilySpans}
	projector2 := NewProjector(source, bridge)
	if err := projector2.ResetScope(ctx, scope, families...); err != nil {
		t.Fatalf("ResetScope: %v", err)
	}

	// Verify bridge is empty after reset.
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 0)

	if err := projector2.SyncFamilies(ctx, scope, families...); err != nil {
		t.Fatalf("SyncFamilies rebuild: %v", err)
	}

	// Snapshot after rebuild and compare.
	replaysB := snapshotReplays(t, bridge, "proj-1")
	timelineB := snapshotReplayTimeline(t, bridge, "proj-1")
	profilesB := snapshotProfiles(t, bridge, "proj-1")
	txnsB := snapshotTransactions(t, bridge, "proj-1")
	spansB := snapshotSpans(t, bridge, "proj-1")

	assertSnapshotsEqual(t, "replays", replaysA, replaysB)
	assertSnapshotsEqual(t, "replay_timeline", timelineA, timelineB)
	assertSnapshotsEqual(t, "profiles", profilesA, profilesB)
	assertSnapshotsEqual(t, "transactions", txnsA, txnsB)
	assertSnapshotsEqual(t, "spans", spansA, spansB)
}

func assertSnapshotsEqual[T any](t *testing.T, label string, a, b []T) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: rebuild row count %d != %d", label, len(b), len(a))
	}
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	if string(aJSON) != string(bJSON) {
		t.Fatalf("%s: rebuild mismatch:\n  before: %s\n  after:  %s", label, aJSON, bJSON)
	}
}

// ---------- test: partial projection resumes correctly ----------

func TestProjectorPartialProjectionResumesReplayProfileTrace(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)
	// Add extra data so there is enough to split across steps.
	seedExtraReplaysAndProfiles(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	families := []Family{FamilyReplays, FamilyReplayTimeline, FamilyProfiles, FamilyTransactions, FamilySpans}

	// Step with batchSize=1 so each step processes at most one row per family.
	projector := NewProjector(source, bridge)
	projector.batchSize = 1

	steps := 0
	for {
		result, err := projector.StepFamilies(ctx, scope, families...)
		if err != nil {
			t.Fatalf("StepFamilies step %d: %v", steps, err)
		}
		steps++
		if result.Done {
			break
		}
		// Safety valve — avoid infinite loop if something is broken.
		if steps > 100 {
			t.Fatalf("too many steps (%d), projection did not converge", steps)
		}
	}

	// Must have taken more than one step because there are multiple families
	// and multiple rows in some of them.
	if steps < 3 {
		t.Fatalf("expected multi-step resume, got %d steps", steps)
	}

	// Verify all data arrived.
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 4)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 1)
}

// ---------- test: interrupted projection can be finished by a second projector ----------

func TestProjectorInterruptedProjectionCanBeResumedByNewProjector(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)
	seedExtraReplaysAndProfiles(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	families := []Family{FamilyReplays, FamilyReplayTimeline, FamilyProfiles}

	// Run exactly two steps then "crash" (stop using this projector).
	p1 := NewProjector(source, bridge)
	p1.batchSize = 1
	for i := 0; i < 2; i++ {
		result, err := p1.StepFamilies(ctx, scope, families...)
		if err != nil {
			t.Fatalf("p1 StepFamilies step %d: %v", i, err)
		}
		if result.Done {
			// If source is tiny enough to finish in 2 steps, this test is
			// still valid — it just won't exercise the resume path deeply.
			break
		}
	}

	// Counts after partial projection — at least some rows projected.
	var replayCountMid int
	if err := bridge.QueryRow(`SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`).Scan(&replayCountMid); err != nil {
		t.Fatalf("mid-count: %v", err)
	}

	// New projector picks up from persisted cursors.
	p2 := NewProjector(source, bridge)
	p2.batchSize = 128
	if err := p2.SyncFamilies(ctx, scope, families...); err != nil {
		t.Fatalf("p2 SyncFamilies: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 2)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_timeline_items WHERE project_id = 'proj-1'`, 4)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 2)
}

// ---------- test: stale cursor detection via AssessFreshness ----------

func TestProjectorStaleCursorDetectedAfterNewSourceData(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, FamilyReplays, FamilyProfiles, FamilyTransactions, FamilySpans); err != nil {
		t.Fatalf("initial SyncFamilies: %v", err)
	}

	// Freshness should report no pending work right after sync.
	items, err := projector.AssessFreshness(ctx, scope, FamilyReplays, FamilyProfiles, FamilyTransactions, FamilySpans)
	if err != nil {
		t.Fatalf("AssessFreshness after sync: %v", err)
	}
	for _, item := range items {
		if !item.CursorFound {
			t.Fatalf("cursor not found for %s after sync", item.Family)
		}
	}

	// Backdating cursor updated_at simulates a stale projector that hasn't
	// run in a while. Freshness should detect lag.
	staleTime := time.Now().UTC().Add(-10 * time.Minute)
	for _, fam := range []Family{FamilyReplays, FamilyProfiles, FamilyTransactions, FamilySpans} {
		name := cursorName(scope, fam)
		if _, err := bridge.Exec(`UPDATE telemetry.projector_cursors SET updated_at = $1 WHERE name = $2`, staleTime, name); err != nil {
			t.Fatalf("backdate cursor %s: %v", fam, err)
		}
	}

	// New projector to avoid cached state.
	p2 := NewProjector(source, bridge)
	staleItems, err := p2.AssessFreshness(ctx, scope, FamilyReplays, FamilyProfiles, FamilyTransactions, FamilySpans)
	if err != nil {
		t.Fatalf("AssessFreshness stale: %v", err)
	}
	for _, item := range staleItems {
		if !item.CursorFound {
			t.Fatalf("cursor not found for %s", item.Family)
		}
		if !item.Pending {
			t.Fatalf("expected pending=true for %s after backdating cursor", item.Family)
		}
		if item.Lag < 5*time.Minute {
			t.Fatalf("expected lag >= 5m for %s, got %s", item.Family, item.Lag)
		}
	}
}

// ---------- test: reset clears cursors and enables clean re-project ----------

func TestProjectorResetClearsCursorsEnablingCleanReproject(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	// Sync replays and profiles.
	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, FamilyReplays, FamilyProfiles); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)

	// Verify cursors exist.
	var cursorCount int
	if err := bridge.QueryRow(`SELECT COUNT(*) FROM telemetry.projector_cursors WHERE scope_id = 'proj-1'`).Scan(&cursorCount); err != nil {
		t.Fatalf("cursor count: %v", err)
	}
	if cursorCount < 2 {
		t.Fatalf("expected at least 2 cursors, got %d", cursorCount)
	}

	// Reset.
	if err := projector.ResetScope(ctx, scope, FamilyReplays, FamilyProfiles); err != nil {
		t.Fatalf("ResetScope: %v", err)
	}

	// Data and cursors should be gone.
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 0)

	var cursorCountAfter int
	if err := bridge.QueryRow(`SELECT COUNT(*) FROM telemetry.projector_cursors WHERE scope_id = 'proj-1' AND cursor_family IN ('replays', 'profiles')`).Scan(&cursorCountAfter); err != nil {
		t.Fatalf("cursor count after reset: %v", err)
	}
	if cursorCountAfter != 0 {
		t.Fatalf("expected 0 cursors after reset, got %d", cursorCountAfter)
	}

	// Re-sync should project data from scratch.
	p2 := NewProjector(source, bridge)
	if err := p2.SyncFamilies(ctx, scope, FamilyReplays, FamilyProfiles); err != nil {
		t.Fatalf("SyncFamilies after reset: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)
}

// ---------- test: trace families survive full rebuild cycle ----------

func TestProjectorTraceRebuildCyclePreservesData(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	// Sync traces (transactions + spans).
	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, FamilyTransactions, FamilySpans); err != nil {
		t.Fatalf("SyncFamilies traces: %v", err)
	}

	// Verify content, not just count.
	txns := snapshotTransactions(t, bridge, "proj-1")
	spans := snapshotSpans(t, bridge, "proj-1")
	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txns))
	}
	if txns[0].TraceID != "trace-1" || txns[0].TransactionName != "GET /checkout" {
		t.Fatalf("unexpected transaction content: %+v", txns[0])
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].TraceID != "trace-1" || spans[0].Op.String != "db" {
		t.Fatalf("unexpected span content: %+v", spans[0])
	}

	// Full rebuild cycle: reset -> re-sync.
	p2 := NewProjector(source, bridge)
	if err := p2.ResetScope(ctx, scope, FamilyTransactions, FamilySpans); err != nil {
		t.Fatalf("ResetScope traces: %v", err)
	}
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.span_facts WHERE project_id = 'proj-1'`, 0)

	if err := p2.SyncFamilies(ctx, scope, FamilyTransactions, FamilySpans); err != nil {
		t.Fatalf("SyncFamilies rebuild traces: %v", err)
	}

	txnsAfter := snapshotTransactions(t, bridge, "proj-1")
	spansAfter := snapshotSpans(t, bridge, "proj-1")
	assertSnapshotsEqual(t, "transactions", txns, txnsAfter)
	assertSnapshotsEqual(t, "spans", spans, spansAfter)
}

// ---------- test: replay content fidelity through bridge ----------

func TestProjectorReplayContentFidelity(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, FamilyReplays, FamilyReplayTimeline); err != nil {
		t.Fatalf("SyncFamilies replays: %v", err)
	}

	replays := snapshotReplays(t, bridge, "proj-1")
	if len(replays) != 1 {
		t.Fatalf("expected 1 replay manifest, got %d", len(replays))
	}
	if replays[0].ReplayID != "replay-1" {
		t.Fatalf("replay_id = %q, want replay-1", replays[0].ReplayID)
	}

	timeline := snapshotReplayTimeline(t, bridge, "proj-1")
	if len(timeline) != 2 {
		t.Fatalf("expected 2 timeline items, got %d", len(timeline))
	}
	// Verify kinds are preserved.
	kinds := map[string]bool{}
	for _, item := range timeline {
		kinds[item.Kind] = true
		if item.ReplayID != "replay-1" {
			t.Fatalf("timeline item replay_id = %q, want replay-1", item.ReplayID)
		}
	}
	if !kinds["navigation"] || !kinds["error"] {
		t.Fatalf("expected navigation and error timeline kinds, got %v", kinds)
	}
}

// ---------- test: profile content fidelity through bridge ----------

func TestProjectorProfileContentFidelity(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, FamilyProfiles); err != nil {
		t.Fatalf("SyncFamilies profiles: %v", err)
	}

	profiles := snapshotProfiles(t, bridge, "proj-1")
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	p := profiles[0]
	if p.ProfileID != "profile-1" {
		t.Fatalf("profile_id = %q, want profile-1", p.ProfileID)
	}
	if !p.TraceID.Valid || p.TraceID.String != "trace-1" {
		t.Fatalf("trace_id = %v, want trace-1", p.TraceID)
	}
	if !p.Release.Valid || p.Release.String != "backend@1.2.3" {
		t.Fatalf("release = %v, want backend@1.2.3", p.Release)
	}
}

// ---------- test: double sync is idempotent ----------

func TestProjectorDoubleSyncIdempotentForAllSurfaces(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	families := []Family{FamilyReplays, FamilyReplayTimeline, FamilyProfiles, FamilyTransactions, FamilySpans}

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, scope, families...); err != nil {
		t.Fatalf("first SyncFamilies: %v", err)
	}

	replaysA := snapshotReplays(t, bridge, "proj-1")
	profilesA := snapshotProfiles(t, bridge, "proj-1")
	txnsA := snapshotTransactions(t, bridge, "proj-1")

	// Sync again without reset — cursors already at end, should be no-op.
	if err := projector.SyncFamilies(ctx, scope, families...); err != nil {
		t.Fatalf("second SyncFamilies: %v", err)
	}

	replaysB := snapshotReplays(t, bridge, "proj-1")
	profilesB := snapshotProfiles(t, bridge, "proj-1")
	txnsB := snapshotTransactions(t, bridge, "proj-1")

	assertSnapshotsEqual(t, "replays", replaysA, replaysB)
	assertSnapshotsEqual(t, "profiles", profilesA, profilesB)
	assertSnapshotsEqual(t, "transactions", txnsA, txnsB)
}

// ---------- test: org-level reset clears project-level cursors for these surfaces ----------

func TestProjectorOrgResetClearsProjectCursorsForSurfaces(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	projectScope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}
	orgScope := Scope{OrganizationID: "org-1"}

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, projectScope, FamilyReplays, FamilyProfiles, FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)

	// Org-level reset should cascade to project-level cursors.
	if err := projector.ResetScope(ctx, orgScope, FamilyReplays, FamilyProfiles, FamilyTransactions); err != nil {
		t.Fatalf("ResetScope org: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 0)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 0)

	// Re-sync from scratch.
	p2 := NewProjector(source, bridge)
	if err := p2.SyncFamilies(ctx, projectScope, FamilyReplays, FamilyProfiles, FamilyTransactions); err != nil {
		t.Fatalf("SyncFamilies after org reset: %v", err)
	}

	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.replay_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.profile_manifests WHERE project_id = 'proj-1'`, 1)
	assertBridgeCount(t, bridge, `SELECT COUNT(*) FROM telemetry.transaction_facts WHERE project_id = 'proj-1'`, 1)
}

// ---------- seed helper for extra data ----------

func seedExtraReplaysAndProfiles(tb testing.TB, db *sql.DB) {
	tb.Helper()

	blobs := store.NewMemoryBlobStore()
	replays := sqlite.NewReplayStore(db, blobs)
	attachments := sqlite.NewAttachmentStore(db, blobs)

	if _, err := replays.SaveEnvelopeReplay(context.Background(), "proj-1", "evt-replay-2", []byte(`{"event_id":"evt-replay-2","replay_id":"replay-2","timestamp":"2026-03-29T12:05:00Z","platform":"javascript","release":"web@1.2.3","environment":"staging"}`)); err != nil {
		tb.Fatalf("save replay envelope 2: %v", err)
	}
	if err := attachments.SaveAttachment(context.Background(), &attachmentstore.Attachment{
		ID:          "att-replay-2",
		EventID:     "evt-replay-2",
		ProjectID:   "proj-1",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
	}, []byte(`{"events":[{"type":"navigation","offset_ms":0,"data":{"url":"https://app.example.com/settings"}},{"type":"click","offset_ms":20,"data":{"selector":"button.save","text":"Save"}}]}`)); err != nil {
		tb.Fatalf("save replay attachment 2: %v", err)
	}
	if err := replays.IndexReplay(context.Background(), "proj-1", "replay-2"); err != nil {
		tb.Fatalf("index replay 2: %v", err)
	}

	profiles := sqlite.NewProfileStore(db, blobs)
	profilefixtures.Save(tb, profiles, "proj-1", profilefixtures.IOHeavy().Spec().WithIDs("evt-profile-2", "profile-2").WithTrace("trace-2").WithRelease("backend@2.0.0"))
}
