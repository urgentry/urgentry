package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"urgentry/internal/normalize"
	"urgentry/internal/proguard"
	"urgentry/internal/store"
)

func TestDebugFileStoreSaveListAndLookup(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	blobs := store.NewMemoryBlobStore()
	debugFiles := NewDebugFileStore(db, blobs)
	proguards := NewProGuardStore(db, blobs)
	ctx := context.Background()

	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID:   "proj-1",
		ReleaseID:   "ios@1.2.3",
		Kind:        "macho",
		Name:        "App.dSYM",
		UUID:        "debug-1",
		CodeID:      "code-1",
		ContentType: "application/octet-stream",
		CreatedAt:   time.Now().UTC(),
	}, []byte("native symbols")); err != nil {
		t.Fatalf("Save native debug file: %v", err)
	}
	if err := proguards.SaveMapping(ctx, &proguard.Mapping{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		UUID:      "proguard-1",
		Name:      "mapping.txt",
		CreatedAt: time.Now().UTC(),
	}, []byte("a -> b")); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	files, err := debugFiles.ListByRelease(ctx, "proj-1", "ios@1.2.3", "")
	if err != nil {
		t.Fatalf("ListByRelease: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("ListByRelease len = %d, want 1", len(files))
	}
	if files[0].Kind != "macho" {
		t.Fatalf("Kind = %q, want macho", files[0].Kind)
	}

	got, body, err := debugFiles.LookupByDebugID(ctx, "proj-1", "ios@1.2.3", "", "debug-1")
	if err != nil {
		t.Fatalf("LookupByDebugID: %v", err)
	}
	if got == nil || got.CodeID != "code-1" {
		t.Fatalf("LookupByDebugID = %+v, want code-1", got)
	}
	if string(body) != "native symbols" {
		t.Fatalf("body = %q, want native symbols", body)
	}
	sources, err := debugFiles.ListNativeSymbolSourcesByRelease(ctx, "proj-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ListNativeSymbolSourcesByRelease: %v", err)
	}
	if len(sources) != 1 || sources[0].DebugID != "debug-1" || sources[0].CodeID != "code-1" {
		t.Fatalf("unexpected native symbol sources: %+v", sources)
	}

	var releaseCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM releases WHERE organization_id = 'org-1' AND version = 'ios@1.2.3'`).Scan(&releaseCount); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releaseCount != 1 {
		t.Fatalf("release count = %d, want 1", releaseCount)
	}
}

func TestDebugFileStoreLookupNativeSymbolSourcePrecedence(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	ctx := context.Background()
	debugFiles := NewDebugFileStore(db, store.NewMemoryBlobStore())

	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "WeakModule.sym",
		UUID:      "",
		CodeID:    "",
		CreatedAt: time.Date(2026, time.March, 29, 9, 0, 0, 0, time.UTC),
	}, []byte("MODULE mac arm64 weak-debug App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 weak\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save weak module: %v", err)
	}
	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "StrongDebug.sym",
		UUID:      "debug-strong",
		CodeID:    "code-strong",
		CreatedAt: time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC),
	}, []byte("MODULE mac arm64 debug-strong App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save strong module: %v", err)
	}

	strong, file, _, err := debugFiles.LookupNativeSymbolSource(ctx, "proj-1", "ios@1.2.3", "macho", NativeLookupInput{
		DebugID:    "debug-strong",
		ModuleName: "app",
	})
	if err != nil {
		t.Fatalf("LookupNativeSymbolSource strong: %v", err)
	}
	if strong == nil || strong.MatchedBy != "debug_id" || file == nil || file.UUID != "debug-strong" {
		t.Fatalf("unexpected strong lookup: source=%+v file=%+v", strong, file)
	}

	codeOnly, file, _, err := debugFiles.LookupNativeSymbolSource(ctx, "proj-1", "ios@1.2.3", "macho", NativeLookupInput{
		CodeID:     "code-strong",
		ModuleName: "app",
	})
	if err != nil {
		t.Fatalf("LookupNativeSymbolSource code: %v", err)
	}
	if codeOnly == nil || codeOnly.MatchedBy != "code_id" || file == nil || file.CodeID != "code-strong" {
		t.Fatalf("unexpected code lookup: source=%+v file=%+v", codeOnly, file)
	}

	moduleOnly, file, _, err := debugFiles.LookupNativeSymbolSource(ctx, "proj-1", "ios@1.2.3", "macho", NativeLookupInput{
		ModuleName:   "app",
		Architecture: "arm64",
		Platform:     "mac",
	})
	if err != nil {
		t.Fatalf("LookupNativeSymbolSource module: %v", err)
	}
	if moduleOnly == nil || moduleOnly.MatchedBy != "module_name" || file == nil || file.UUID != "debug-strong" {
		t.Fatalf("unexpected module fallback lookup: source=%+v file=%+v", moduleOnly, file)
	}
}

func TestDebugFileStoreLookupPrefersNewestDuplicateUpload(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	ctx := context.Background()
	debugFiles := NewDebugFileStore(db, store.NewMemoryBlobStore())

	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App-v1.sym",
		UUID:      "debug-dup",
		CodeID:    "code-dup",
		CreatedAt: time.Date(2026, time.March, 29, 9, 0, 0, 0, time.UTC),
	}, []byte("MODULE mac arm64 debug-dup App\nFILE 0 src/Old.swift\nFUNC 1010 10 0 old\n1010 10 11 0\n")); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	if err := debugFiles.Save(ctx, &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App-v2.sym",
		UUID:      "debug-dup",
		CodeID:    "code-dup",
		CreatedAt: time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC),
	}, []byte("MODULE mac arm64 debug-dup App\nFILE 0 src/New.swift\nFUNC 1010 10 0 newer\n1010 10 22 0\n")); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	sources, err := debugFiles.ListNativeSymbolSourcesByRelease(ctx, "proj-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ListNativeSymbolSourcesByRelease: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("native source count = %d, want 2", len(sources))
	}

	source, file, body, err := debugFiles.LookupNativeSymbolSource(ctx, "proj-1", "ios@1.2.3", "macho", NativeLookupInput{
		DebugID:    "debug-dup",
		CodeID:     "code-dup",
		ModuleName: "app",
	})
	if err != nil {
		t.Fatalf("LookupNativeSymbolSource duplicate: %v", err)
	}
	if source == nil || source.MatchedBy != "debug_id" {
		t.Fatalf("unexpected duplicate source match: %+v", source)
	}
	if file == nil || file.Name != "App-v2.sym" {
		t.Fatalf("duplicate lookup file = %+v, want App-v2.sym", file)
	}
	if string(body) == "" || string(body) != "MODULE mac arm64 debug-dup App\nFILE 0 src/New.swift\nFUNC 1010 10 0 newer\n1010 10 22 0\n" {
		t.Fatalf("duplicate lookup body = %q", string(body))
	}
}

func TestDebugFileStoreReprocessNativeEvents(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	ctx := context.Background()
	debugFiles := NewDebugFileStore(db, store.NewMemoryBlobStore())
	if err := debugFiles.Save(ctx, &DebugFile{
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

	payload := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","release":"ios@1.2.3","environment":"production","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"exception":{"values":[{"type":"Minidump","value":"Native crash","stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1"}]}}]}}`
	if err := NewEventStore(db).SaveEvent(ctx, &store.StoredEvent{
		ID:             "evt-native-1",
		ProjectID:      "proj-1",
		EventID:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		GroupID:        "group-native-1",
		ReleaseID:      "ios@1.2.3",
		Environment:    "production",
		Platform:       "cocoa",
		Level:          "fatal",
		EventType:      "error",
		OccurredAt:     time.Now().UTC(),
		IngestedAt:     time.Now().UTC(),
		Message:        "Native crash",
		Title:          "Native crash",
		Culprit:        "Native crash",
		Tags:           map[string]string{"ingest.kind": "minidump"},
		NormalizedJSON: json.RawMessage(payload),
	}); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	updated, err := debugFiles.ReprocessNativeEvents(ctx, "proj-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ReprocessNativeEvents: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	item, err := NewEventStore(db).GetEvent(ctx, "proj-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if item == nil {
		t.Fatal("GetEvent returned nil")
	}
	var evt normalize.Event
	if err := json.Unmarshal(item.NormalizedJSON, &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.Exception == nil || len(evt.Exception.Values) == 0 || evt.Exception.Values[0].Stacktrace == nil || len(evt.Exception.Values[0].Stacktrace.Frames) == 0 {
		t.Fatalf("missing symbolicated stacktrace: %+v", evt.Exception)
	}
	frame := evt.Exception.Values[0].Stacktrace.Frames[0]
	if frame.Filename != "src/AppDelegate.swift" || frame.Function != "main" || frame.Lineno != 42 {
		t.Fatalf("unexpected symbolicated frame: %+v", frame)
	}
}

func TestDebugFileStoreRestoresArchivedBlob(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	blobs := store.NewMemoryBlobStore()
	debugFiles := NewDebugFileStore(db, blobs)
	ctx := context.Background()

	item := &DebugFile{
		ProjectID: "proj-1",
		ReleaseID: "ios@1.2.3",
		Kind:      "macho",
		Name:      "App.dSYM",
		UUID:      "debug-restore-1",
		CreatedAt: time.Now().UTC(),
	}
	if err := debugFiles.Save(ctx, item, []byte("symbols")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	archiveKey := telemetryArchiveBlobKey("proj-1", store.TelemetrySurfaceDebugFiles, "debug_file", item.ID)
	body, err := blobs.Get(ctx, item.ObjectKey)
	if err != nil {
		t.Fatalf("Get original blob: %v", err)
	}
	if err := blobs.Put(ctx, archiveKey, body); err != nil {
		t.Fatalf("Put archive blob: %v", err)
	}
	if err := blobs.Delete(ctx, item.ObjectKey); err != nil {
		t.Fatalf("Delete original blob: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_archives (id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES ('debug-archive-1', 'proj-1', 'debug_files', 'debug_file', ?, ?, '{}', ?)`,
		item.ID,
		archiveKey,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert telemetry archive: %v", err)
	}

	got, payload, err := debugFiles.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || string(payload) != "symbols" {
		t.Fatalf("Get restored payload = %q, want symbols", payload)
	}
	if _, err := blobs.Get(ctx, archiveKey); err == nil {
		t.Fatal("expected archive blob to be removed after restore")
	}
}
