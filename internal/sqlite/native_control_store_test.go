package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestNativeControlStoreReleaseSummaryAndDebugFiles(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")
	blobs := store.NewMemoryBlobStore()
	debugFiles := NewDebugFileStore(db, blobs)
	ctx := context.Background()

	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.dSYM",
		UUID:      "debug-1",
		CodeID:    "code-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, ingested_at, tags_json, payload_json, processing_status, ingest_error)
		 VALUES
			('evt-native-1', 'proj-1', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'grp-1', 'ios@1.2.3', 'production', 'cocoa', 'fatal', 'error', 'Native crash', 'boom', 'App', ?, ?, '{}',
			 '{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","release":"ios@1.2.3","exception":{"values":[{"stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1","filename":"src/AppDelegate.swift","function":"main"}]}}]}}',
			 'completed', '')`,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert native event: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, debug_file_id, cursor_rowid, total_items, processed_items, updated_items, failed_items, requested_via, last_error, created_at, updated_at)
		 VALUES
			('run-native-1', 'native_reprocess', 'completed', 'org-1', 'proj-1', 'ios@1.2.3', '', 0, 1, 1, 1, 0, 'test', '', ?, ?)`,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	control := NewNativeControlStore(db, blobs, nil)
	summary, err := control.ReleaseSummary(ctx, "org-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ReleaseSummary: %v", err)
	}
	if summary.TotalEvents != 1 || summary.ResolvedFrames != 1 || summary.UnresolvedFrames != 0 || summary.LastRunStatus != "completed" {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	files, err := control.ListReleaseDebugFiles(ctx, "org-1", "proj-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ListReleaseDebugFiles: %v", err)
	}
	if len(files) != 1 || files[0].SymbolicationStatus != "ready" {
		t.Fatalf("unexpected debug file status: %+v", files)
	}

	runs, err := control.ListReleaseRuns(ctx, "org-1", "proj-1", "ios@1.2.3", 10)
	if err != nil {
		t.Fatalf("ListReleaseRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-native-1" {
		t.Fatalf("unexpected scoped runs: %+v", runs)
	}
}
