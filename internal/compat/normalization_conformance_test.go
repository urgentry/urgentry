//go:build integration

package compat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/normalize"
)

// fetchStoredPayload waits for an event to be persisted and returns the
// payload_json, level, platform, environment, message, release, tags_json,
// and occurred_at columns.
func fetchStoredPayload(t *testing.T, db *sql.DB, eventID string) (payload []byte, level, platform, env, message, release, tagsJSON, occurredAt string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := db.QueryRow(
			`SELECT COALESCE(payload_json,''), COALESCE(level,''), COALESCE(platform,''),
			        COALESCE(environment,''), COALESCE(message,''), COALESCE(release,''),
			        COALESCE(tags_json,'{}'), COALESCE(occurred_at,'')
			 FROM events WHERE event_id = ?`, eventID,
		).Scan(&payload, &level, &platform, &env, &message, &release, &tagsJSON, &occurredAt)
		if err == nil && len(payload) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("event %s was not persisted within timeout", eventID)
	return
}

// sendStoreEvent posts a JSON event to the store endpoint and asserts 200.
func sendStoreEvent(t *testing.T, srv *compatServer, payload []byte) {
	t.Helper()
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("store status = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

// unmarshalNormalized parses the payload_json back into a normalize.Event.
func unmarshalNormalized(t *testing.T, payload []byte) *normalize.Event {
	t.Helper()
	var evt normalize.Event
	if err := json.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("unmarshal normalized payload: %v", err)
	}
	return &evt
}

// ---------------------------------------------------------------------------
// 1. TestNormalizeLongMessage
// ---------------------------------------------------------------------------

// TestNormalizeLongMessage sends an event whose message exceeds 8192 characters
// and verifies the message is stored in the payload. The current normalizer
// preserves the full message; Sentry truncates at 8192. We validate that the
// stored message is at most the original length (i.e. no expansion) and that
// the Title() helper truncates to 100 characters for display.
func TestNormalizeLongMessage(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	longMsg := strings.Repeat("A", 9000)
	eventID := "1111111111111111111111111111111a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  longMsg,
	})

	sendStoreEvent(t, srv, raw)

	payload, _, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	evt := unmarshalNormalized(t, payload)

	// The normalizer stores the full message. Verify no expansion occurred.
	if len(evt.Message) > len(longMsg) {
		t.Fatalf("message expanded: got %d chars, original %d chars", len(evt.Message), len(longMsg))
	}
	// The message should be preserved in full.
	if len(evt.Message) < 8192 {
		t.Fatalf("expected message length >= 8192, got %d", len(evt.Message))
	}

	// Title() should truncate to 100 chars for display.
	title := evt.Title()
	if len(title) > 100 {
		t.Fatalf("title should be at most 100 chars, got %d", len(title))
	}
}

// ---------------------------------------------------------------------------
// 2. TestNormalizeDefaultLevel
// ---------------------------------------------------------------------------

// TestNormalizeDefaultLevel sends an event without a level field and verifies
// that it defaults to "error".
func TestNormalizeDefaultLevel(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "2222222222222222222222222222222a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "no level set",
	})

	sendStoreEvent(t, srv, raw)

	payload, level, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	if level != "error" {
		t.Fatalf("DB level = %q, want %q", level, "error")
	}

	evt := unmarshalNormalized(t, payload)
	if evt.Level != "error" {
		t.Fatalf("payload level = %q, want %q", evt.Level, "error")
	}

	// Also test that "warn" normalizes to "warning".
	eventID2 := "2222222222222222222222222222222b"
	raw2, _ := json.Marshal(map[string]any{
		"event_id": eventID2,
		"message":  "warn level",
		"level":    "warn",
	})
	sendStoreEvent(t, srv, raw2)

	_, level2, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID2)
	if level2 != "warning" {
		t.Fatalf("DB level for 'warn' = %q, want %q", level2, "warning")
	}
}

// ---------------------------------------------------------------------------
// 3. TestNormalizePlatform
// ---------------------------------------------------------------------------

// TestNormalizePlatform verifies that an event without a platform defaults to
// "other", and that an event with a platform preserves it.
func TestNormalizePlatform(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// No platform field.
	eventID := "3333333333333333333333333333333a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "no platform",
	})
	sendStoreEvent(t, srv, raw)

	payload, _, platform, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	if platform != "other" {
		t.Fatalf("DB platform = %q, want %q", platform, "other")
	}
	evt := unmarshalNormalized(t, payload)
	if evt.Platform != "other" {
		t.Fatalf("payload platform = %q, want %q", evt.Platform, "other")
	}

	// Explicit platform.
	eventID2 := "3333333333333333333333333333333b"
	raw2, _ := json.Marshal(map[string]any{
		"event_id": eventID2,
		"message":  "python event",
		"platform": "python",
	})
	sendStoreEvent(t, srv, raw2)

	_, _, platform2, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID2)
	if platform2 != "python" {
		t.Fatalf("DB platform = %q, want %q", platform2, "python")
	}
}

// ---------------------------------------------------------------------------
// 4. TestNormalizeTimestamp
// ---------------------------------------------------------------------------

// TestNormalizeTimestamp verifies that an event without a timestamp gets a
// server-generated timestamp close to now.
func TestNormalizeTimestamp(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	before := time.Now().UTC().Add(-2 * time.Second)

	eventID := "4444444444444444444444444444444a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "no timestamp",
	})
	sendStoreEvent(t, srv, raw)

	after := time.Now().UTC().Add(2 * time.Second)

	payload, _, _, _, _, _, _, occurredAt := fetchStoredPayload(t, srv.db, eventID)

	// Check the DB column.
	ts, err := time.Parse(time.RFC3339, occurredAt)
	if err != nil {
		t.Fatalf("parse occurred_at %q: %v", occurredAt, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("occurred_at %v not in expected range [%v, %v]", ts, before, after)
	}

	// Check the normalized payload.
	evt := unmarshalNormalized(t, payload)
	if evt.Timestamp.Before(before) || evt.Timestamp.After(after) {
		t.Fatalf("payload timestamp %v not in expected range [%v, %v]", evt.Timestamp, before, after)
	}

	// Explicit timestamp should be preserved.
	eventID2 := "4444444444444444444444444444444b"
	explicit := "2025-06-15T10:30:00Z"
	raw2, _ := json.Marshal(map[string]any{
		"event_id":  eventID2,
		"message":   "explicit ts",
		"timestamp": explicit,
	})
	sendStoreEvent(t, srv, raw2)

	payload2, _, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID2)
	evt2 := unmarshalNormalized(t, payload2)
	want, _ := time.Parse(time.RFC3339, explicit)
	if !evt2.Timestamp.Equal(want) {
		t.Fatalf("payload timestamp = %v, want %v", evt2.Timestamp, want)
	}
}

// ---------------------------------------------------------------------------
// 5. TestNormalizeEventID
// ---------------------------------------------------------------------------

// TestNormalizeEventID verifies that an event without an event_id gets an
// auto-generated 32-char hex UUID, and that dashes are stripped and letters
// lowercased in explicitly provided event_ids.
func TestNormalizeEventID(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Event without event_id: the normalizer generates a 32-hex-char ID.
	// The store handler returns one ID, but the normalizer generates another.
	// We verify by waiting for an event to appear in the project.
	raw := []byte(`{"message":"no event_id"}`)
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(raw), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	respID := result["id"]
	if respID == "" {
		t.Fatal("response missing id")
	}
	// The response ID from the handler should be a 32-char hex string.
	if len(respID) != 32 {
		t.Fatalf("response id length = %d, want 32", len(respID))
	}

	// Wait for any event to land for this project.
	waitForProjectEventCount(t, srv.db, "default-project", 1)

	// Verify the stored event has a properly formatted event_id.
	var storedID string
	err := srv.db.QueryRow(`SELECT event_id FROM events WHERE project_id = 'default-project' ORDER BY ingested_at DESC LIMIT 1`).Scan(&storedID)
	if err != nil {
		t.Fatalf("query stored event_id: %v", err)
	}
	if len(storedID) != 32 {
		t.Fatalf("stored event_id length = %d, want 32; got %q", len(storedID), storedID)
	}
	if strings.ContainsAny(storedID, "-ABCDEF") {
		t.Fatalf("stored event_id %q should be lowercase hex without dashes", storedID)
	}

	// Event with UUID-format event_id (dashes): dashes should be stripped.
	dashedID := "AABBCCDD-1122-3344-5566-778899001122"
	normalizedID := "aabbccdd112233445566778899001122"
	raw2, _ := json.Marshal(map[string]any{
		"event_id": dashedID,
		"message":  "dashed id",
	})
	sendStoreEvent(t, srv, raw2)
	waitForEvent(t, srv.db, normalizedID)
}

// ---------------------------------------------------------------------------
// 6. TestNormalizeRelease
// ---------------------------------------------------------------------------

// TestNormalizeRelease verifies that the release field is preserved through
// normalization, and that whitespace-only releases are treated as empty.
func TestNormalizeRelease(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Normal release string.
	eventID := "6666666666666666666666666666666a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "with release",
		"release":  "myapp@1.2.3+build456",
	})
	sendStoreEvent(t, srv, raw)

	payload, _, _, _, _, release, _, _ := fetchStoredPayload(t, srv.db, eventID)
	if release != "myapp@1.2.3+build456" {
		t.Fatalf("DB release = %q, want %q", release, "myapp@1.2.3+build456")
	}
	evt := unmarshalNormalized(t, payload)
	if evt.Release != "myapp@1.2.3+build456" {
		t.Fatalf("payload release = %q, want %q", evt.Release, "myapp@1.2.3+build456")
	}

	// Empty release.
	eventID2 := "6666666666666666666666666666666b"
	raw2, _ := json.Marshal(map[string]any{
		"event_id": eventID2,
		"message":  "no release",
	})
	sendStoreEvent(t, srv, raw2)

	_, _, _, _, _, release2, _, _ := fetchStoredPayload(t, srv.db, eventID2)
	if release2 != "" {
		t.Fatalf("DB release for no-release event = %q, want empty", release2)
	}
}

// ---------------------------------------------------------------------------
// 7. TestNormalizeEnvironment
// ---------------------------------------------------------------------------

// TestNormalizeEnvironment verifies that events without an environment default
// to "production", and that explicit environments are preserved.
func TestNormalizeEnvironment(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// No environment — should default to "production".
	eventID := "7777777777777777777777777777777a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "no env",
	})
	sendStoreEvent(t, srv, raw)

	payload, _, _, env, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	if env != "production" {
		t.Fatalf("DB environment = %q, want %q", env, "production")
	}
	evt := unmarshalNormalized(t, payload)
	if evt.Environment != "production" {
		t.Fatalf("payload environment = %q, want %q", evt.Environment, "production")
	}

	// Explicit environment.
	eventID2 := "7777777777777777777777777777777b"
	raw2, _ := json.Marshal(map[string]any{
		"event_id":    eventID2,
		"message":     "staging env",
		"environment": "staging",
	})
	sendStoreEvent(t, srv, raw2)

	_, _, _, env2, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID2)
	if env2 != "staging" {
		t.Fatalf("DB environment = %q, want %q", env2, "staging")
	}
}

// ---------------------------------------------------------------------------
// 8. TestNormalizeTags
// ---------------------------------------------------------------------------

// TestNormalizeTags verifies that tags in both object format and array-of-pairs
// format are normalized into a consistent map, and that tag values are preserved.
func TestNormalizeTags(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Object format tags.
	eventID := "8888888888888888888888888888888a"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "tags test object",
		"tags": map[string]string{
			"browser": "Chrome 120",
			"os":      "Windows 11",
		},
	})
	sendStoreEvent(t, srv, raw)

	payload, _, _, _, _, _, tagsJSON, _ := fetchStoredPayload(t, srv.db, eventID)
	var dbTags map[string]string
	if err := json.Unmarshal([]byte(tagsJSON), &dbTags); err != nil {
		t.Fatalf("unmarshal tags_json: %v", err)
	}
	if dbTags["browser"] != "Chrome 120" {
		t.Fatalf("DB tag browser = %q, want %q", dbTags["browser"], "Chrome 120")
	}
	if dbTags["os"] != "Windows 11" {
		t.Fatalf("DB tag os = %q, want %q", dbTags["os"], "Windows 11")
	}

	evt := unmarshalNormalized(t, payload)
	if evt.Tags["browser"] != "Chrome 120" {
		t.Fatalf("payload tag browser = %q, want %q", evt.Tags["browser"], "Chrome 120")
	}

	// Array-of-pairs format tags: [["key", "value"], ...]
	eventID2 := "8888888888888888888888888888888b"
	raw2 := []byte(`{
		"event_id": "8888888888888888888888888888888b",
		"message": "tags test array",
		"tags": [["device", "iPhone 15"], ["app_version", "2.1.0"]]
	}`)
	sendStoreEvent(t, srv, raw2)

	payload2, _, _, _, _, _, tagsJSON2, _ := fetchStoredPayload(t, srv.db, eventID2)
	var dbTags2 map[string]string
	if err := json.Unmarshal([]byte(tagsJSON2), &dbTags2); err != nil {
		t.Fatalf("unmarshal tags_json: %v", err)
	}
	if dbTags2["device"] != "iPhone 15" {
		t.Fatalf("DB tag device = %q, want %q", dbTags2["device"], "iPhone 15")
	}

	evt2 := unmarshalNormalized(t, payload2)
	if evt2.Tags["app_version"] != "2.1.0" {
		t.Fatalf("payload tag app_version = %q, want %q", evt2.Tags["app_version"], "2.1.0")
	}
}

// ---------------------------------------------------------------------------
// 9. TestNormalizeStackTrace
// ---------------------------------------------------------------------------

// TestNormalizeStackTrace verifies that in_app flags on stacktrace frames are
// preserved through normalization and storage.
func TestNormalizeStackTrace(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	inApp := true
	notInApp := false
	eventID := "9999999999999999999999999999999a"

	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "stacktrace test",
		"platform": "python",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "ValueError",
					"value": "invalid input",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "django/core/handlers/base.py",
								"function": "get_response",
								"module":   "django.core.handlers.base",
								"lineno":   113,
								"in_app":   notInApp,
							},
							{
								"filename": "myapp/views.py",
								"function": "process_form",
								"module":   "myapp.views",
								"lineno":   42,
								"in_app":   inApp,
							},
							{
								"filename": "myapp/utils.py",
								"function": "validate_input",
								"module":   "myapp.utils",
								"lineno":   15,
								"in_app":   inApp,
							},
						},
					},
				},
			},
		},
	})
	sendStoreEvent(t, srv, raw)

	payload, _, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	evt := unmarshalNormalized(t, payload)

	if evt.Exception == nil || len(evt.Exception.Values) == 0 {
		t.Fatal("expected exception in normalized payload")
	}
	frames := evt.Exception.Values[0].Stacktrace.Frames
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}

	// Frame 0: django — not in_app.
	if frames[0].InApp == nil || *frames[0].InApp != false {
		t.Fatalf("frame 0 in_app = %v, want false", frames[0].InApp)
	}
	// Frame 1: myapp/views.py — in_app.
	if frames[1].InApp == nil || *frames[1].InApp != true {
		t.Fatalf("frame 1 in_app = %v, want true", frames[1].InApp)
	}
	// Frame 2: myapp/utils.py — in_app.
	if frames[2].InApp == nil || *frames[2].InApp != true {
		t.Fatalf("frame 2 in_app = %v, want true", frames[2].InApp)
	}

	// Culprit should be derived from the last in_app frame.
	culprit := evt.Culprit()
	if culprit == "" {
		t.Fatal("expected non-empty culprit from in_app frames")
	}
	if !strings.Contains(culprit, "validate_input") {
		t.Fatalf("culprit = %q, expected it to contain %q", culprit, "validate_input")
	}
}

// ---------------------------------------------------------------------------
// 10. TestNormalizePII
// ---------------------------------------------------------------------------

// TestNormalizePII verifies that normalize.ScrubEvent masks credit card numbers
// and sensitive field values. Note: ScrubEvent is not currently wired into the
// ingest pipeline, so this test validates the scrubbing logic directly on a
// normalized event round-tripped through the store.
func TestNormalizePII(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0"
	raw, _ := json.Marshal(map[string]any{
		"event_id": eventID,
		"message":  "User card is 4111111111111111 oops",
		"tags": map[string]string{
			"password":   "secret123",
			"browser":    "Chrome 120",
			"api_key":    "sk-live-abc123",
			"safe_field": "hello world",
		},
		"user": map[string]any{
			"id":         "user-42",
			"email":      "john@example.com",
			"ip_address": "192.168.1.100",
		},
		"extra": map[string]any{
			"credit_card_number": "5500000000000004",
			"debug_info":         "some safe data",
		},
	})

	sendStoreEvent(t, srv, raw)

	payload, _, _, _, _, _, _, _ := fetchStoredPayload(t, srv.db, eventID)
	evt := unmarshalNormalized(t, payload)

	// Apply scrubbing (as the normalize package provides it).
	normalize.ScrubEvent(evt, nil)

	// Credit card in message should be scrubbed.
	if strings.Contains(evt.Message, "4111111111111111") {
		t.Fatalf("message still contains credit card: %s", evt.Message)
	}
	if !strings.Contains(evt.Message, "[Filtered]") {
		t.Fatalf("message should contain [Filtered] placeholder: %s", evt.Message)
	}

	// Sensitive tag keys should be scrubbed.
	if evt.Tags["password"] != "[Filtered]" {
		t.Fatalf("tag password = %q, want [Filtered]", evt.Tags["password"])
	}
	if evt.Tags["api_key"] != "[Filtered]" {
		t.Fatalf("tag api_key = %q, want [Filtered]", evt.Tags["api_key"])
	}
	// Non-sensitive tags should be preserved.
	if evt.Tags["browser"] != "Chrome 120" {
		t.Fatalf("tag browser = %q, want %q", evt.Tags["browser"], "Chrome 120")
	}
	if evt.Tags["safe_field"] != "hello world" {
		t.Fatalf("tag safe_field = %q, want %q", evt.Tags["safe_field"], "hello world")
	}

	// User email should be scrubbed (contains email pattern).
	if strings.Contains(evt.User.Email, "john@example.com") {
		t.Fatalf("user email not scrubbed: %s", evt.User.Email)
	}

	// User IP should be partially masked (first two octets preserved).
	if evt.User.IPAddress != "192.168.0.0" {
		t.Fatalf("user ip = %q, want %q", evt.User.IPAddress, "192.168.0.0")
	}

	// Extra field with sensitive key should be scrubbed.
	if val, ok := evt.Extra["credit_card_number"]; ok && val != "[Filtered]" {
		t.Fatalf("extra credit_card_number = %v, want [Filtered]", val)
	}

	// Non-sensitive extra field should be preserved.
	if val, ok := evt.Extra["debug_info"]; !ok || val != "some safe data" {
		t.Fatalf("extra debug_info = %v, want %q", val, "some safe data")
	}
}
