package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/config"
	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	minidumpfixture "urgentry/internal/testfixtures/minidump"
)

// TestE2E_IngestToRender tests the full flow:
// 1. Start server with SQLite + pipeline
// 2. POST an event to /api/1/store/
// 3. Wait for pipeline to process
// 4. Verify the event appears in the web UI and API
func TestE2E_IngestToRender(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// --- 1. Set up real SQLite and pipeline ---
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	eventStore := sqlite.NewEventStore(db)
	groupStore := sqlite.NewGroupStore(db)
	blobStore := store.NewMemoryBlobStore()
	jobStore := sqlite.NewJobStore(db)
	releaseStore := sqlite.NewReleaseStore(db)
	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	publicKey, err := sqlite.EnsureDefaultKey(context.Background(), db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 "admin@example.com",
		DisplayName:           "Admin",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_e2e_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}

	processor := &issue.Processor{
		Events:   eventStore,
		Groups:   groupStore,
		Blobs:    blobStore,
		Releases: releaseStore,
	}

	pipe := pipeline.New(processor, 100, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobStore, jobStore, 100)

	cfg := config.Config{
		Env:      "test",
		HTTPAddr: ":0",
	}

	handler := NewServer("all", cfg, sqliteServerDeps(t, db, dataDir, keyStore, authStore, pipe, blobStore, nativeCrashes))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// --- 2. POST an event to the store endpoint ---
	eventPayload := `{
		"event_id": "e2edeadbeefdeadbeefdeadbeefdeadb",
		"platform": "go",
		"level": "error",
		"message": "E2E test error message",
		"exception": {
			"values": [{
				"type": "E2ETestError",
				"value": "something went wrong in e2e",
				"stacktrace": {
					"frames": [{
						"filename": "e2e_test.go",
						"function": "TestE2E",
						"lineno": 42,
						"in_app": true
					}]
				}
			}]
		},
		"tags": {
			"environment": "e2e-test",
			"browser": "TestBrowser"
		},
		"user": {
			"id": "e2e-user-1",
			"email": "e2e@example.com"
		}
	}`

	resp, err := http.Post(srv.URL+"/api/default-project/store/?sentry_key="+publicKey, "application/json", strings.NewReader(eventPayload))
	if err != nil {
		t.Fatalf("POST /api/1/store/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/default-project/store/ status = %d, body = %s", resp.StatusCode, body)
	}

	var storeResp map[string]any
	if err := json.Unmarshal(body, &storeResp); err != nil {
		t.Fatalf("decode store response: %v", err)
	}
	if storeResp["id"] == nil || storeResp["id"] == "" {
		t.Fatal("store response missing 'id' field")
	}

	// --- 3. Wait for pipeline to process ---
	var groupID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil {
			t.Fatalf("count events: %v", err)
		}
		if count > 0 {
			// Find the group ID.
			if err := db.QueryRow("SELECT group_id FROM events LIMIT 1").Scan(&groupID); err != nil {
				t.Fatalf("lookup group id: %v", err)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if groupID == "" {
		// Check if any events or groups were created.
		var eventCount, groupCount int
		if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil {
			t.Fatalf("count events after timeout: %v", err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM groups").Scan(&groupCount); err != nil {
			t.Fatalf("count groups after timeout: %v", err)
		}
		t.Fatalf("pipeline did not process event within timeout (events=%d, groups=%d)", eventCount, groupCount)
	}

	// --- 4. Verify the issue appears in the web UI ---
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(srv.URL+"/login/", "application/x-www-form-urlencoded", strings.NewReader("email="+bootstrap.Email+"&password="+bootstrap.Password+"&next=%2Fissues%2F"))
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login/ status = %d, want 303", loginResp.StatusCode)
	}

	issuesResp, err := client.Get(srv.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	issuesBody, _ := io.ReadAll(issuesResp.Body)
	issuesResp.Body.Close()

	if issuesResp.StatusCode != http.StatusOK {
		t.Errorf("GET /issues/ status = %d", issuesResp.StatusCode)
	}
	if !strings.Contains(string(issuesBody), "E2ETestError") && !strings.Contains(string(issuesBody), "e2e") {
		t.Error("issues page should contain the E2E test error")
	}

	// --- 5. Verify the issue detail page renders ---
	detailResp, err := client.Get(srv.URL + "/issues/" + groupID + "/")
	if err != nil {
		t.Fatalf("GET /issues/%s/: %v", groupID, err)
	}
	detailBody, _ := io.ReadAll(detailResp.Body)
	detailResp.Body.Close()

	if detailResp.StatusCode != http.StatusOK {
		t.Errorf("GET /issues/%s/ status = %d", groupID, detailResp.StatusCode)
	}
	if len(detailBody) < 100 {
		t.Error("issue detail page body suspiciously short")
	}

	// --- 6. Verify the event detail page renders ---
	var eventID string
	if err := db.QueryRow("SELECT event_id FROM events WHERE group_id = ?", groupID).Scan(&eventID); err != nil {
		t.Fatalf("lookup event id: %v", err)
	}
	if eventID == "" {
		t.Fatal("could not find event_id for the group")
	}

	eventResp, err := client.Get(srv.URL + "/events/" + eventID + "/")
	if err != nil {
		t.Fatalf("GET /events/%s/: %v", eventID, err)
	}
	eventBody, _ := io.ReadAll(eventResp.Body)
	eventResp.Body.Close()

	if eventResp.StatusCode != http.StatusOK {
		t.Errorf("GET /events/%s/ status = %d", eventID, eventResp.StatusCode)
	}
	if len(eventBody) < 100 {
		t.Error("event detail page body suspiciously short")
	}

	// --- Bonus: verify dashboard also renders ---
	dashResp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	dashResp.Body.Close()
	if dashResp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d", dashResp.StatusCode)
	}

	// --- Bonus: verify healthz still works ---
	healthResp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d", healthResp.StatusCode)
	}
}

// suppress unused import
var _ = sql.ErrNoRows

func TestE2E_MinidumpStagesAndCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	eventStore := sqlite.NewEventStore(db)
	groupStore := sqlite.NewGroupStore(db)
	blobStore := store.NewMemoryBlobStore()
	jobStore := sqlite.NewJobStore(db)
	releaseStore := sqlite.NewReleaseStore(db)
	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	publicKey, err := sqlite.EnsureDefaultKey(context.Background(), db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}

	processor := &issue.Processor{
		Events:   eventStore,
		Groups:   groupStore,
		Blobs:    blobStore,
		Releases: releaseStore,
	}
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobStore, jobStore, 100)
	pipe := pipeline.NewDurable(processor, jobStore, 100, 1)
	pipe.SetNativeJobProcessor(pipeline.NativeJobProcessorFunc(func(ctx context.Context, projectID string, payload []byte) error {
		return nativeCrashes.ProcessStackwalkJob(ctx, processor, projectID, payload)
	}))
	pipe.Start(context.Background())
	defer pipe.Stop()

	cfg := config.Config{
		Env:      "test",
		HTTPAddr: ":0",
	}
	handler := NewServer("all", cfg, sqliteServerDeps(t, db, dataDir, keyStore, authStore, pipe, blobStore, nativeCrashes))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body, contentType := multipartMinidumpRequestE2E(t, "crash.dmp", minidumpfixture.Build(t, 0x1010, 0x1000, 0x200, "App"), map[string]string{
		"sentry": `{"event_id":"minidump-e2e-1","release":"ios@1.2.3","platform":"cocoa","message":"Native crash","debug_meta":{"images":[{"code_file":"App","debug_id":"DEBUG-1","code_id":"CODE-1","instruction_addr":"0x1010","image_addr":"0x1000","arch":"arm64"}]}}`,
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/default-project/minidump/?sentry_key="+publicKey, body)
	if err != nil {
		t.Fatalf("NewRequest minidump: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/default-project/minidump/: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/default-project/minidump/ status = %d, body = %s", resp.StatusCode, respBody)
	}

	var minidumpResp map[string]string
	if err := json.Unmarshal(respBody, &minidumpResp); err != nil {
		t.Fatalf("decode minidump response: %v", err)
	}
	eventID := minidumpResp["id"]
	if eventID == "" {
		t.Fatal("minidump response missing id")
	}

	var finalGroupID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		evt, err := eventStore.GetEvent(context.Background(), "default-project", eventID)
		if err == nil && evt != nil && evt.ProcessingStatus == store.EventProcessingStatusCompleted && strings.TrimSpace(evt.GroupID) != "" {
			finalGroupID = evt.GroupID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalGroupID == "" {
		evt, err := eventStore.GetEvent(context.Background(), "default-project", eventID)
		if err != nil {
			t.Fatalf("GetEvent after timeout: %v", err)
		}
		t.Fatalf("native minidump did not complete within timeout: %+v", evt)
	}

	crash, err := nativeCrashes.GetByEventID(context.Background(), "default-project", eventID)
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if crash == nil || crash.Status != sqlite.NativeCrashStatusCompleted || crash.Attempts < 1 {
		t.Fatalf("unexpected native crash receipt: %+v", crash)
	}
}

func multipartMinidumpRequestE2E(t *testing.T, filename string, payload []byte, fields map[string]string) (io.Reader, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload_file_minidump", filename)
	if err != nil {
		t.Fatalf("create minidump part: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write minidump payload: %v", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close minidump writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}
