//go:build integration

package compat

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestSessionViaEnvelope sends a session item via envelope and verifies it is stored.
func TestSessionViaEnvelope(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-via-env-001",
		"did":     "user-1",
		"status":  "ok",
		"errors":  0,
		"started": "2024-01-01T00:00:00Z",
		"attrs": map[string]any{
			"release":     "1.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
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

	waitForSession(t, srv.db, "default-project", "1.0.0")

	// Verify the specific session row exists with the right session_id.
	var sid string
	err := srv.db.QueryRow(
		`SELECT COALESCE(session_id, '') FROM release_sessions WHERE project_id = ? AND release_version = ?`,
		"default-project", "1.0.0",
	).Scan(&sid)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if sid != "sess-via-env-001" {
		t.Fatalf("session_id = %q, want %q", sid, "sess-via-env-001")
	}
}

// TestSessionInit sends a session with init=true and verifies a new session is created.
func TestSessionInit(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-init-001",
		"did":     "user-init",
		"init":    true,
		"started": "2024-01-01T00:00:00Z",
		"status":  "ok",
		"attrs": map[string]any{
			"release":     "2.0.0",
			"environment": "staging",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
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

	waitForSession(t, srv.db, "default-project", "2.0.0")

	var count int
	err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM release_sessions WHERE project_id = ? AND session_id = ?`,
		"default-project", "sess-init-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if count < 1 {
		t.Fatalf("session with sid=sess-init-001 not found after init=true")
	}
}

// TestSessionUpdate sends a session init then an update with the same sid and verifies both stored.
func TestSessionUpdate(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// First: init session.
	initPayload := jsonPayload(map[string]any{
		"sid":     "sess-update-001",
		"did":     "user-update",
		"init":    true,
		"started": "2024-01-01T00:00:00Z",
		"status":  "ok",
		"attrs": map[string]any{
			"release":     "3.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
		envelopeItem{typ: "session", payload: initPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init status = %d, want 200", resp.StatusCode)
	}

	waitForSession(t, srv.db, "default-project", "3.0.0")

	// Second: update the same session.
	updatePayload := jsonPayload(map[string]any{
		"sid":     "sess-update-001",
		"did":     "user-update",
		"started": "2024-01-01T00:00:00Z",
		"status":  "exited",
		"attrs": map[string]any{
			"release":     "3.0.0",
			"environment": "production",
		},
	})

	body2 := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
		envelopeItem{typ: "session", payload: updatePayload},
	)

	resp2 := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body2), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", resp2.StatusCode)
	}

	// Wait for the update row to appear.
	waitForSessionCount(t, srv.db, "default-project", "3.0.0", 2)

	// Verify the update row has status "exited".
	var exitedCount int
	err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM release_sessions WHERE project_id = ? AND session_id = ? AND status = 'exited'`,
		"default-project", "sess-update-001",
	).Scan(&exitedCount)
	if err != nil {
		t.Fatalf("query exited session: %v", err)
	}
	if exitedCount < 1 {
		t.Fatal("update session with status=exited not found")
	}
}

// TestSessionExited sends a session with status "exited" and verifies it is stored.
func TestSessionExited(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-exited-001",
		"did":     "user-exited",
		"started": "2024-01-01T00:00:00Z",
		"status":  "exited",
		"attrs": map[string]any{
			"release":     "4.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
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

	waitForSession(t, srv.db, "default-project", "4.0.0")

	var status string
	err := srv.db.QueryRow(
		`SELECT status FROM release_sessions WHERE project_id = ? AND session_id = ?`,
		"default-project", "sess-exited-001",
	).Scan(&status)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "exited" {
		t.Fatalf("status = %q, want %q", status, "exited")
	}
}

// TestSessionCrashed sends a session with status "crashed" and verifies it is stored.
func TestSessionCrashed(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-crashed-001",
		"did":     "user-crashed",
		"started": "2024-01-01T00:00:00Z",
		"status":  "crashed",
		"errors":  1,
		"attrs": map[string]any{
			"release":     "5.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
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

	waitForSession(t, srv.db, "default-project", "5.0.0")

	var status string
	err := srv.db.QueryRow(
		`SELECT status FROM release_sessions WHERE project_id = ? AND session_id = ?`,
		"default-project", "sess-crashed-001",
	).Scan(&status)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if status != "crashed" {
		t.Fatalf("status = %q, want %q", status, "crashed")
	}
}

// TestSessionAggregates sends a sessions_aggregates envelope and verifies rows are stored.
func TestSessionAggregates(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	aggPayload := jsonPayload(map[string]any{
		"aggregates": []any{
			map[string]any{
				"started": "2024-01-01T00:00:00Z",
				"exited":  10,
				"crashed": 1,
			},
		},
		"attrs": map[string]any{
			"release":     "6.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
		envelopeItem{typ: "sessions", payload: aggPayload},
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

	waitForSession(t, srv.db, "default-project", "6.0.0")

	// Verify aggregate rows: should have an "exited" row (qty 10) and a "crashed" row (qty 1).
	var exitedQty, crashedQty int
	err := srv.db.QueryRow(
		`SELECT COALESCE(SUM(quantity), 0) FROM release_sessions WHERE project_id = ? AND release_version = ? AND status = 'exited'`,
		"default-project", "6.0.0",
	).Scan(&exitedQty)
	if err != nil {
		t.Fatalf("query exited aggregate: %v", err)
	}
	if exitedQty != 10 {
		t.Fatalf("exited quantity = %d, want 10", exitedQty)
	}

	err = srv.db.QueryRow(
		`SELECT COALESCE(SUM(quantity), 0) FROM release_sessions WHERE project_id = ? AND release_version = ? AND status = 'crashed'`,
		"default-project", "6.0.0",
	).Scan(&crashedQty)
	if err != nil {
		t.Fatalf("query crashed aggregate: %v", err)
	}
	if crashedQty != 1 {
		t.Fatalf("crashed quantity = %d, want 1", crashedQty)
	}
}

// TestSessionAuthRequired verifies that sessions require authentication.
func TestSessionAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-noauth-001",
		"did":     "user-noauth",
		"started": "2024-01-01T00:00:00Z",
		"status":  "ok",
		"attrs": map[string]any{
			"release":     "7.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
		envelopeItem{typ: "session", payload: sessionPayload},
	)

	// No X-Sentry-Auth header.
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type": "application/x-sentry-envelope",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSessionReleaseAssociation sends a session with a release field and verifies
// the session is associated with that release in the releases table.
func TestSessionReleaseAssociation(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	sessionPayload := jsonPayload(map[string]any{
		"sid":     "sess-release-001",
		"did":     "user-release",
		"started": "2024-01-01T00:00:00Z",
		"status":  "ok",
		"attrs": map[string]any{
			"release":     "rel-assoc-8.0.0",
			"environment": "production",
		},
	})

	body := buildEnvelope(
		map[string]any{"dsn": "https://" + srv.projectKey + "@localhost/default-project"},
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

	waitForSession(t, srv.db, "default-project", "rel-assoc-8.0.0")

	// Verify the release was auto-created in the releases table.
	var releaseCount int
	err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM releases WHERE version = ?`,
		"rel-assoc-8.0.0",
	).Scan(&releaseCount)
	if err != nil {
		t.Fatalf("query releases table: %v", err)
	}
	if releaseCount < 1 {
		t.Fatal("release rel-assoc-8.0.0 was not created in releases table")
	}

	// Verify the session row references the correct release.
	var sessionRelease string
	err = srv.db.QueryRow(
		`SELECT release_version FROM release_sessions WHERE project_id = ? AND session_id = ?`,
		"default-project", "sess-release-001",
	).Scan(&sessionRelease)
	if err != nil {
		t.Fatalf("query session release: %v", err)
	}
	if sessionRelease != "rel-assoc-8.0.0" {
		t.Fatalf("session release = %q, want %q", sessionRelease, "rel-assoc-8.0.0")
	}
}

// --- helpers ---

func waitForSessionCount(t *testing.T, db *sql.DB, projectID, release string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM release_sessions WHERE project_id = ? AND release_version = ?`,
			projectID, release,
		).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("session count for project %s release %s did not reach %d", projectID, release, want)
}
