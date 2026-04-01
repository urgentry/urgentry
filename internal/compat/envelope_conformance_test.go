//go:build integration

package compat

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestEnvelopeBasicEvent sends an envelope with a single event item and verifies
// it is accepted (200) and the event is stored in the database.
func TestEnvelopeBasicEvent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "envelope basic event",
		"level":    "error",
		"platform": "python",
	})

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}

	waitForEvent(t, srv.db, eventID)
}

// TestEnvelopeMultipleItems sends an envelope with event + attachment items.
func TestEnvelopeMultipleItems(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "envelope multi-item event",
		"level":    "warning",
		"platform": "javascript",
	})
	attachmentData := []byte("this is an attachment payload")

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: attachmentData, filename: "debug.log", contentType: "text/plain"},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForEvent(t, srv.db, eventID)

	// Verify the attachment was persisted.
	waitForAttachment(t, srv.db, eventID, "debug.log")
}

// TestEnvelopeTransaction sends an envelope with a transaction item.
func TestEnvelopeTransaction(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2"
	txPayload := jsonPayload(map[string]any{
		"event_id":    eventID,
		"type":        "transaction",
		"transaction": "/api/checkout",
		"start_timestamp": "2026-04-01T10:00:00Z",
		"timestamp":       "2026-04-01T10:00:01.500Z",
		"contexts": map[string]any{
			"trace": map[string]any{
				"trace_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
				"span_id":  "bbbbbbbbbbbbbb01",
				"op":       "http.server",
			},
		},
		"spans": []any{},
	})

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "transaction", payload: txPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForTransactionCount(t, srv.db, "default-project", 1)
}

// TestEnvelopeSession sends an envelope with a session item.
func TestEnvelopeSession(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "session-aaa-bbb-ccc",
		"did":     "user-123",
		"status":  "ok",
		"errors":  0,
		"started": "2026-04-01T12:00:00Z",
		"release": "myapp@1.0.0",
		"attrs": map[string]any{
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3"},
		envelopeItem{typ: "session", payload: sessionPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForSession(t, srv.db, "default-project", "myapp@1.0.0")
}

// TestEnvelopeUserFeedback sends an envelope with a user_report item.
func TestEnvelopeUserFeedback(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4",
		"name":     "Jane Tester",
		"email":    "jane@example.com",
		"comments": "Something broke on checkout",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4"},
		envelopeItem{typ: "user_report", payload: feedbackPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForFeedback(t, srv.db, "default-project", "Jane Tester")
}

// TestEnvelopeCheckIn sends an envelope with a check_in item (monitors/cron).
func TestEnvelopeCheckIn(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-aaa-bbb-ccc",
		"monitor_slug": "daily-backup",
		"status":       "ok",
		"duration":     12.5,
		"environment":  "production",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "0 3 * * *",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5"},
		envelopeItem{typ: "check_in", payload: checkInPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForCheckIn(t, srv.db, "default-project", "daily-backup")
}

// TestEnvelopeGzip sends a gzip-compressed envelope and verifies it is accepted.
func TestEnvelopeGzip(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6e6"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "gzip envelope test",
		"level":    "info",
		"platform": "go",
	})

	raw := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
	)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", &buf, map[string]string{
		"Content-Type":     "application/x-sentry-envelope",
		"Content-Encoding": "gzip",
		"X-Sentry-Auth":    srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}

	waitForEvent(t, srv.db, eventID)
}

// TestEnvelopeAuthRequired verifies that a missing auth header returns 401.
func TestEnvelopeAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventPayload := jsonPayload(map[string]any{
		"event_id": "e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7",
		"message":  "should be rejected",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7e7"},
		envelopeItem{typ: "event", payload: eventPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type": "application/x-sentry-envelope",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestEnvelopeEmptyBody verifies that an empty body returns an error.
func TestEnvelopeEmptyBody(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader([]byte{}), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestEnvelopeMalformedHeader verifies that invalid JSON in the header line returns an error.
func TestEnvelopeMalformedHeader(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	malformed := []byte("this is not valid json\n{\"type\":\"event\",\"length\":2}\n{}\n")

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(malformed), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// --- wait helpers for envelope-specific item types ---

func waitForAttachment(t *testing.T, db *sql.DB, eventID, filename string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM event_attachments WHERE event_id = ? AND name = ?`, eventID, filename).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("attachment %q for event %s was not persisted", filename, eventID)
}

func waitForSession(t *testing.T, db *sql.DB, projectID, release string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM release_sessions WHERE project_id = ? AND release_version = ?`, projectID, release).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("session for project %s release %s was not persisted", projectID, release)
}

func waitForFeedback(t *testing.T, db *sql.DB, projectID, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM user_feedback WHERE project_id = ? AND name = ?`, projectID, name).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("feedback from %q for project %s was not persisted", name, projectID)
}

func waitForCheckIn(t *testing.T, db *sql.DB, projectID, monitorSlug string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, projectID, monitorSlug).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Debug: check if monitor was created but check-in wasn't.
	var monitorCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM monitors WHERE project_id = ? AND slug = ?`, projectID, monitorSlug).Scan(&monitorCount)
	t.Fatalf("check-in for project %s monitor %s was not persisted (monitors=%d)", projectID, monitorSlug, monitorCount)
}

