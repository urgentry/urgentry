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
)

// TestAttachmentViaEnvelope sends an envelope with an event + attachment item
// and verifies both the event and the attachment are stored.
func TestAttachmentViaEnvelope(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: basic",
		"level":    "error",
		"platform": "python",
	})
	attachmentData := []byte("hello from the attachment")

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: attachmentData, filename: "test.txt", contentType: "text/plain"},
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
	waitForAttachment(t, srv.db, eventID, "test.txt")

	// Verify content type was stored.
	var ct string
	err := srv.db.QueryRow(
		`SELECT content_type FROM event_attachments WHERE event_id = ? AND name = ?`,
		eventID, "test.txt",
	).Scan(&ct)
	if err != nil {
		t.Fatalf("query attachment content_type: %v", err)
	}
	if ct != "text/plain" {
		t.Fatalf("content_type = %q, want %q", ct, "text/plain")
	}
}

// TestAttachmentLargeFile sends a 1 MB attachment via envelope and verifies it is stored.
func TestAttachmentLargeFile(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "a1a1a1a1b1b1b1b1c1c1c1c1d1d1d1d1"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: large file",
		"level":    "info",
		"platform": "go",
	})

	// 1 MB of repeated bytes.
	largeData := bytes.Repeat([]byte("X"), 1<<20)

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: largeData, filename: "large.bin", contentType: "application/octet-stream"},
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
	waitForAttachment(t, srv.db, eventID, "large.bin")

	// Verify size was persisted correctly.
	var size int64
	err := srv.db.QueryRow(
		`SELECT size_bytes FROM event_attachments WHERE event_id = ? AND name = ?`,
		eventID, "large.bin",
	).Scan(&size)
	if err != nil {
		t.Fatalf("query attachment size: %v", err)
	}
	if size != 1<<20 {
		t.Fatalf("size = %d, want %d", size, 1<<20)
	}
}

// TestAttachmentMultiple sends 3 attachments in a single envelope and verifies all are stored.
func TestAttachmentMultiple(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "a2a2a2a2b2b2b2b2c2c2c2c2d2d2d2d2"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: multiple",
		"level":    "warning",
		"platform": "javascript",
	})

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: []byte("first file contents"), filename: "alpha.txt", contentType: "text/plain"},
		envelopeItem{typ: "attachment", payload: []byte("second file contents"), filename: "beta.log", contentType: "text/plain"},
		envelopeItem{typ: "attachment", payload: []byte("third file contents"), filename: "gamma.dat", contentType: "application/octet-stream"},
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
	waitForAttachment(t, srv.db, eventID, "alpha.txt")
	waitForAttachment(t, srv.db, eventID, "beta.log")
	waitForAttachment(t, srv.db, eventID, "gamma.dat")

	// Double-check total count.
	waitForAttachmentCount(t, srv.db, eventID, 3)
}

// TestAttachmentContentTypes sends attachments with different content types and
// verifies each is stored with the correct content_type value.
func TestAttachmentContentTypes(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "a3a3a3a3b3b3b3b3c3c3c3c3d3d3d3d3"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: content types",
		"level":    "info",
		"platform": "python",
	})

	types := []struct {
		filename    string
		contentType string
		data        []byte
	}{
		{"readme.txt", "text/plain", []byte("plain text content")},
		{"screenshot.png", "image/png", []byte("\x89PNG\r\n\x1a\nfake-png-data")},
		{"dump.bin", "application/octet-stream", []byte{0x00, 0x01, 0x02, 0xFF}},
	}

	items := []envelopeItem{
		{typ: "event", payload: eventPayload},
	}
	for _, ct := range types {
		items = append(items, envelopeItem{
			typ:         "attachment",
			payload:     ct.data,
			filename:    ct.filename,
			contentType: ct.contentType,
		})
	}

	body := buildEnvelope(map[string]any{"event_id": eventID}, items...)

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

	for _, ct := range types {
		waitForAttachment(t, srv.db, eventID, ct.filename)

		var stored string
		err := srv.db.QueryRow(
			`SELECT content_type FROM event_attachments WHERE event_id = ? AND name = ?`,
			eventID, ct.filename,
		).Scan(&stored)
		if err != nil {
			t.Fatalf("query content_type for %s: %v", ct.filename, err)
		}
		if stored != ct.contentType {
			t.Fatalf("content_type for %s = %q, want %q", ct.filename, stored, ct.contentType)
		}
	}
}

// TestAttachmentRetrieval uploads an attachment via envelope, then retrieves it
// through the GET /api/0/events/{event_id}/attachments/ API endpoint.
func TestAttachmentRetrieval(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "a4a4a4a4b4b4b4b4c4c4c4c4d4d4d4d4"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: retrieval",
		"level":    "error",
		"platform": "go",
	})
	attachmentContent := []byte("retrievable content payload")

	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: attachmentContent, filename: "retrieve.txt", contentType: "text/plain"},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("ingest status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	waitForEvent(t, srv.db, eventID)
	waitForAttachment(t, srv.db, eventID, "retrieve.txt")

	// List attachments via API.
	listResp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/events/"+eventID+"/attachments/",
		srv.pat, nil, "")
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		listBody, _ := io.ReadAll(listResp.Body)
		t.Fatalf("list attachments status = %d, want 200, body=%s", listResp.StatusCode, listBody)
	}

	var attachments []struct {
		ID          string `json:"id"`
		EventID     string `json:"eventId"`
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
		Size        int64  `json:"size"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&attachments); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(attachments))
	}
	att := attachments[0]
	if att.Name != "retrieve.txt" {
		t.Fatalf("name = %q, want %q", att.Name, "retrieve.txt")
	}
	if att.ContentType != "text/plain" {
		t.Fatalf("contentType = %q, want %q", att.ContentType, "text/plain")
	}
	if att.Size != int64(len(attachmentContent)) {
		t.Fatalf("size = %d, want %d", att.Size, len(attachmentContent))
	}

	// Download the actual attachment data.
	dlResp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/events/"+eventID+"/attachments/"+att.ID+"/",
		srv.pat, nil, "")
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		dlBody, _ := io.ReadAll(dlResp.Body)
		t.Fatalf("download attachment status = %d, want 200, body=%s", dlResp.StatusCode, dlBody)
	}

	gotData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if !bytes.Equal(gotData, attachmentContent) {
		t.Fatalf("downloaded content mismatch: got %d bytes, want %d bytes", len(gotData), len(attachmentContent))
	}
	if ct := dlResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("download Content-Type = %q, want text/plain", ct)
	}
}

// TestAttachmentAuthRequired verifies that the attachment list endpoint requires
// authentication and returns 401 without a valid token.
func TestAttachmentAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// First, ingest an event so the endpoint is reachable.
	eventID := "a5a5a5a5b5b5b5b5c5c5c5c5d5d5d5d5"
	eventPayload := jsonPayload(map[string]any{
		"event_id": eventID,
		"message":  "attachment conformance: auth",
		"level":    "error",
		"platform": "python",
	})
	body := buildEnvelope(
		map[string]any{"event_id": eventID},
		envelopeItem{typ: "event", payload: eventPayload},
		envelopeItem{typ: "attachment", payload: []byte("auth test"), filename: "secret.txt", contentType: "text/plain"},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()

	waitForEvent(t, srv.db, eventID)
	waitForAttachment(t, srv.db, eventID, "secret.txt")

	// Request without auth should be rejected.
	noAuthResp := doRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/events/"+eventID+"/attachments/",
		nil, map[string]string{})
	defer noAuthResp.Body.Close()

	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", noAuthResp.StatusCode)
	}

	// Request with invalid token should be rejected.
	badAuthResp := doRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/events/"+eventID+"/attachments/",
		nil, map[string]string{
			"Authorization": "Bearer invalid-token-xyz",
		})
	defer badAuthResp.Body.Close()

	if badAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", badAuthResp.StatusCode)
	}
}

// --- helpers ---

func waitForAttachmentCount(t *testing.T, db *sql.DB, eventID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM event_attachments WHERE event_id = ?`, eventID).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("event %s did not reach %d attachments", eventID, want)
}
