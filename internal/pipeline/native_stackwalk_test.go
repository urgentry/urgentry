package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/nativesym"
	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	minidumpfixture "urgentry/internal/testfixtures/minidump"
)

func TestPipelineProcessesNativeStackwalkJob(t *testing.T) {
	ctx := context.Background()
	db := openNativePipelineDB(t)
	seedNativePipelineProject(t, db)

	blobs := store.NewMemoryBlobStore()
	jobs := sqlite.NewJobStore(db)
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.sym",
		UUID:      "DEBUG-1",
		CodeID:    "CODE-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 DEBUG-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}

	nativeCrashes, _, pipe := newNativeStackwalkFixture(db, blobs, jobs)
	pipe.Start(ctx)
	defer pipe.Stop()

	crash, created, err := nativeCrashes.IngestMinidump(ctx, sqlite.MinidumpReceiptInput{
		ProjectID:   "proj-1",
		EventID:     "native-success-1",
		Filename:    "crash.dmp",
		ContentType: "application/x-dmp",
		Dump:        minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App"),
		EventJSON:   []byte(`{"event_id":"native-success-1","release":"ios@1.2.3","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`),
	})
	if err != nil {
		t.Fatalf("IngestMinidump: %v", err)
	}
	if !created || crash == nil {
		t.Fatalf("expected native crash receipt, got created=%v crash=%+v", created, crash)
	}

	waitForNativeCrashStatus(t, nativeCrashes, crash.ID, sqlite.NativeCrashStatusCompleted)

	evt, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", crash.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if evt == nil || evt.ProcessingStatus != store.EventProcessingStatusCompleted || strings.TrimSpace(evt.GroupID) == "" {
		t.Fatalf("unexpected processed native event: %+v", evt)
	}

	var normalized normalize.Event
	if err := json.Unmarshal(evt.NormalizedJSON, &normalized); err != nil {
		t.Fatalf("unmarshal processed event: %v", err)
	}
	frame := normalized.Exception.Values[0].Stacktrace.Frames[0]
	if frame.Filename != "src/AppDelegate.swift" || frame.Function != "main" || frame.Lineno != 42 {
		t.Fatalf("unexpected symbolicated frame: %+v", frame)
	}

	finalCrash, err := nativeCrashes.Get(ctx, crash.ID)
	if err != nil {
		t.Fatalf("Get final native crash: %v", err)
	}
	if finalCrash == nil || finalCrash.Status != sqlite.NativeCrashStatusCompleted || finalCrash.Attempts != 1 {
		t.Fatalf("unexpected final native crash: %+v", finalCrash)
	}
}

func TestPipelineMarksMalformedNativeCrashFailed(t *testing.T) {
	ctx := context.Background()
	db := openNativePipelineDB(t)
	seedNativePipelineProject(t, db)

	blobs := store.NewMemoryBlobStore()
	jobs := sqlite.NewJobStore(db)
	nativeCrashes, _, pipe := newNativeStackwalkFixture(db, blobs, jobs)
	pipe.Start(ctx)
	defer pipe.Stop()

	crash, created, err := nativeCrashes.IngestMinidump(ctx, sqlite.MinidumpReceiptInput{
		ProjectID:   "proj-1",
		EventID:     "native-malformed-1",
		Filename:    "crash.dmp",
		ContentType: "application/x-dmp",
		Dump:        []byte("BROKEN"),
		EventJSON:   []byte(`{"event_id":"native-malformed-1","release":"ios@1.2.3","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`),
	})
	if err != nil {
		t.Fatalf("IngestMinidump: %v", err)
	}
	if !created || crash == nil {
		t.Fatalf("expected native crash receipt, got created=%v crash=%+v", created, crash)
	}

	waitForNativeCrashStatus(t, nativeCrashes, crash.ID, sqlite.NativeCrashStatusFailed)

	evt, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", crash.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if evt == nil || evt.ProcessingStatus != store.EventProcessingStatusFailed || !strings.Contains(evt.IngestError, "malformed minidump payload") {
		t.Fatalf("unexpected failed native event: %+v", evt)
	}
	if strings.TrimSpace(evt.GroupID) != "" {
		t.Fatalf("expected malformed native event to stay ungrouped, got group %q", evt.GroupID)
	}
}

func TestNativeCrashStackwalkRetrySafety(t *testing.T) {
	ctx := context.Background()
	db := openNativePipelineDB(t)
	seedNativePipelineProject(t, db)

	blobs := store.NewMemoryBlobStore()
	jobs := sqlite.NewJobStore(db)
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.sym",
		UUID:      "DEBUG-1",
		CodeID:    "CODE-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 DEBUG-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}

	nativeCrashes, proc, _ := newNativeStackwalkFixture(db, blobs, jobs)
	crash, created, err := nativeCrashes.IngestMinidump(ctx, sqlite.MinidumpReceiptInput{
		ProjectID:   "proj-1",
		EventID:     "native-retry-1",
		Filename:    "crash.dmp",
		ContentType: "application/x-dmp",
		Dump:        minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App"),
		EventJSON:   []byte(`{"event_id":"native-retry-1","release":"ios@1.2.3","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`),
	})
	if err != nil {
		t.Fatalf("IngestMinidump: %v", err)
	}
	if !created || crash == nil {
		t.Fatalf("expected native crash receipt, got created=%v crash=%+v", created, crash)
	}

	firstJob, err := jobs.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext first: %v", err)
	}
	if firstJob == nil {
		t.Fatal("expected native stackwalk job")
	}

	var attachmentKey string
	if err := db.QueryRowContext(ctx, `SELECT raw_blob_key FROM native_crashes WHERE id = ?`, crash.ID).Scan(&attachmentKey); err != nil {
		t.Fatalf("load raw_blob_key: %v", err)
	}
	if err := blobs.Delete(ctx, attachmentKey); err != nil {
		t.Fatalf("Delete raw minidump blob: %v", err)
	}

	err = nativeCrashes.ProcessStackwalkJob(ctx, proc, firstJob.ProjectID, firstJob.Payload)
	if err == nil {
		t.Fatal("expected transient stackwalk error")
	}
	if err := jobs.Requeue(ctx, firstJob.ID, 0, err.Error()); err != nil {
		t.Fatalf("Requeue first job: %v", err)
	}

	midCrash, err := nativeCrashes.Get(ctx, crash.ID)
	if err != nil {
		t.Fatalf("Get mid native crash: %v", err)
	}
	if midCrash == nil || midCrash.Status != sqlite.NativeCrashStatusPending || midCrash.Attempts != 1 {
		t.Fatalf("unexpected retry-pending crash: %+v", midCrash)
	}

	if err := blobs.Put(ctx, attachmentKey, minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App")); err != nil {
		t.Fatalf("restore raw minidump blob: %v", err)
	}
	secondJob, err := jobs.ClaimNext(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatalf("ClaimNext second: %v", err)
	}
	if secondJob == nil {
		t.Fatal("expected requeued native stackwalk job")
	}
	if err := nativeCrashes.ProcessStackwalkJob(ctx, proc, secondJob.ProjectID, secondJob.Payload); err != nil {
		t.Fatalf("ProcessStackwalkJob second: %v", err)
	}
	if err := jobs.MarkDone(ctx, secondJob.ID); err != nil {
		t.Fatalf("MarkDone second: %v", err)
	}

	finalCrash, err := nativeCrashes.Get(ctx, crash.ID)
	if err != nil {
		t.Fatalf("Get final native crash: %v", err)
	}
	if finalCrash == nil || finalCrash.Status != sqlite.NativeCrashStatusCompleted || finalCrash.Attempts != 2 {
		t.Fatalf("unexpected final retry-safe crash: %+v", finalCrash)
	}

	evt, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", crash.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if evt == nil || evt.ProcessingStatus != store.EventProcessingStatusCompleted || strings.TrimSpace(evt.GroupID) == "" {
		t.Fatalf("unexpected retried native event: %+v", evt)
	}
}

func TestNativeCrashRestagesFailedDuplicateDelivery(t *testing.T) {
	ctx := context.Background()
	db := openNativePipelineDB(t)
	seedNativePipelineProject(t, db)

	blobs := store.NewMemoryBlobStore()
	jobs := sqlite.NewJobStore(db)
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	if err := debugFiles.Save(ctx, &sqlite.DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.sym",
		UUID:      "DEBUG-1",
		CodeID:    "CODE-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 DEBUG-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}

	nativeCrashes, _, pipe := newNativeStackwalkFixture(db, blobs, jobs)
	pipe.Start(ctx)
	defer pipe.Stop()

	crash, created, err := nativeCrashes.IngestMinidump(ctx, sqlite.MinidumpReceiptInput{
		ProjectID:   "proj-1",
		EventID:     "native-redelivery-1",
		Filename:    "crash.dmp",
		ContentType: "application/x-dmp",
		Dump:        []byte("BROKEN"),
		EventJSON:   []byte(`{"event_id":"native-redelivery-1","release":"ios@1.2.3","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`),
	})
	if err != nil || !created || crash == nil {
		t.Fatalf("initial malformed ingest = (%v, %v, %+v)", err, created, crash)
	}
	waitForNativeCrashStatus(t, nativeCrashes, crash.ID, sqlite.NativeCrashStatusFailed)

	restaged, created, err := nativeCrashes.IngestMinidump(ctx, sqlite.MinidumpReceiptInput{
		ProjectID:   "proj-1",
		EventID:     "native-redelivery-1",
		Filename:    "crash.dmp",
		ContentType: "application/x-dmp",
		Dump:        minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App"),
		EventJSON:   []byte(`{"event_id":"native-redelivery-1","release":"ios@1.2.3","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`),
	})
	if err != nil {
		t.Fatalf("restage ingest: %v", err)
	}
	if created {
		t.Fatal("expected duplicate delivery to reuse the existing crash receipt")
	}
	if restaged == nil || restaged.ID != crash.ID {
		t.Fatalf("unexpected restaged crash: %+v", restaged)
	}

	waitForNativeCrashStatus(t, nativeCrashes, crash.ID, sqlite.NativeCrashStatusCompleted)

	finalCrash, err := nativeCrashes.Get(ctx, crash.ID)
	if err != nil {
		t.Fatalf("Get final native crash: %v", err)
	}
	if finalCrash == nil || finalCrash.Attempts != 2 || finalCrash.Status != sqlite.NativeCrashStatusCompleted {
		t.Fatalf("unexpected final native crash: %+v", finalCrash)
	}

	evt, err := sqlite.NewEventStore(db).GetEvent(ctx, "proj-1", crash.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if evt == nil || evt.ProcessingStatus != store.EventProcessingStatusCompleted || strings.TrimSpace(evt.GroupID) == "" {
		t.Fatalf("unexpected restaged native event: %+v", evt)
	}

	var crashCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_crashes WHERE project_id = ? AND event_id = ?`, "proj-1", crash.EventID).Scan(&crashCount); err != nil {
		t.Fatalf("count native crashes: %v", err)
	}
	if crashCount != 1 {
		t.Fatalf("native crash rows = %d, want 1", crashCount)
	}
}

func TestPipelineRequeuesNativeStackwalkJobWithoutProcessor(t *testing.T) {
	ctx := context.Background()
	db := openNativePipelineDB(t)
	seedNativePipelineProject(t, db)
	jobs := sqlite.NewJobStore(db)

	payload, err := json.Marshal(map[string]string{"crashId": "missing-processor-crash"})
	if err != nil {
		t.Fatalf("marshal job payload: %v", err)
	}
	ok, err := jobs.Enqueue(ctx, sqlite.JobKindNativeStackwalk, "proj-1", payload, 10)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !ok {
		t.Fatal("expected native job to enqueue")
	}

	pipe := NewDurable(&issue.Processor{}, jobs, 10, 1)
	pipe.Start(ctx)
	defer pipe.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var status, lastError string
		var attempts int
		err := db.QueryRowContext(ctx, `SELECT status, attempts, COALESCE(last_error, '') FROM jobs LIMIT 1`).Scan(&status, &attempts, &lastError)
		if err != nil {
			t.Fatalf("load native job row: %v", err)
		}
		if status == "pending" && attempts >= 1 && strings.Contains(lastError, "native job processor is not configured") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("native stackwalk job was not requeued when processor was missing")
}

func openNativePipelineDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedNativePipelineProject(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'mobile', 'Mobile', 'cocoa', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func newNativeStackwalkFixture(db *sql.DB, blobs store.BlobStore, jobs *sqlite.JobStore) (*sqlite.NativeCrashStore, *issue.Processor, *Pipeline) {
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	nativeResolver := nativesym.NewResolver(nativePipelineDebugLookup{store: debugFiles})
	proc := &issue.Processor{
		Events:   sqlite.NewEventStore(db),
		Groups:   sqlite.NewGroupStore(db),
		Blobs:    blobs,
		Releases: sqlite.NewReleaseStore(db),
		Native:   nativeResolver,
	}
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, jobs, 10)
	pipe := NewDurable(proc, jobs, 10, 1)
	pipe.SetNativeJobProcessor(NativeJobProcessorFunc(func(ctx context.Context, projectID string, payload []byte) error {
		return nativeCrashes.ProcessStackwalkJob(ctx, proc, projectID, payload)
	}))
	return nativeCrashes, proc, pipe
}

func waitForNativeCrashStatus(t *testing.T, crashes *sqlite.NativeCrashStore, crashID, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, err := crashes.Get(context.Background(), crashID)
		if err != nil {
			t.Fatalf("Get native crash: %v", err)
		}
		if item != nil && item.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	item, err := crashes.Get(context.Background(), crashID)
	if err != nil {
		t.Fatalf("Get native crash timeout: %v", err)
	}
	t.Fatalf("native crash status did not reach %q: %+v", status, item)
}

type nativePipelineDebugLookup struct {
	store *sqlite.DebugFileStore
}

func (n nativePipelineDebugLookup) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*nativesym.File, []byte, error) {
	item, body, err := n.store.LookupByDebugID(ctx, projectID, releaseVersion, kind, debugID)
	if err != nil || item == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: item.ID, CodeID: item.CodeID, Kind: item.Kind}, body, nil
}

func (n nativePipelineDebugLookup) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*nativesym.File, []byte, error) {
	item, body, err := n.store.LookupByCodeID(ctx, projectID, releaseVersion, kind, codeID)
	if err != nil || item == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: item.ID, CodeID: item.CodeID, Kind: item.Kind}, body, nil
}
