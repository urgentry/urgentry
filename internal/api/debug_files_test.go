package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/normalize"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	fixtures "urgentry/internal/testfixtures/nativesym"
)

func TestDebugFilesUploadListLookup(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	payload := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","release":"ios@1.2.3","environment":"production","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"exception":{"values":[{"type":"Minidump","value":"Native crash","stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1"}]}}]}}`
	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen) VALUES ('group-native-1', 'test-proj-id', 'urgentry-v1', 'group-native-1', 'Native crash', 'Native crash', 'fatal', 'unresolved', ?, ?, 1)`,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := sqlite.NewEventStore(db).SaveEvent(context.Background(), &store.StoredEvent{
		ID:             "evt-native-1",
		ProjectID:      "test-proj-id",
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

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "App.dSYM")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.WriteField("kind", "macho"); err != nil {
		t.Fatalf("WriteField kind: %v", err)
	}
	if err := writer.WriteField("debug_id", "debug-1"); err != nil {
		t.Fatalf("WriteField debug_id: %v", err)
	}
	if err := writer.WriteField("code_id", "code-1"); err != nil {
		t.Fatalf("WriteField code_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST debug file: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}

	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded["symbolicationStatus"] != "ready" {
		t.Fatalf("symbolicationStatus = %v, want ready", uploaded["symbolicationStatus"])
	}

	reprocessed := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/"+uploaded["id"].(string)+"/reprocess/", "gpat_test_admin_token", nil)
	if reprocessed.StatusCode != http.StatusAccepted {
		t.Fatalf("reprocess status = %d, want 202", reprocessed.StatusCode)
	}
	var reprocessResp BackfillRun
	decodeBody(t, reprocessed, &reprocessResp)
	if reprocessResp.ID == "" || reprocessResp.Kind != "native_reprocess" || reprocessResp.Status != "pending" {
		t.Fatalf("unexpected reprocess response: %+v", reprocessResp)
	}
	reprocessedDuplicate := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/"+uploaded["id"].(string)+"/reprocess/", "gpat_test_admin_token", nil)
	if reprocessedDuplicate.StatusCode != http.StatusAccepted {
		t.Fatalf("duplicate reprocess status = %d, want 202", reprocessedDuplicate.StatusCode)
	}
	var duplicateRun BackfillRun
	decodeBody(t, reprocessedDuplicate, &duplicateRun)
	if duplicateRun.ID != reprocessResp.ID {
		t.Fatalf("duplicate reprocess id = %q, want %q", duplicateRun.ID, reprocessResp.ID)
	}

	controller := pipeline.NewBackfillController(sqlite.NewBackfillStore(db), sqlite.NewDebugFileStore(db, blobs), "test-worker")
	advanced, err := controller.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !advanced {
		t.Fatal("expected native reprocess backfill to advance")
	}
	run, err := sqlite.NewBackfillStore(db).GetRun(context.Background(), "test-org-id", reprocessResp.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run == nil || run.Status != sqlite.BackfillStatusCompleted || run.UpdatedItems != 1 {
		t.Fatalf("unexpected completed run: %+v", run)
	}
	polled := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/backfills/"+reprocessResp.ID+"/", "gpat_test_admin_token", nil)
	if polled.StatusCode != http.StatusOK {
		t.Fatalf("poll status = %d, want 200", polled.StatusCode)
	}
	var polledRun BackfillRun
	decodeBody(t, polled, &polledRun)
	if polledRun.Status != "completed" || polledRun.ProcessedItems != 1 || polledRun.UpdatedItems != 1 {
		t.Fatalf("unexpected polled run: %+v", polledRun)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var listed []map[string]any
	decodeBody(t, resp, &listed)
	if len(listed) != 1 {
		t.Fatalf("listed len = %d, want 1", len(listed))
	}
	if listed[0]["kind"] != "macho" {
		t.Fatalf("kind = %v, want macho", listed[0]["kind"])
	}
	if listed[0]["reprocessStatus"] != "completed" {
		t.Fatalf("reprocessStatus = %v, want completed", listed[0]["reprocessStatus"])
	}

	releaseResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/releases/ios@1.2.3/", "gpat_test_admin_token", nil)
	if releaseResp.StatusCode != http.StatusOK {
		t.Fatalf("release status = %d, want 200", releaseResp.StatusCode)
	}
	var release Release
	decodeBody(t, releaseResp, &release)
	if release.NativeEventCount != 1 || release.NativeResolvedFrames == 0 || release.NativeReprocessStatus != "completed" {
		t.Fatalf("unexpected native release summary: %+v", release)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/debug-1/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup status = %d, want 200", resp.StatusCode)
	}
	var lookedUp map[string]any
	decodeBody(t, resp, &lookedUp)
	if lookedUp["codeId"] != "code-1" {
		t.Fatalf("codeId = %v, want code-1", lookedUp["codeId"])
	}
	sources, err := sqlite.NewDebugFileStore(db, blobs).ListNativeSymbolSourcesByRelease(context.Background(), "test-proj-id", "ios@1.2.3")
	if err != nil {
		t.Fatalf("ListNativeSymbolSourcesByRelease: %v", err)
	}
	if len(sources) != 1 || sources[0].DebugID != "debug-1" || sources[0].ModuleName != "app" {
		t.Fatalf("unexpected native symbol catalog: %+v", sources)
	}

	reprocessedEvent, err := sqlite.NewEventStore(db).GetEvent(context.Background(), "test-proj-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if reprocessedEvent == nil {
		t.Fatal("expected reprocessed event")
	}
	var evt normalize.Event
	if err := json.Unmarshal(reprocessedEvent.NormalizedJSON, &evt); err != nil {
		t.Fatalf("unmarshal reprocessed event: %v", err)
	}
	if evt.Exception == nil || len(evt.Exception.Values) == 0 || evt.Exception.Values[0].Stacktrace == nil || len(evt.Exception.Values[0].Stacktrace.Frames) == 0 {
		t.Fatalf("missing symbolicated stacktrace: %+v", evt.Exception)
	}
	frame := evt.Exception.Values[0].Stacktrace.Frames[0]
	if frame.Filename != "src/AppDelegate.swift" || frame.Function != "main" || frame.Lineno != 42 {
		t.Fatalf("unexpected symbolicated frame: %+v", frame)
	}
	activityResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/issues/"+reprocessedEvent.GroupID+"/activity/", "gpat_test_admin_token", nil)
	if activityResp.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d, want 200", activityResp.StatusCode)
	}
	var activity []IssueActivity
	decodeBody(t, activityResp, &activity)
	foundReprocess := false
	for _, item := range activity {
		if item.Kind == "native_reprocess" {
			foundReprocess = true
			break
		}
	}
	if !foundReprocess {
		t.Fatalf("expected native reprocess activity, got %+v", activity)
	}
	operatorLogs, err := sqlite.NewOperatorAuditStore(db).List(context.Background(), "test-org", 10)
	if err != nil {
		t.Fatalf("List() operator logs: %v", err)
	}
	foundReprocess = false
	for _, item := range operatorLogs {
		if item.Action == "native.reprocess.requested" {
			foundReprocess = true
			break
		}
	}
	if !foundReprocess {
		t.Fatalf("expected native reprocess operator ledger entry, got %+v", operatorLogs)
	}

	var releaseCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM releases WHERE organization_id = 'test-org-id' AND version = 'ios@1.2.3'`).Scan(&releaseCount); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releaseCount != 1 {
		t.Fatalf("release count = %d, want 1", releaseCount)
	}
}

func TestDebugFileReprocessRejectsConflictingRun(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	payload := `{"event_id":"cccccccccccccccccccccccccccccccc","release":"ios@1.2.3","environment":"production","platform":"cocoa","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"exception":{"values":[{"type":"Minidump","value":"Native crash","stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1"}]}}]}}`
	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen) VALUES ('group-native-conflict', 'test-proj-id', 'urgentry-v1', 'group-native-conflict', 'Native crash', 'Native crash', 'fatal', 'unresolved', ?, ?, 1)`,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := sqlite.NewEventStore(db).SaveEvent(context.Background(), &store.StoredEvent{
		ID:             "evt-native-conflict",
		ProjectID:      "test-proj-id",
		EventID:        "cccccccccccccccccccccccccccccccc",
		GroupID:        "group-native-conflict",
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

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "App.dSYM")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("MODULE mac arm64 debug-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.WriteField("kind", "macho"); err != nil {
		t.Fatalf("WriteField kind: %v", err)
	}
	if err := writer.WriteField("debug_id", "debug-1"); err != nil {
		t.Fatalf("WriteField debug_id: %v", err)
	}
	if err := writer.WriteField("code_id", "code-1"); err != nil {
		t.Fatalf("WriteField code_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST debug file: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}

	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	if _, err := sqlite.NewBackfillStore(db).CreateRun(context.Background(), sqlite.CreateBackfillRun{
		Kind:           sqlite.BackfillKindNativeReprocess,
		OrganizationID: "test-org-id",
		ProjectID:      "test-proj-id",
		ReleaseVersion: "ios@1.2.3",
		RequestedVia:   "test",
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	reprocess := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/"+uploaded["id"].(string)+"/reprocess/", "gpat_test_admin_token", nil)
	if reprocess.StatusCode != http.StatusConflict {
		t.Fatalf("reprocess status = %d, want 409", reprocess.StatusCode)
	}
}

func TestDebugFilesUploadELFAndReprocess(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	payload := `{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","release":"linux@1.2.3","environment":"production","platform":"native","level":"fatal","message":"Native crash","tags":{"ingest.kind":"minidump"},"exception":{"values":[{"type":"SIGSEGV","value":"boom","stacktrace":{"frames":[{"instruction_addr":"0x1","package":"ELF-CODE-1"}]}}]}}`
	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen) VALUES ('group-elf-1', 'test-proj-id', 'urgentry-v1', 'group-elf-1', 'Native crash', 'Native crash', 'fatal', 'unresolved', ?, ?, 1)`,
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := sqlite.NewEventStore(db).SaveEvent(context.Background(), &store.StoredEvent{
		ID:             "evt-elf-1",
		ProjectID:      "test-proj-id",
		EventID:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		GroupID:        "group-elf-1",
		ReleaseID:      "linux@1.2.3",
		Environment:    "production",
		Platform:       "native",
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

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "server.o")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(fixtures.ELFHandleRequestObject(t)); err != nil {
		t.Fatalf("write ELF file: %v", err)
	}
	if err := writer.WriteField("kind", "elf"); err != nil {
		t.Fatalf("WriteField kind: %v", err)
	}
	if err := writer.WriteField("code_id", "ELF-CODE-1"); err != nil {
		t.Fatalf("WriteField code_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/linux@1.2.3/debug-files/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST debug file: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}

	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded["symbolicationStatus"] != "ready" {
		t.Fatalf("symbolicationStatus = %v, want ready", uploaded["symbolicationStatus"])
	}

	reprocessed := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/releases/linux@1.2.3/debug-files/"+uploaded["id"].(string)+"/reprocess/", "gpat_test_admin_token", nil)
	if reprocessed.StatusCode != http.StatusAccepted {
		t.Fatalf("reprocess status = %d, want 202", reprocessed.StatusCode)
	}
	var reprocessResp BackfillRun
	decodeBody(t, reprocessed, &reprocessResp)

	controller := pipeline.NewBackfillController(sqlite.NewBackfillStore(db), sqlite.NewDebugFileStore(db, blobs), "test-worker")
	advanced, err := controller.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !advanced {
		t.Fatal("expected ELF reprocess backfill to advance")
	}

	reprocessedEvent, err := sqlite.NewEventStore(db).GetEvent(context.Background(), "test-proj-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if reprocessedEvent == nil {
		t.Fatal("expected reprocessed ELF event")
	}
	var evt normalize.Event
	if err := json.Unmarshal(reprocessedEvent.NormalizedJSON, &evt); err != nil {
		t.Fatalf("unmarshal reprocessed event: %v", err)
	}
	frame := evt.Exception.Values[0].Stacktrace.Frames[0]
	if frame.Function != "handle_request" {
		t.Fatalf("unexpected ELF symbolicated frame: %+v", frame)
	}
}

func TestDebugFilesMalformedELFStatus(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "bad.elf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("not-an-elf")); err != nil {
		t.Fatalf("write bad ELF file: %v", err)
	}
	if err := writer.WriteField("kind", "elf"); err != nil {
		t.Fatalf("WriteField kind: %v", err)
	}
	if err := writer.WriteField("code_id", "ELF-BAD-1"); err != nil {
		t.Fatalf("WriteField code_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/linux@9.9.9/debug-files/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST debug file: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}

	var uploaded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded["symbolicationStatus"] != "malformed" {
		t.Fatalf("symbolicationStatus = %v, want malformed", uploaded["symbolicationStatus"])
	}

	listed := authGet(t, ts, "/api/0/projects/test-org/test-project/releases/linux@9.9.9/debug-files/")
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listed.StatusCode)
	}
	var files []map[string]any
	decodeBody(t, listed, &files)
	if len(files) != 1 || files[0]["symbolicationStatus"] != "malformed" {
		t.Fatalf("unexpected listed debug files: %+v", files)
	}
}
