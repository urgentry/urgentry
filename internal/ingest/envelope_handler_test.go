package ingest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func seedAttachmentProjectForTest(t *testing.T, db *sql.DB, projectID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES (?, 'org-1', ?, 'Project')`, projectID, projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func insertReplayPolicyForTest(t *testing.T, db *sql.DB, projectID string, sampleRate float64, maxBytes int64, scrubFieldsJSON, scrubSelectorsJSON string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO project_replay_configs
			(project_id, sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		sampleRate,
		maxBytes,
		scrubFieldsJSON,
		scrubSelectorsJSON,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert replay policy: %v", err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d for %q", got, want, query)
	}
}

func envelopeFixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "envelopes")
}

func loadEnvelopeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(envelopeFixturesDir(t), name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func TestEnvelopeHandlerValidFixtures(t *testing.T) {
	fixtures := []struct {
		name   string
		wantID string // expected event_id, empty means any non-empty
	}{
		{"single_error.envelope", "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"},
		{"error_with_attachment.envelope", "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"},
		{"user_feedback.envelope", "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"},
		{"multi_item.envelope", "d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"},
		{"with_client_report.envelope", "e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5"},
		{"check_in.envelope", "99999999999999999999999999999999"},
		{"go_sdk_error.envelope", "f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6"},
	}

	handler := EnvelopeHandler(nil)

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			body := loadEnvelopeFixture(t, tc.name)

			req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
			}

			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.wantID != "" && result["id"] != tc.wantID {
				t.Errorf("id = %q, want %q", result["id"], tc.wantID)
			}
			if result["id"] == "" {
				t.Error("response missing id")
			}
		})
	}
}

func TestEnvelopeHandlerEmpty(t *testing.T) {
	handler := EnvelopeHandler(nil)
	body := loadEnvelopeFixture(t, "empty.envelope")

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Empty envelope has no event_id in header; server generates one.
	if result["id"] == "" {
		t.Error("response missing id for empty envelope")
	}
}

func TestEnvelopeHandlerMalformed(t *testing.T) {
	handler := EnvelopeHandler(nil)
	body := loadEnvelopeFixture(t, "malformed_header.envelope")

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEnvelopeHandlerOversized(t *testing.T) {
	handler := EnvelopeHandler(nil)

	big := make([]byte, maxEnvelopeBodySize+1)
	for i := range big {
		big[i] = 'A'
	}

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(big))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestEnvelopeHandlerQueueFullReturns503(t *testing.T) {
	pipe := pipeline.New(nil, 1, 1)
	handler := EnvelopeHandler(pipe)
	body := loadEnvelopeFixture(t, "single_error.envelope")

	first := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	handler.ServeHTTP(first, req1)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	handler.ServeHTTP(second, req2)
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}

// TestEnvelopeHandlerQueueFullSuppressesSideEffects verifies that when
// the pipeline queue rejects an event, side effects (feedback, attachments,
// sessions, check-ins, outcomes) are not persisted.
func TestEnvelopeHandlerQueueFullSuppressesSideEffects(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	feedback := sqlite.NewFeedbackStore(db)
	outcomes := sqlite.NewOutcomeStore(db)
	sessions := sqlite.NewReleaseHealthStore(db)
	monitors := sqlite.NewMonitorStore(db)

	// Queue size 1 so the first event fills it.
	pipe := pipeline.New(nil, 1, 1)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		Pipeline:        pipe,
		AttachmentStore: attachments,
		BlobStore:       blobs,
		FeedbackStore:   feedback,
		OutcomeStore:    outcomes,
		SessionStore:    sessions,
		MonitorStore:    monitors,
	})

	// Fill the queue with a plain event.
	fillBody := loadEnvelopeFixture(t, "single_error.envelope")
	fillRec := httptest.NewRecorder()
	handler.ServeHTTP(fillRec, httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(fillBody)))
	if fillRec.Code != http.StatusOK {
		t.Fatalf("fill status = %d, want 200", fillRec.Code)
	}

	// Send an envelope with an event + many side effects. Queue is full
	// so the event will be rejected — side effects must NOT persist.
	multiBody := loadEnvelopeFixture(t, "multi_item.envelope")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(multiBody)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("multi status = %d, want 503", rec.Code)
	}

	// Verify nothing leaked into the stores.
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE project_id = '1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM user_feedback WHERE project_id = '1'`, 0)
}

func TestEnvelopeHandlerStoresReplayMetadataAndAssets(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     sqlite.NewReplayStore(db, blobs),
		AttachmentStore: attachments,
		BlobStore:       blobs,
	})

	replayPayload := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","replay_id":"replay-1","timestamp":"2026-03-28T12:00:00Z","request":{"url":"https://app.example.com/checkout"},"user":{"email":"dev@example.com"}}`
	recordingPayload := `{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout"}}]}`
	body := []byte(strings.Join([]string{
		`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"type":"replay_event","length":0}`,
		replayPayload,
		`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
		recordingPayload,
		"",
	}, "\n"))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	stored, err := sqlite.NewEventStore(db).GetEventByType(t.Context(), "1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "replay")
	if err != nil {
		t.Fatalf("GetEventByType replay: %v", err)
	}
	if stored == nil || stored.Culprit != "https://app.example.com/checkout" || stored.UserIdentifier != "dev@example.com" {
		t.Fatalf("unexpected replay event: %+v", stored)
	}
	items, err := attachments.ListByEvent(t.Context(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("ListByEvent replay attachment: %v", err)
	}
	if len(items) != 1 || items[0].Name != "segment-1.rrweb" {
		t.Fatalf("unexpected replay attachments: %+v", items)
	}
	var manifestCount, timelineCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_manifests WHERE project_id = '1' AND replay_id = 'replay-1'`).Scan(&manifestCount); err != nil {
		t.Fatalf("count replay manifests: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-1'`).Scan(&timelineCount); err != nil {
		t.Fatalf("count replay timeline: %v", err)
	}
	if manifestCount != 1 || timelineCount != 1 {
		t.Fatalf("unexpected replay index rows: manifests=%d timeline=%d", manifestCount, timelineCount)
	}
}

func TestEnvelopeHandlerReplayRetryIsIdempotent(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     sqlite.NewReplayStore(db, blobs),
		AttachmentStore: attachments,
		BlobStore:       blobs,
	})

	body := []byte(strings.Join([]string{
		`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		`{"type":"replay_event","length":0}`,
		`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","replay_id":"replay-retry","timestamp":"2026-03-28T12:00:00Z","request":{"url":"https://app.example.com/cart"}}`,
		`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
		`{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/cart"}}]}`,
		"",
	}, "\n"))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200", i, rec.Code)
		}
	}

	var manifestCount, attachmentCount, timelineCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_manifests WHERE project_id = '1' AND replay_id = 'replay-retry'`).Scan(&manifestCount); err != nil {
		t.Fatalf("count replay manifests: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_attachments WHERE event_id = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`).Scan(&attachmentCount); err != nil {
		t.Fatalf("count replay attachments: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-retry'`).Scan(&timelineCount); err != nil {
		t.Fatalf("count replay timeline: %v", err)
	}
	if manifestCount != 1 || attachmentCount != 1 || timelineCount != 1 {
		t.Fatalf("unexpected retry row counts: manifests=%d attachments=%d timeline=%d", manifestCount, attachmentCount, timelineCount)
	}
}

func TestEnvelopeHandlerAppliesReplaySamplingPolicy(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")
	insertReplayPolicyForTest(t, db, "1", 0, 4096, "[]", "[]")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	outcomes := sqlite.NewOutcomeStore(db)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     sqlite.NewReplayStore(db, blobs),
		ReplayPolicies:  sqlite.NewReplayConfigStore(db),
		AttachmentStore: attachments,
		BlobStore:       blobs,
		OutcomeStore:    outcomes,
	})

	body := []byte(strings.Join([]string{
		`{"event_id":"cccccccccccccccccccccccccccccccc"}`,
		`{"type":"replay_event","length":0}`,
		`{"event_id":"cccccccccccccccccccccccccccccccc","replay_id":"cccccccccccccccccccccccccccccccc","timestamp":"2026-03-28T12:00:00Z"}`,
		`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
		`{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout"}}]}`,
		"",
	}, "\n"))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE project_id = '1' AND event_type = 'replay'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE project_id = '1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE project_id = '1'`, 0)
	items, err := outcomes.ListRecent(t.Context(), "1", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 || items[0].Category != "replay" || items[0].Reason != "sample_rate" {
		t.Fatalf("unexpected outcomes: %+v", items)
	}
}

func TestEnvelopeHandlerScrubsReplayPrivacyFields(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")
	insertReplayPolicyForTest(t, db, "1", 1, 4096, `["email","token","password"]`, `[".secret"]`)

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	replays := sqlite.NewReplayStore(db, blobs)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     replays,
		ReplayPolicies:  sqlite.NewReplayConfigStore(db),
		AttachmentStore: attachments,
		BlobStore:       blobs,
	})

	replayPayload := `{"event_id":"dddddddddddddddddddddddddddddddd","replay_id":"dddddddddddddddddddddddddddddddd","timestamp":"2026-03-28T12:00:00Z","request":{"url":"https://app.example.com/checkout?token=secret"},"user":{"email":"secret@example.com"}}`
	recordingPayload := `{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout?token=secret"}},{"type":"click","offset_ms":20,"data":{"selector":".secret","text":"4111111111111111","value":"super-secret"}}]}`
	body := []byte(strings.Join([]string{
		`{"event_id":"dddddddddddddddddddddddddddddddd"}`,
		`{"type":"replay_event","length":0}`,
		replayPayload,
		`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
		recordingPayload,
		"",
	}, "\n"))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	stored, err := sqlite.NewEventStore(db).GetEventByType(t.Context(), "1", "dddddddddddddddddddddddddddddddd", "replay")
	if err != nil {
		t.Fatalf("GetEventByType replay: %v", err)
	}
	payloadText := string(stored.NormalizedJSON)
	if strings.Contains(payloadText, "secret@example.com") || strings.Contains(payloadText, "token=secret") {
		t.Fatalf("expected replay receipt to be scrubbed: %s", payloadText)
	}
	if !strings.Contains(payloadText, "privacy_policy_version") {
		t.Fatalf("expected replay receipt to include privacy version: %s", payloadText)
	}
	list, err := attachments.ListByEvent(t.Context(), "dddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one replay attachment, got %d", len(list))
	}
	_, attachmentBody, err := attachments.GetAttachment(t.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	attachmentText := string(attachmentBody)
	if strings.Contains(attachmentText, "super-secret") || strings.Contains(attachmentText, "4111111111111111") || strings.Contains(attachmentText, "token=secret") || strings.Contains(attachmentText, ".secret") {
		t.Fatalf("expected replay recording to be scrubbed: %s", attachmentText)
	}
	if !strings.Contains(attachmentText, "[Filtered]") {
		t.Fatalf("expected replay recording to include filtered marker: %s", attachmentText)
	}
	replay, err := replays.GetReplay(t.Context(), "1", "dddddddddddddddddddddddddddddddd")
	if err != nil {
		t.Fatalf("GetReplay: %v", err)
	}
	if replay.Manifest.PrivacyPolicyVersion == "" {
		t.Fatalf("expected replay manifest privacy version: %+v", replay.Manifest)
	}
}

func TestEnvelopeHandlerEnforcesReplaySizeCap(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")
	insertReplayPolicyForTest(t, db, "1", 1, 32, "[]", "[]")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	outcomes := sqlite.NewOutcomeStore(db)
	replays := sqlite.NewReplayStore(db, blobs)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     replays,
		ReplayPolicies:  sqlite.NewReplayConfigStore(db),
		AttachmentStore: attachments,
		BlobStore:       blobs,
		OutcomeStore:    outcomes,
	})

	body := []byte(strings.Join([]string{
		`{"event_id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}`,
		`{"type":"replay_event","length":0}`,
		`{"event_id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","replay_id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","timestamp":"2026-03-28T12:00:00Z","request":{"url":"https://app.example.com/checkout"}}`,
		`{"type":"replay_recording","length":0,"filename":"segment-1.rrweb","content_type":"application/json"}`,
		`{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout"}}]}`,
		"",
	}, "\n"))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	stored, err := sqlite.NewEventStore(db).GetEventByType(t.Context(), "1", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "replay")
	if err != nil {
		t.Fatalf("GetEventByType replay: %v", err)
	}
	if !strings.Contains(string(stored.NormalizedJSON), "policy_drop_reason") {
		t.Fatalf("expected replay receipt to record drop reason: %s", string(stored.NormalizedJSON))
	}
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE event_id = 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'`, 0)
	replay, err := replays.GetReplay(t.Context(), "1", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	if err != nil {
		t.Fatalf("GetReplay: %v", err)
	}
	if replay.Manifest.ProcessingStatus != store.ReplayProcessingStatusPartial || !strings.Contains(replay.Manifest.IngestError, "max_bytes policy") {
		t.Fatalf("unexpected replay manifest after quota enforcement: %+v", replay.Manifest)
	}
	items, err := outcomes.ListRecent(t.Context(), "1", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 || items[0].Category != "replay" || items[0].Reason != "max_bytes" {
		t.Fatalf("unexpected replay outcomes: %+v", items)
	}
}

func TestEnvelopeHandlerStoresProfileMetadata(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")
	blobs := store.NewMemoryBlobStore()

	handler := EnvelopeHandlerWithDeps(IngestDeps{
		EventStore:   sqlite.NewEventStore(db),
		ProfileStore: sqlite.NewProfileStore(db, blobs),
		BlobStore:    blobs,
	})

	profilePayload := string(profilefixtures.MalformedEmpty().Spec().
		WithIDs("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "profile-1").
		WithTimestamp(time.Date(2026, time.March, 28, 12, 5, 0, 0, time.UTC)).
		Payload())
	body := []byte(strings.Join([]string{
		`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		`{"type":"profile","length":0}`,
		profilePayload,
		"",
	}, "\n"))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	stored, err := sqlite.NewEventStore(db).GetEventByType(t.Context(), "1", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "profile")
	if err != nil {
		t.Fatalf("GetEventByType profile: %v", err)
	}
	if stored == nil || stored.Culprit != "checkout" || stored.EventType != "profile" {
		t.Fatalf("unexpected profile event: %+v", stored)
	}

	profile, err := sqlite.NewProfileStore(db, blobs).GetProfile(t.Context(), "1", "profile-1")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.Manifest.ProfileID != "profile-1" || profile.Manifest.ProcessingStatus != store.ProfileProcessingStatusFailed {
		t.Fatalf("unexpected stored profile: %+v", profile.Manifest)
	}
	if _, err := blobs.Get(t.Context(), profile.Manifest.RawBlobKey); err != nil {
		t.Fatalf("Get raw profile blob: %v", err)
	}
}

func TestEnvelopeHandlerPersistsAttachmentMetadataAndBlob(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("db ping: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		AttachmentStore: attachments,
		BlobStore:       blobs,
	})

	body := []byte(
		"{\"event_id\":\"evt-attach-1\"}\n" +
			"{\"type\":\"attachment\",\"length\":16,\"content_type\":\"text/plain\",\"filename\":\"note.txt\"}\n" +
			"attachment-bytes",
	)

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["id"] != "evt-attach-1" {
		t.Fatalf("id = %q, want %q", result["id"], "evt-attach-1")
	}

	list, err := attachments.ListByEvent(req.Context(), "evt-attach-1")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByEvent returned %d items, want 1", len(list))
	}

	got, payload, err := attachments.GetAttachment(req.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got == nil {
		t.Fatal("GetAttachment returned nil")
	}
	if got.Name != "note.txt" {
		t.Fatalf("Name = %q, want %q", got.Name, "note.txt")
	}
	if got.ContentType != "text/plain" {
		t.Fatalf("ContentType = %q, want %q", got.ContentType, "text/plain")
	}
	if string(payload) != "attachment-bytes" {
		t.Fatalf("payload = %q, want %q", payload, "attachment-bytes")
	}
}

func TestEnvelopeHandlerPersistsClientReportOutcomes(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	outcomes := sqlite.NewOutcomeStore(db)
	handler := EnvelopeHandlerWithDeps(IngestDeps{OutcomeStore: outcomes})

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(loadEnvelopeFixture(t, "with_client_report.envelope")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	items, err := outcomes.ListRecent(req.Context(), "1", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Category != "error" || items[0].Reason != "sample_rate" || items[0].Quantity != 5 {
		t.Fatalf("unexpected outcome: %+v", items[0])
	}
}

func TestEnvelopeHandlerPersistsCheckIn(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	monitors := sqlite.NewMonitorStore(db)
	handler := EnvelopeHandlerWithDeps(IngestDeps{MonitorStore: monitors})

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(loadEnvelopeFixture(t, "check_in.envelope")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	list, err := monitors.ListMonitors(req.Context(), "1", 10)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(list) != 1 || list[0].Slug != "nightly-import" {
		t.Fatalf("unexpected monitors: %+v", list)
	}

	checkIns, err := monitors.ListCheckIns(req.Context(), "1", "nightly-import", 10)
	if err != nil {
		t.Fatalf("ListCheckIns: %v", err)
	}
	if len(checkIns) != 1 || checkIns[0].Status != "ok" {
		t.Fatalf("unexpected check-ins: %+v", checkIns)
	}
}

func TestEnvelopeHandlerPersistsTransaction(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: store.NewMemoryEventStore(),
		Groups: issue.NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.New(processor, 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	payload := "{\"type\":\"transaction\",\"event_id\":\"txnenvelope111111111111111111111111\",\"platform\":\"javascript\",\"transaction\":\"GET /checkout\",\"start_timestamp\":\"2026-03-27T12:00:00Z\",\"timestamp\":\"2026-03-27T12:00:01Z\",\"contexts\":{\"trace\":{\"trace_id\":\"trace-envelope-1\",\"span_id\":\"root-envelope-1\",\"op\":\"http.server\",\"status\":\"ok\"}}}"
	body := []byte(fmt.Sprintf(
		"{\"event_id\":\"txnenvelope111111111111111111111111\",\"dsn\":\"https://abc123@o1.ingest.example.com/1\"}\n"+
			"{\"type\":\"transaction\",\"length\":%d}\n%s",
		len(payload),
		payload,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	EnvelopeHandlerWithDeps(IngestDeps{Pipeline: pipe}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	waitForTransactionCount(t, db, "1", 1)
}

func TestEnvelopeHandlerPersistsTransactionDurable(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: store.NewMemoryEventStore(),
		Groups: issue.NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.NewDurable(processor, sqlite.NewJobStore(db), 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	payload := "{\"type\":\"transaction\",\"event_id\":\"abc123abc123abc123abc123abc123ab\",\"platform\":\"javascript\",\"transaction\":\"GET /checkout\",\"start_timestamp\":\"2026-03-27T12:00:00Z\",\"timestamp\":\"2026-03-27T12:00:01Z\",\"contexts\":{\"trace\":{\"trace_id\":\"trace-envelope-durable-1\",\"span_id\":\"root-envelope-durable-1\",\"op\":\"http.server\",\"status\":\"ok\"}}}"
	body := []byte(fmt.Sprintf(
		"{\"event_id\":\"abc123abc123abc123abc123abc123ab\",\"dsn\":\"https://abc123@o1.ingest.example.com/1\"}\n"+
			"{\"type\":\"transaction\",\"length\":%d}\n%s",
		len(payload),
		payload,
	))
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	EnvelopeHandlerWithDeps(IngestDeps{Pipeline: pipe}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	waitForTransactionCount(t, db, "1", 1)
}

func TestEnvelopeHandlerPersistsSessionItem(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	handler := EnvelopeHandlerWithDeps(IngestDeps{SessionStore: releaseHealth})

	sessionPayload := `{"sid":"sid-1","did":"user-1","status":"crashed","errors":1,"started":"2026-03-27T08:00:00Z","attrs":{"release":"ios@1.2.3","environment":"production"}}`
	body := []byte(fmt.Sprintf("{}\n{\"type\":\"session\",\"length\":%d}\n%s", len(sessionPayload), sessionPayload))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	summary, err := releaseHealth.GetReleaseHealth(req.Context(), "1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("GetReleaseHealth: %v", err)
	}
	if summary.SessionCount != 1 || summary.CrashedSessions != 1 {
		t.Fatalf("summary = %+v, want 1 crashed session", summary)
	}
}

func TestEnvelopeHandlerPersistsSessionAggregates(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	handler := EnvelopeHandlerWithDeps(IngestDeps{SessionStore: releaseHealth})

	aggregatePayload := `{"attrs":{"release":"ios@2.0.0","environment":"production"},"aggregates":[{"started":"2026-03-27T09:00:00Z","exited":3,"errored":2,"abnormal":1,"crashed":1}]}`
	body := []byte(fmt.Sprintf("{}\n{\"type\":\"sessions\",\"length\":%d}\n%s", len(aggregatePayload), aggregatePayload))

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	summary, err := releaseHealth.GetReleaseHealth(req.Context(), "1", "ios@2.0.0")
	if err != nil {
		t.Fatalf("GetReleaseHealth: %v", err)
	}
	if summary.SessionCount != 7 {
		t.Fatalf("SessionCount = %d, want 7", summary.SessionCount)
	}
	if summary.ErroredSessions != 2 || summary.AbnormalSessions != 1 || summary.CrashedSessions != 1 {
		t.Fatalf("summary = %+v, want errored=2 abnormal=1 crashed=1", summary)
	}
}

func TestEnvelopeHandlerPersistsSessionForReleaseHealth(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	handler := EnvelopeHandlerWithDeps(IngestDeps{
		SessionStore: sqlite.NewReleaseHealthStore(db),
	})

	sessionPayload := []byte(`{"sid":"sid-1","did":"user-1","started":"2026-01-02T03:04:05Z","status":"ok","errors":0,"attrs":{"release":"ios@1.2.3","environment":"production"}}`)
	body := []byte(
		"{\"event_id\":\"evt-session-1\"}\n" +
			fmt.Sprintf("{\"type\":\"session\",\"length\":%d}\n", len(sessionPayload)) +
			string(sessionPayload),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	health, err := sqlite.NewReleaseHealthStore(db).GetReleaseHealth(req.Context(), "1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("GetReleaseHealth: %v", err)
	}
	if health.SessionCount != 1 || health.CrashFreeRate != 100 {
		t.Fatalf("unexpected release health: %+v", health)
	}
}

func TestMinidumpHandlerStoresAttachmentAndSyntheticEvent(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	blobs := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobs)
	jobs := sqlite.NewJobStore(db)
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, jobs, 2)
	handler := MinidumpHandlerWithDeps(IngestDeps{
		NativeCrashes: nativeCrashes,
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload_file_minidump", "crash.dmp")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("minidump-bytes")); err != nil {
		t.Fatalf("write minidump: %v", err)
	}
	if err := writer.WriteField("release", "native@1.0.0"); err != nil {
		t.Fatalf("WriteField(release): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/1/minidump/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("expected synthetic event id")
	}

	items, err := attachments.ListByEvent(req.Context(), result["id"])
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(items) != 1 || items[0].ContentType != "application/x-dmp" {
		t.Fatalf("unexpected minidump attachment metadata: %+v", items)
	}
	if got, err := jobs.Len(t.Context()); err != nil {
		t.Fatalf("JobStore.Len: %v", err)
	} else if got != 1 {
		t.Fatalf("job length = %d, want 1", got)
	}
}
