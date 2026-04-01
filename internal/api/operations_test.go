package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestProjectSettingsAndAuditLogAPI(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	getResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/settings/", pat, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200", getResp.StatusCode)
	}
	var project store.ProjectSettings
	decodeBody(t, getResp, &project)
	if project.EventRetentionDays != 90 {
		t.Fatalf("event retention = %d, want 90", project.EventRetentionDays)
	}
	if project.ReplayPolicy.SampleRate != 1 || project.ReplayPolicy.MaxBytes != 10<<20 {
		t.Fatalf("unexpected default replay policy: %+v", project.ReplayPolicy)
	}

	updateResp := authzJSONRequest(t, ts, http.MethodPut, "/api/0/projects/test-org/test-project/settings/", pat, map[string]any{
		"name":                    "Updated Project",
		"platform":                "ios",
		"status":                  "active",
		"eventRetentionDays":      14,
		"attachmentRetentionDays": 7,
		"debugFileRetentionDays":  30,
		"telemetryPolicies": []map[string]any{
			{"surface": "errors", "retentionDays": 14, "storageTier": "delete"},
			{"surface": "replays", "retentionDays": 7, "storageTier": "archive", "archiveRetentionDays": 21},
			{"surface": "debug_files", "retentionDays": 30, "storageTier": "archive", "archiveRetentionDays": 60},
		},
		"replayPolicy": map[string]any{
			"sampleRate":     0.25,
			"maxBytes":       4096,
			"scrubFields":    []string{"email", "token"},
			"scrubSelectors": []string{".secret"},
		},
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update settings status = %d, want 200", updateResp.StatusCode)
	}
	var updated store.ProjectSettings
	decodeBody(t, updateResp, &updated)
	if len(updated.TelemetryPolicies) == 0 {
		t.Fatalf("expected telemetry policies in updated project: %+v", updated)
	}
	foundReplay := false
	for _, policy := range updated.TelemetryPolicies {
		if policy.Surface == "replays" && policy.StorageTier == "archive" && policy.ArchiveRetentionDays == 21 {
			foundReplay = true
		}
	}
	if !foundReplay {
		t.Fatalf("updated settings missing replay archive policy: %+v", updated.TelemetryPolicies)
	}
	if updated.ReplayPolicy.SampleRate != 0.25 || updated.ReplayPolicy.MaxBytes != 4096 || len(updated.ReplayPolicy.ScrubFields) != 2 || len(updated.ReplayPolicy.ScrubSelectors) != 1 {
		t.Fatalf("unexpected replay ingest policy: %+v", updated.ReplayPolicy)
	}

	invalidResp := authzJSONRequest(t, ts, http.MethodPut, "/api/0/projects/test-org/test-project/settings/", pat, map[string]any{
		"name":                    "Updated Project",
		"platform":                "ios",
		"status":                  "active",
		"eventRetentionDays":      14,
		"attachmentRetentionDays": 7,
		"debugFileRetentionDays":  30,
		"replayPolicy": map[string]any{
			"sampleRate": 2,
		},
	})
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid replay policy status = %d, want 400", invalidResp.StatusCode)
	}

	auditResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/audit-logs/", pat, nil)
	if auditResp.StatusCode != http.StatusOK {
		t.Fatalf("audit logs status = %d, want 200", auditResp.StatusCode)
	}
	var logs []AuditLogEntry
	decodeBody(t, auditResp, &logs)
	if len(logs) == 0 {
		t.Fatal("expected at least one audit log entry")
	}
}

func TestReleaseHealthAndDebugFilesAPI(t *testing.T) {
	db := openTestSQLite(t)
	blobStore := store.NewMemoryBlobStore()
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{BlobStore: blobStore})
	defer ts.Close()

	if _, err := db.Exec(`INSERT INTO releases (id, organization_id, version, created_at) VALUES ('rel-1', 'test-org-id', 'ios@1.2.3', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert release: %v", err)
	}
	if err := sqlite.NewReleaseHealthStore(db).SaveSession(context.Background(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@1.2.3",
		Environment: "production",
		DistinctID:  "user-1",
		Status:      "ok",
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save release session: %v", err)
	}

	healthResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/health/", pat, nil)
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("release health status = %d, want 200", healthResp.StatusCode)
	}
	var health ReleaseHealth
	decodeBody(t, healthResp, &health)
	if health.SessionCount != 1 || health.CrashFreeRate != 100 {
		t.Fatalf("unexpected release health: %+v", health)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "App.dSYM.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("debug-bundle")); err != nil {
		t.Fatalf("write debug file: %v", err)
	}
	if err := writer.WriteField("kind", "apple"); err != nil {
		t.Fatalf("WriteField(kind): %v", err)
	}
	if err := writer.WriteField("debug_id", "UUID-APPLE-1"); err != nil {
		t.Fatalf("WriteField(debug_id): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload debug file: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload debug file status = %d, want 201", resp.StatusCode)
	}
	var created DebugFile
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created debug file: %v", err)
	}
	resp.Body.Close()

	listResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/", pat, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list debug files status = %d, want 200", listResp.StatusCode)
	}
	var files []DebugFile
	decodeBody(t, listResp, &files)
	if len(files) != 1 || files[0].Kind != "apple" {
		t.Fatalf("unexpected debug files: %+v", files)
	}

	downloadResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/debug-files/"+created.ID+"/", pat, nil)
	if downloadResp.StatusCode != http.StatusOK {
		t.Fatalf("download debug file status = %d, want 200", downloadResp.StatusCode)
	}
	payload, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		t.Fatalf("read debug file body: %v", err)
	}
	downloadResp.Body.Close()
	if string(payload) != "debug-bundle" {
		t.Fatalf("debug file payload = %q, want %q", string(payload), "debug-bundle")
	}
}
