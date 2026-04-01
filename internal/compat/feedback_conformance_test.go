//go:build integration

package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestFeedbackViaEnvelope sends a feedback item via envelope with type
// "user_report" (the canonical Sentry feedback envelope type) and verifies
// it is persisted in the user_feedback table.
func TestFeedbackViaEnvelope(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "fb01fb01fb01fb01fb01fb01fb01fb01",
		"name":     "Alice Envelope",
		"email":    "alice@example.com",
		"comments": "The page crashed when I clicked submit",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "fb01fb01fb01fb01fb01fb01fb01fb01"},
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

	waitForFeedback(t, srv.db, "default-project", "Alice Envelope")
}

// TestFeedbackViaUserReport sends a user_report envelope item with all fields
// populated and verifies the comments are stored correctly.
func TestFeedbackViaUserReport(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "fb02fb02fb02fb02fb02fb02fb02fb02",
		"name":     "Bob Reporter",
		"email":    "bob@example.com",
		"comments": "Upload failed with a 500 error",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "fb02fb02fb02fb02fb02fb02fb02fb02"},
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

	waitForFeedback(t, srv.db, "default-project", "Bob Reporter")

	// Verify the comments were stored correctly.
	var comments string
	err := srv.db.QueryRow(
		`SELECT comments FROM user_feedback WHERE project_id = ? AND name = ?`,
		"default-project", "Bob Reporter",
	).Scan(&comments)
	if err != nil {
		t.Fatalf("query feedback comments: %v", err)
	}
	if comments != "Upload failed with a 500 error" {
		t.Fatalf("comments = %q, want %q", comments, "Upload failed with a 500 error")
	}
}

// TestFeedbackRetrieval sends feedback via envelope, then retrieves it through
// the web feedback page to verify end-to-end visibility.
func TestFeedbackRetrieval(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "fb03fb03fb03fb03fb03fb03fb03fb03",
		"name":     "Carol Retriever",
		"email":    "carol@example.com",
		"comments": "Dashboard graphs are blank",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "fb03fb03fb03fb03fb03fb03fb03fb03"},
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

	waitForFeedback(t, srv.db, "default-project", "Carol Retriever")

	// Retrieve through the authenticated web feedback page.
	client := loginClient(t, srv)
	getResp, err := client.Get(srv.server.URL + "/feedback/")
	if err != nil {
		t.Fatalf("GET /feedback/: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /feedback/ status = %d, want 200", getResp.StatusCode)
	}

	pageBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read feedback page body: %v", err)
	}

	if !bytes.Contains(pageBody, []byte("Carol Retriever")) {
		t.Fatalf("feedback page does not contain 'Carol Retriever';\nbody snippet: %s", truncate(pageBody, 500))
	}
}

// TestFeedbackWithContact sends feedback with a contact_email field and
// verifies the email is persisted in the store.
func TestFeedbackWithContact(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "fb04fb04fb04fb04fb04fb04fb04fb04",
		"name":     "Dana Contact",
		"email":    "dana@contact.example.com",
		"comments": "Notifications are not arriving",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "fb04fb04fb04fb04fb04fb04fb04fb04"},
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

	waitForFeedback(t, srv.db, "default-project", "Dana Contact")

	// Verify the contact email is stored.
	var email string
	err := srv.db.QueryRow(
		`SELECT email FROM user_feedback WHERE project_id = ? AND name = ?`,
		"default-project", "Dana Contact",
	).Scan(&email)
	if err != nil {
		t.Fatalf("query feedback email: %v", err)
	}
	if email != "dana@contact.example.com" {
		t.Fatalf("email = %q, want %q", email, "dana@contact.example.com")
	}
}

// TestFeedbackLinkedToEvent sends an event first, then sends feedback linked
// to that event_id, and verifies the association is stored.
func TestFeedbackLinkedToEvent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// First, send an event.
	eventID := "fb05fb05fb05fb05fb05fb05fb05fb05"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "crash in checkout flow",
		"level":    "error",
		"platform": "javascript",
	})

	eventBody := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(eventBody), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event ingest status = %d, want 200", resp.StatusCode)
	}

	waitForEvent(t, srv.db, eventID)

	// Now send feedback linked to that event.
	feedbackPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"name":     "Eve Linked",
		"email":    "eve@example.com",
		"comments": "This crash happened after I clicked pay",
	})

	feedbackBody := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "user_report", payload: feedbackPayload},
	)

	resp2 := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(feedbackBody), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp2.Body)
		t.Fatalf("feedback ingest status = %d, want 200, body=%s", resp2.StatusCode, respBody)
	}

	waitForFeedback(t, srv.db, "default-project", "Eve Linked")

	// Verify the feedback is linked to the event.
	var storedEventID string
	err := srv.db.QueryRow(
		`SELECT event_id FROM user_feedback WHERE project_id = ? AND name = ?`,
		"default-project", "Eve Linked",
	).Scan(&storedEventID)
	if err != nil {
		t.Fatalf("query feedback event_id: %v", err)
	}
	if storedEventID != eventID {
		t.Fatalf("event_id = %q, want %q", storedEventID, eventID)
	}
}

// TestFeedbackAuthRequired verifies that feedback ingest requires auth.
func TestFeedbackAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	feedbackPayload := jsonPayload(map[string]any{
		"event_id": "fb06fb06fb06fb06fb06fb06fb06fb06",
		"name":     "Mallory NoAuth",
		"email":    "mallory@example.com",
		"comments": "This should be rejected",
	})

	body := buildEnvelope(
		map[string]any{"event_id": "fb06fb06fb06fb06fb06fb06fb06fb06"},
		envelopeItem{typ: "user_report", payload: feedbackPayload},
	)

	// No X-Sentry-Auth header.
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type": "application/x-sentry-envelope",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	// Verify nothing was stored.
	var count int
	err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM user_feedback WHERE name = ?`, "Mallory NoAuth",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query feedback count: %v", err)
	}
	if count != 0 {
		t.Fatalf("feedback count = %d, want 0 (should not be stored without auth)", count)
	}
}

// --- helpers ---

// truncate returns the first n bytes of b as a string, for error messages.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// Compile-time assertion that json import is used.
var _ = json.Marshal
