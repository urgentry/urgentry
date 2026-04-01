package ingest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	minidumpfixture "urgentry/internal/testfixtures/minidump"
)

func TestMinidumpHandlerStoresAttachmentAndQueuesSyntheticEvent(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 10)
	handler := MinidumpHandlerWithDeps(IngestDeps{
		NativeCrashes: nativeCrashes,
	})

	body, contentType := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"sentry": `{"event_id":"11111111111111111111111111111111","release":"ios@1.2.3","environment":"production","platform":"cocoa","message":"startup crash"}`,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, data)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	eventID := result["id"]
	if eventID != "11111111111111111111111111111111" {
		t.Fatalf("event id = %q, want sentry-provided id", eventID)
	}

	items, err := attachments.ListByEvent(t.Context(), eventID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(items) != 1 || items[0].Name != "crash.dmp" {
		t.Fatalf("unexpected attachments: %+v", items)
	}

	kind, queued := queuedJob(t, db)
	if kind != sqlite.JobKindNativeStackwalk {
		t.Fatalf("job kind = %q, want %q", kind, sqlite.JobKindNativeStackwalk)
	}
	var queuedJob struct {
		CrashID string `json:"crashId"`
	}
	if err := json.Unmarshal(queued, &queuedJob); err != nil {
		t.Fatalf("decode queued job: %v", err)
	}
	crash, err := nativeCrashes.GetByEventID(t.Context(), "1", eventID)
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if crash == nil || crash.ID == "" || crash.ID != queuedJob.CrashID || crash.Status != sqlite.NativeCrashStatusPending {
		t.Fatalf("unexpected native crash receipt: %+v queued=%+v", crash, queuedJob)
	}
	receipt, err := sqlite.NewEventStore(db).GetEvent(t.Context(), "1", eventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if receipt == nil || receipt.ProcessingStatus != store.EventProcessingStatusPending {
		t.Fatalf("unexpected pending native receipt: %+v", receipt)
	}
}

func TestMinidumpHandlerBuildsNativeFrameHints(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	handler := MinidumpHandlerWithDeps(IngestDeps{
		NativeCrashes: sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 10),
	})

	body, contentType := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"release":          "ios@1.2.3",
		"platform":         "cocoa",
		"debug_id":         "UUID-APPLE-1",
		"code_id":          "CODE-APPLE-1",
		"instruction_addr": "0x1010",
		"module":           "App",
		"function":         "main",
		"filename":         "AppDelegate.swift",
		"lineno":           "42",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	stored, err := sqlite.NewEventStore(db).GetEvent(t.Context(), "1", result["id"])
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if stored == nil {
		t.Fatal("expected pending native receipt")
	}
	var queued map[string]any
	if err := json.Unmarshal(stored.NormalizedJSON, &queued); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	exception := queued["exception"].(map[string]any)
	values := exception["values"].([]any)
	frame := values[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)[0].(map[string]any)
	if frame["debug_id"] != "UUID-APPLE-1" || frame["instruction_addr"] != "0x1010" || frame["filename"] != "AppDelegate.swift" {
		t.Fatalf("unexpected native frame hints: %+v", frame)
	}
	tags := queued["tags"].(map[string]any)
	if tags["minidump.debug_id"] != "UUID-APPLE-1" || tags["minidump.code_id"] != "CODE-APPLE-1" {
		t.Fatalf("unexpected minidump tags: %+v", tags)
	}
}

func TestMinidumpHandlerPersistsNativeCrashImages(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	handler := MinidumpHandlerWithDeps(IngestDeps{
		NativeCrashes: sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 10),
	})

	body, contentType := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"sentry": `{"event_id":"33333333333333333333333333333333","release":"ios@1.2.3","platform":"cocoa","debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","image_addr":"0x1000","image_size":"4096","arch":"arm64"},{"code_file":"UIKit","debug_id":"DEBUG-2","code_id":"CODE-2","image_addr":"0x2000","image_size":"8192","arch":"arm64"}]}}`,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	images, err := debugFiles.ListNativeCrashImages(t.Context(), "1", "33333333333333333333333333333333")
	if err != nil {
		t.Fatalf("ListNativeCrashImages: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("native crash images = %+v", images)
	}
	if images[0].Position != 0 || images[1].Position != 1 {
		t.Fatalf("unexpected native crash image positions: %+v", images)
	}
	if images[0].DebugID != "debug-1" || images[1].CodeID != "code-2" {
		t.Fatalf("unexpected native crash image catalog: %+v", images)
	}
}

func TestMinidumpHandlerPersistsDumpDerivedNativeCrashImages(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	handler := MinidumpHandlerWithDeps(IngestDeps{
		NativeCrashes: sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 10),
	})

	body, contentType := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"sentry": `{"event_id":"dddddddddddddddddddddddddddddddd","release":"ios@1.2.3","platform":"cocoa"}`,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	images, err := debugFiles.ListNativeCrashImages(t.Context(), "1", "dddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatalf("ListNativeCrashImages: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("native crash images = %+v", images)
	}
	if images[0].ModuleName != "app" || images[0].ImageAddr != "0x1000" || images[0].InstructionAddr != "0x1010" || images[0].Source != "minidump" {
		t.Fatalf("unexpected dump-derived native crash image: %+v", images[0])
	}
}

func TestMinidumpHandlerRejectsMissingUpload(t *testing.T) {
	handler := MinidumpHandlerWithDeps(IngestDeps{})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("sentry", `{"event_id":"22222222222222222222222222222222"}`); err != nil {
		t.Fatalf("write sentry field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMinidumpHandlerReturns503WhenQueueFull(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	handler := MinidumpHandlerWithDeps(IngestDeps{NativeCrashes: sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 1)})

	body1, ct1 := multipartMinidumpRequest(t, "first.dmp", validMinidumpPayload(t), nil)
	req1 := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body1)
	req1.Header.Set("Content-Type", ct1)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec1.Code)
	}

	body2, ct2 := multipartMinidumpRequest(t, "second.dmp", validMinidumpPayload(t), nil)
	req2 := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body2)
	req2.Header.Set("Content-Type", ct2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", rec2.Code)
	}
}

func TestMinidumpHandlerDeduplicatesDuplicateDelivery(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, sqlite.NewJobStore(db), 10)
	handler := MinidumpHandlerWithDeps(IngestDeps{NativeCrashes: nativeCrashes})

	body1, ct1 := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"sentry": `{"event_id":"44444444444444444444444444444444","release":"ios@1.2.3","platform":"cocoa"}`,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body1)
	req1.Header.Set("Content-Type", ct1)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec1.Code)
	}

	body2, ct2 := multipartMinidumpRequest(t, "crash.dmp", validMinidumpPayload(t), map[string]string{
		"sentry": `{"event_id":"44444444444444444444444444444444","release":"ios@1.2.3","platform":"cocoa"}`,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", body2)
	req2.Header.Set("Content-Type", ct2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", rec2.Code)
	}

	crash, err := nativeCrashes.GetByEventID(t.Context(), "1", "44444444444444444444444444444444")
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if crash == nil {
		t.Fatal("expected native crash receipt")
	}

	var crashCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM native_crashes WHERE project_id = ? AND event_id = ?`, "1", "44444444444444444444444444444444").Scan(&crashCount); err != nil {
		t.Fatalf("count native crashes: %v", err)
	}
	if crashCount != 1 {
		t.Fatalf("native crash rows = %d, want 1", crashCount)
	}

	var jobCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ? AND project_id = ?`, sqlite.JobKindNativeStackwalk, "1").Scan(&jobCount); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("native stackwalk jobs = %d, want 1", jobCount)
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND event_id = ?`, "1", "44444444444444444444444444444444").Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event receipt rows = %d, want 1", eventCount)
	}

	items, err := sqlite.NewAttachmentStore(db, blobs).ListByEvent(t.Context(), "44444444444444444444444444444444")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("attachments = %d, want 1", len(items))
	}
}

func multipartMinidumpRequest(t *testing.T, filename string, payload []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload_file_minidump", filename)
	if err != nil {
		t.Fatalf("create minidump part: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write minidump part: %v", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func queuedJob(t *testing.T, db *sql.DB) (string, []byte) {
	t.Helper()

	var kind string
	var payload []byte
	if err := db.QueryRow(`SELECT kind, payload FROM jobs ORDER BY created_at DESC LIMIT 1`).Scan(&kind, &payload); err != nil {
		t.Fatalf("load queued payload: %v", err)
	}
	return kind, payload
}

func validMinidumpPayload(t *testing.T) []byte {
	t.Helper()
	return minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App")
}
