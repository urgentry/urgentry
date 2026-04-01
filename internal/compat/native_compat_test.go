//go:build integration

package compat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"urgentry/internal/normalize"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	nativefixture "urgentry/internal/testfixtures/nativecrash"
)

type nativeCompatDebugFile struct {
	ID              string `json:"id"`
	ReprocessStatus string `json:"reprocessStatus"`
}

type nativeCompatRun struct {
	ID string `json:"id"`
}

const (
	compatOrgSlug  = "urgentry-org"
	compatProjSlug = "default"
)

func TestNativeMinidumpAppleCompat(t *testing.T) {
	runNativeMinidumpCompat(t, "protocol", "native_minidump_apple", nativefixture.ByName(t, "apple_multimodule"), strings.Repeat("1", 32), true)
}

func TestNativeMinidumpLinuxCompat(t *testing.T) {
	runNativeMinidumpCompat(t, "protocol", "native_minidump_linux", nativefixture.ByName(t, "linux_elf"), strings.Repeat("2", 32), true)
}

func TestNativeMinidumpFallbackCompat(t *testing.T) {
	runNativeMinidumpCompat(t, "protocol", "native_minidump_fallback", nativefixture.ByName(t, "fallback_module_only"), strings.Repeat("3", 32), false)
}

func TestNativeReprocessRoundtrip(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	fixture := nativefixture.ByName(t, "apple_multimodule")
	eventID := strings.Repeat("4", 32)
	postCompatMinidump(t, srv, fixture, eventID)

	before := waitForNativeEventStatus(t, srv.db, "default-project", eventID, sharedstore.EventProcessingStatusCompleted)
	beforeFrames := nativefixture.SnapshotFrames(t, before.NormalizedJSON)
	writeHarnessArtifact(t, "migration", "native_reprocess_roundtrip", "before_frames", beforeFrames)

	resolved, unresolved := normalize.CountNativeFrames(string(before.NormalizedJSON))
	if resolved != 0 || unresolved != 2 {
		t.Fatalf("unexpected pre-reprocess native counts: resolved=%d unresolved=%d", resolved, unresolved)
	}

	uploaded := uploadNativeSymbols(t, srv, fixture)
	if len(uploaded) != 1 || uploaded[0].ID == "" {
		t.Fatalf("unexpected uploaded debug files: %+v", uploaded)
	}

	reprocessResp := apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/projects/"+compatOrgSlug+"/"+compatProjSlug+"/releases/"+fixture.Release+"/debug-files/"+uploaded[0].ID+"/reprocess/", srv.pat, nil, "")
	if reprocessResp.StatusCode != http.StatusAccepted {
		t.Fatalf("native reprocess status = %d, want 202", reprocessResp.StatusCode)
	}
	var run nativeCompatRun
	if err := json.NewDecoder(reprocessResp.Body).Decode(&run); err != nil {
		t.Fatalf("decode reprocess run: %v", err)
	}
	reprocessResp.Body.Close()
	if run.ID == "" {
		t.Fatal("native reprocess response missing run id")
	}

	waitForBackfillRunCompletion(t, srv, run.ID)

	after := waitForNativeCounts(t, srv.db, "default-project", eventID, fixture.WantResolvedFrames, fixture.WantUnresolved)
	assertNativeFixtureSnapshot(t, srv.db, "migration", "native_reprocess_roundtrip", fixture, after)

	listResp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/"+compatOrgSlug+"/"+compatProjSlug+"/releases/"+fixture.Release+"/debug-files/", srv.pat, nil, "")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list debug files status = %d, want 200", listResp.StatusCode)
	}
	var listed []nativeCompatDebugFile
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode listed debug files: %v", err)
	}
	listResp.Body.Close()
	if len(listed) != 1 || listed[0].ReprocessStatus != "completed" {
		t.Fatalf("unexpected debug-file reprocess state: %+v", listed)
	}
}

func runNativeMinidumpCompat(t *testing.T, dimension, name string, fixture nativefixture.Fixture, eventID string, uploadSymbols bool) {
	t.Helper()

	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	if uploadSymbols {
		uploadNativeSymbols(t, srv, fixture)
	}
	postCompatMinidump(t, srv, fixture, eventID)

	evt := waitForNativeEventStatus(t, srv.db, "default-project", eventID, sharedstore.EventProcessingStatusCompleted)
	assertNativeFixtureSnapshot(t, srv.db, dimension, name, fixture, evt)
}

func uploadNativeSymbols(t *testing.T, srv *compatServer, fixture nativefixture.Fixture) []nativeCompatDebugFile {
	t.Helper()

	items := make([]nativeCompatDebugFile, 0, len(fixture.Symbols))
	for _, symbol := range fixture.Symbols {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", symbol.Name)
		if err != nil {
			t.Fatalf("create native symbol form file: %v", err)
		}
		if _, err := part.Write(symbol.Body); err != nil {
			t.Fatalf("write native symbol body: %v", err)
		}
		if err := writer.WriteField("kind", symbol.Kind); err != nil {
			t.Fatalf("write native symbol kind: %v", err)
		}
		if symbol.DebugID != "" {
			if err := writer.WriteField("debug_id", symbol.DebugID); err != nil {
				t.Fatalf("write native symbol debug_id: %v", err)
			}
		}
		if symbol.CodeID != "" {
			if err := writer.WriteField("code_id", symbol.CodeID); err != nil {
				t.Fatalf("write native symbol code_id: %v", err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close native symbol writer: %v", err)
		}

		resp := apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/projects/"+compatOrgSlug+"/"+compatProjSlug+"/releases/"+fixture.Release+"/debug-files/", srv.pat, &body, writer.FormDataContentType())
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload native symbol status = %d, want 201", resp.StatusCode)
		}
		var uploaded nativeCompatDebugFile
		if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
			t.Fatalf("decode uploaded debug file: %v", err)
		}
		resp.Body.Close()
		items = append(items, uploaded)
	}
	return items
}

func postCompatMinidump(t *testing.T, srv *compatServer, fixture nativefixture.Fixture, eventID string) string {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload_file_minidump", fixture.DumpFilename)
	if err != nil {
		t.Fatalf("create minidump form file: %v", err)
	}
	if _, err := part.Write(fixture.Dump); err != nil {
		t.Fatalf("write minidump fixture: %v", err)
	}
	if err := writer.WriteField("sentry", string(fixture.EventJSON(t, eventID))); err != nil {
		t.Fatalf("write sentry payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close minidump writer: %v", err)
	}

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/minidump/?sentry_key="+srv.projectKey, &body, map[string]string{
		"Content-Type": writer.FormDataContentType(),
	})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("native minidump status = %d body=%s", resp.StatusCode, body)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode native minidump response: %v", err)
	}
	resp.Body.Close()
	if result["id"] == "" {
		t.Fatal("native minidump response missing id")
	}
	return result["id"]
}

func waitForNativeEventStatus(t *testing.T, db *sql.DB, projectID, eventID string, want sharedstore.EventProcessingStatus) *sharedstore.StoredEvent {
	t.Helper()

	store := sqlite.NewEventStore(db)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		evt, err := store.GetEvent(context.Background(), projectID, eventID)
		if err == nil && evt != nil && evt.ProcessingStatus == want {
			return evt
		}
		time.Sleep(25 * time.Millisecond)
	}
	evt, err := store.GetEvent(context.Background(), projectID, eventID)
	if err != nil {
		t.Fatalf("load native event after timeout: %v", err)
	}
	t.Fatalf("native event %s did not reach %s: %+v", eventID, want, evt)
	return nil
}

func waitForNativeCounts(t *testing.T, db *sql.DB, projectID, eventID string, wantResolved, wantUnresolved int) *sharedstore.StoredEvent {
	t.Helper()

	store := sqlite.NewEventStore(db)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		evt, err := store.GetEvent(context.Background(), projectID, eventID)
		if err == nil && evt != nil {
			resolved, unresolved := normalize.CountNativeFrames(string(evt.NormalizedJSON))
			if resolved == wantResolved && unresolved == wantUnresolved {
				return evt
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	evt, err := store.GetEvent(context.Background(), projectID, eventID)
	if err != nil {
		t.Fatalf("load native event after timeout: %v", err)
	}
	t.Fatalf("native event %s did not reach resolved=%d unresolved=%d: %+v", eventID, wantResolved, wantUnresolved, evt)
	return nil
}

func waitForBackfillRunCompletion(t *testing.T, srv *compatServer, runID string) {
	t.Helper()

	store := sqlite.NewBackfillStore(srv.db)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.GetRun(context.Background(), "default-org", runID)
		if err != nil {
			t.Fatalf("get backfill run: %v", err)
		}
		if run != nil {
			writeHarnessArtifact(t, "migration", "native_reprocess_roundtrip", "run_state", run)
			switch run.Status {
			case sqlite.BackfillStatusCompleted:
				return
			case sqlite.BackfillStatusFailed, sqlite.BackfillStatusCancelled:
				t.Fatalf("native reprocess run ended as %s: %+v", run.Status, run)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	run, err := store.GetRun(context.Background(), "default-org", runID)
	if err != nil {
		t.Fatalf("get backfill run after timeout: %v", err)
	}
	t.Fatalf("native reprocess run %s did not finish: %+v", runID, run)
}

func assertNativeFixtureSnapshot(t *testing.T, db *sql.DB, dimension, name string, fixture nativefixture.Fixture, evt *sharedstore.StoredEvent) {
	t.Helper()

	frames := nativefixture.SnapshotFrames(t, evt.NormalizedJSON)
	crash := waitForNativeCrashStatus(t, db, evt.ProjectID, evt.EventID, sqlite.NativeCrashStatusCompleted)
	writeHarnessArtifact(t, dimension, name, "frames", frames)
	writeHarnessArtifact(t, dimension, name, "event", map[string]any{
		"event_id":          evt.EventID,
		"processing_status": evt.ProcessingStatus,
		"ingest_error":      evt.IngestError,
	})
	writeHarnessArtifact(t, dimension, name, "native_crash", map[string]any{
		"crash_id":          crash.ID,
		"status":            crash.Status,
		"ingest_error":      crash.IngestError,
		"attempts":          crash.Attempts,
		"processed_at":      crash.ProcessedAt,
		"raw_attachment_id": crash.RawAttachmentID,
	})

	want := nativefixture.LoadGoldenFrames(t, fixture.GoldenFrames)
	if !reflect.DeepEqual(want, frames) {
		t.Fatalf("native frame snapshot mismatch for %s\nwant:\n%s\ngot:\n%s", fixture.Name, nativefixture.MarshalFrames(t, want), nativefixture.MarshalFrames(t, frames))
	}
	resolved, unresolved := normalize.CountNativeFrames(string(evt.NormalizedJSON))
	if resolved != fixture.WantResolvedFrames || unresolved != fixture.WantUnresolved {
		t.Fatalf("unexpected native frame counts for %s: resolved=%d unresolved=%d", fixture.Name, resolved, unresolved)
	}
	if evt.ProcessingStatus != sharedstore.EventProcessingStatusCompleted {
		t.Fatalf("native event status = %s, want completed", evt.ProcessingStatus)
	}
	if crash.Status != sqlite.NativeCrashStatusCompleted {
		t.Fatalf("native crash status = %s, want completed", crash.Status)
	}
	if strings.TrimSpace(crash.IngestError) != "" {
		t.Fatalf("native crash ingest_error = %q, want empty", crash.IngestError)
	}
	if crash.Attempts < 1 {
		t.Fatalf("native crash attempts = %d, want at least 1", crash.Attempts)
	}
	if strings.TrimSpace(crash.RawAttachmentID) == "" {
		t.Fatal("native crash missing raw attachment")
	}
	if crash.ProcessedAt.IsZero() {
		t.Fatal("native crash missing processed_at")
	}
}

func waitForNativeCrashStatus(t *testing.T, db *sql.DB, projectID, eventID, want string) *sqlite.NativeCrash {
	t.Helper()

	store := sqlite.NewNativeCrashStore(db, sharedstore.NewMemoryBlobStore(), sqlite.NewJobStore(db), 1)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		crash, err := store.GetByEventID(context.Background(), projectID, eventID)
		if err == nil && crash != nil && crash.Status == want {
			return crash
		}
		time.Sleep(25 * time.Millisecond)
	}
	crash, err := store.GetByEventID(context.Background(), projectID, eventID)
	if err != nil {
		t.Fatalf("load native crash after timeout: %v", err)
	}
	t.Fatalf("native crash %s did not reach %s: %+v", eventID, want, crash)
	return nil
}

func writeHarnessArtifact(t *testing.T, dimension, name, label string, payload any) {
	t.Helper()

	resultsDir := strings.TrimSpace(os.Getenv("RESULTS_DIR"))
	if resultsDir == "" {
		return
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("mkdir results dir: %v", err)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal harness artifact %s.%s.%s: %v", dimension, name, label, err)
	}
	path := filepath.Join(resultsDir, fmt.Sprintf("%s.%s.%s.json", dimension, name, label))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write harness artifact %s: %v", path, err)
	}
}
