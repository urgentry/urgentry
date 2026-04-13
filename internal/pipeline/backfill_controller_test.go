package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"urgentry/internal/normalize"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

func TestBackfillControllerProcessesNativeRuns(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	ctx := context.Background()

	debugFiles := sqlite.NewDebugFileStore(db, store.NewMemoryBlobStore())
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.dSYM",
		UUID:      "debug-1",
		CodeID:    "code-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}

	insertNativeBackfillEvent(t, db, "evt-native-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC().Add(-time.Minute))
	insertNativeBackfillEvent(t, db, "evt-native-2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", time.Now().UTC())

	runs := sqlite.NewBackfillStore(db)
	run, err := runs.CreateRun(ctx, sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	controller := NewBackfillController(runs, debugFiles, "worker-1")
	controller.chunkSize = 1

	advanced, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce first: %v", err)
	}
	if !advanced {
		t.Fatal("expected first run step to advance")
	}
	mid, err := runs.GetRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun mid: %v", err)
	}
	if mid == nil || mid.Status != sqlite.BackfillStatusPending || mid.ProcessedItems != 1 || mid.UpdatedItems != 1 || mid.TotalItems != 2 {
		t.Fatalf("mid run = %+v", mid)
	}

	advanced, err = controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}
	if !advanced {
		t.Fatal("expected second run step to advance")
	}
	done, err := runs.GetRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun done: %v", err)
	}
	if done == nil || done.Status != sqlite.BackfillStatusCompleted || done.ProcessedItems != 2 || done.UpdatedItems != 2 {
		t.Fatalf("done run = %+v", done)
	}
	for _, eventID := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"} {
		item, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", eventID)
		if err != nil {
			t.Fatalf("GetEvent %s: %v", eventID, err)
		}
		if item == nil {
			t.Fatalf("expected event %s", eventID)
		}
		var evt normalize.Event
		if err := json.Unmarshal(item.NormalizedJSON, &evt); err != nil {
			t.Fatalf("unmarshal %s: %v", eventID, err)
		}
		frame := evt.Exception.Values[0].Stacktrace.Frames[0]
		if frame.Filename != "src/AppDelegate.swift" || frame.Function != "main" || frame.Lineno != 42 {
			t.Fatalf("unexpected symbolicated frame for %s: %+v", eventID, frame)
		}
	}
	primary, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetEvent primary: %v", err)
	}
	if primary == nil || primary.GroupID == "" {
		t.Fatalf("expected primary event group: %+v", primary)
	}
	var groupTitle, groupCulprit string
	if err := db.QueryRow(`SELECT title, culprit FROM groups WHERE id = ?`, primary.GroupID).Scan(&groupTitle, &groupCulprit); err != nil {
		t.Fatalf("query synced group: %v", err)
	}
	if groupTitle == "stale title" || groupCulprit == "stale culprit" {
		t.Fatalf("group summary was not refreshed: title=%q culprit=%q", groupTitle, groupCulprit)
	}
}

func TestBackfillControllerResumesExpiredRun(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	ctx := context.Background()

	debugFiles := sqlite.NewDebugFileStore(db, store.NewMemoryBlobStore())
	insertNativeBackfillEvent(t, db, "evt-native-resume", "cccccccccccccccccccccccccccccccc", time.Now().UTC())

	runs := sqlite.NewBackfillStore(db)
	run, err := runs.CreateRun(ctx, sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := db.Exec(`UPDATE backfill_runs SET status = 'running', lease_until = ?, worker_id = 'dead-worker' WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), run.ID); err != nil {
		t.Fatalf("expire run lease: %v", err)
	}

	controller := NewBackfillController(runs, debugFiles, "worker-2")
	advanced, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !advanced {
		t.Fatal("expected expired run to resume")
	}
}

func TestBackfillControllerRunLoopProcessesPendingRun(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	debugFiles := sqlite.NewDebugFileStore(db, store.NewMemoryBlobStore())
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.dSYM",
		UUID:      "debug-1",
		CodeID:    "code-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}
	insertNativeBackfillEvent(t, db, "evt-native-loop-1", "ffffffffffffffffffffffffffffffff", time.Now().UTC())

	runs := sqlite.NewBackfillStore(db)
	run, err := runs.CreateRun(ctx, sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	controller := NewBackfillController(runs, debugFiles, "worker-loop")
	go controller.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := runs.GetRun(context.Background(), "org-1", run.ID)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if current != nil && current.Status == sqlite.BackfillStatusCompleted {
			if current.ProcessedItems != 1 || current.UpdatedItems != 1 || current.FinishedAt.IsZero() {
				t.Fatalf("unexpected completed run: %+v", current)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for backfill loop to finish")
}

func TestBackfillControllerQueuedModeMarksJobDone(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	debugFiles := sqlite.NewDebugFileStore(db, store.NewMemoryBlobStore())
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.dSYM",
		UUID:      "debug-1",
		CodeID:    "code-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}
	insertNativeBackfillEvent(t, db, "evt-native-queued-1", "12121212121212121212121212121212", time.Now().UTC())
	runs := sqlite.NewBackfillStore(db)
	run, err := runs.CreateRun(ctx, sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	queue := &scriptedQueue{
		jobs: []*runtimeasync.Job{{
			ID:   "job-1",
			Kind: sqlite.JobKindBackfill,
		}},
	}
	controller := NewBackfillController(runs, debugFiles, "worker-queued")
	controller.SetQueue(queue)
	controller.SetEnqueuer(queue)

	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := runs.GetRun(context.Background(), "org-1", run.ID)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if current != nil && current.Status == sqlite.BackfillStatusCompleted {
			cancel()
			<-done
			if len(queue.done) == 0 || queue.done[0] != "job-1" {
				t.Fatalf("expected queued job to be marked done, got %+v", queue.done)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for queued backfill loop to finish")
}

func TestBackfillControllerProcessesTelemetryRebuildRuns(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	ctx := context.Background()

	runs := sqlite.NewBackfillStore(db)
	run, err := runs.CreateRun(ctx, sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	rebuilder := &scriptedTelemetryRebuilder{
		total: 3,
		steps: []telemetryStep{
			{processed: 2, done: false},
			{processed: 1, done: true},
		},
	}
	controller := NewBackfillController(runs, nil, "worker-telemetry")
	controller.SetTelemetryRebuilder(rebuilder)

	advanced, err := controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce first: %v", err)
	}
	if !advanced {
		t.Fatal("expected first run step to advance")
	}
	mid, err := runs.GetRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun mid: %v", err)
	}
	if mid == nil || mid.Status != sqlite.BackfillStatusPending || mid.TotalItems != 3 || mid.ProcessedItems != 2 || mid.UpdatedItems != 2 {
		t.Fatalf("mid run = %+v", mid)
	}
	if rebuilder.resetCalls != 1 || rebuilder.estimateCalls != 1 {
		t.Fatalf("expected one reset and estimate, got reset=%d estimate=%d", rebuilder.resetCalls, rebuilder.estimateCalls)
	}

	advanced, err = controller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}
	if !advanced {
		t.Fatal("expected second run step to advance")
	}
	done, err := runs.GetRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun done: %v", err)
	}
	if done == nil || done.Status != sqlite.BackfillStatusCompleted || done.ProcessedItems != 3 || done.UpdatedItems != 3 {
		t.Fatalf("done run = %+v", done)
	}
}

func insertNativeBackfillEvent(t *testing.T, db *sql.DB, rowID, eventID string, occurredAt time.Time) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen)
		 VALUES (?, 'proj-1', 'urgentry-v1', ?, 'stale title', 'stale culprit', 'fatal', 'unresolved', ?, ?, 1)`,
		"group-"+rowID, "group-"+rowID, occurredAt.Format(time.RFC3339), occurredAt.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert group %s: %v", rowID, err)
	}
	payload := `{"event_id":"` + eventID + `","release":"ios@1.2.3","environment":"production","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"exception":{"values":[{"type":"Minidump","value":"Native crash","stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1"}]}}]}}`
	if err := sqlite.NewEventStore(db).SaveEvent(context.Background(), &store.StoredEvent{
		ID:             rowID,
		ProjectID:      "proj-1",
		EventID:        eventID,
		GroupID:        "group-" + rowID,
		ReleaseID:      "ios@1.2.3",
		Environment:    "production",
		Platform:       "cocoa",
		Level:          "fatal",
		EventType:      "error",
		OccurredAt:     occurredAt,
		IngestedAt:     occurredAt,
		Message:        "Native crash",
		Title:          "Native crash",
		Culprit:        "Native crash",
		Tags:           map[string]string{"ingest.kind": "minidump"},
		NormalizedJSON: json.RawMessage(payload),
	}); err != nil {
		t.Fatalf("SaveEvent %s: %v", eventID, err)
	}
}

type scriptedQueue struct {
	mu   sync.Mutex
	jobs []*runtimeasync.Job
	done []string
}

func (q *scriptedQueue) Enqueue(context.Context, string, string, []byte, int) (bool, error) {
	return true, nil
}

func (q *scriptedQueue) EnqueueKeyed(_ context.Context, _, _, _ string, _ []byte, _ int) (bool, error) {
	return true, nil
}

func (q *scriptedQueue) ClaimNext(context.Context, string, time.Duration) (*runtimeasync.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		return nil, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, nil
}

func (q *scriptedQueue) MarkDone(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.done = append(q.done, jobID)
	return nil
}

func (q *scriptedQueue) Requeue(context.Context, string, time.Duration, string) error {
	return nil
}

func (q *scriptedQueue) Len(context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs), nil
}

func (q *scriptedQueue) RequeueExpiredProcessing(context.Context) (int64, error) {
	return 0, nil
}

var _ runtimeasync.Queue = (*scriptedQueue)(nil)

type telemetryStep struct {
	processed int
	done      bool
}

type scriptedTelemetryRebuilder struct {
	resetCalls    int
	estimateCalls int
	total         int
	steps         []telemetryStep
}

func (s *scriptedTelemetryRebuilder) ResetScope(context.Context, telemetrybridge.Scope, ...telemetrybridge.Family) error {
	s.resetCalls++
	return nil
}

func (s *scriptedTelemetryRebuilder) StepFamilies(context.Context, telemetrybridge.Scope, ...telemetrybridge.Family) (telemetrybridge.StepResult, error) {
	if len(s.steps) == 0 {
		return telemetrybridge.StepResult{Done: true}, nil
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return telemetrybridge.StepResult{Processed: step.processed, Done: step.done}, nil
}

func (s *scriptedTelemetryRebuilder) EstimateFamilies(context.Context, telemetrybridge.Scope, ...telemetrybridge.Family) (int, error) {
	s.estimateCalls++
	return s.total, nil
}
var _ runtimeasync.KeyedEnqueuer = (*scriptedQueue)(nil)
