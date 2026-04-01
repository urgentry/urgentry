//go:build integration

package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestMonitorCheckInViaEnvelope sends a check_in envelope item and verifies
// the monitor is auto-created in the database.
func TestMonitorCheckInViaEnvelope(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-envelope-aaa-001",
		"monitor_slug": "envelope-cron",
		"status":       "ok",
		"duration":     1.0,
		"environment":  "production",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "*/5 * * * *",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{
			"event_id": "m1m1m1m1m1m1m1m1m1m1m1m1m1m1m1m1",
			"dsn":      "https://abc123@o1.ingest.example.com/1",
		},
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

	// Verify the check-in was persisted.
	waitForCheckIn(t, srv.db, "default-project", "envelope-cron")

	// Verify the monitor itself was auto-created.
	var monitorCount int
	err := srv.db.QueryRow(`SELECT COUNT(*) FROM monitors WHERE project_id = ? AND slug = ?`, "default-project", "envelope-cron").Scan(&monitorCount)
	if err != nil {
		t.Fatalf("query monitors: %v", err)
	}
	if monitorCount != 1 {
		t.Fatalf("expected 1 monitor, got %d", monitorCount)
	}
}

// TestMonitorCheckInOK sends a check_in with status "ok" and verifies it is stored.
func TestMonitorCheckInOK(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-ok-001",
		"monitor_slug": "ok-cron",
		"status":       "ok",
		"environment":  "production",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "0 * * * *",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "m2m2m2m2m2m2m2m2m2m2m2m2m2m2m2m2"},
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

	waitForCheckIn(t, srv.db, "default-project", "ok-cron")

	// Verify the stored status is "ok".
	var status string
	err := srv.db.QueryRow(`SELECT status FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, "default-project", "ok-cron").Scan(&status)
	if err != nil {
		t.Fatalf("query check-in: %v", err)
	}
	if status != "ok" {
		t.Fatalf("check-in status = %q, want %q", status, "ok")
	}
}

// TestMonitorCheckInError sends a check_in with status "error" and verifies it is stored.
func TestMonitorCheckInError(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-error-001",
		"monitor_slug": "error-cron",
		"status":       "error",
		"environment":  "production",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "0 3 * * *",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "m3m3m3m3m3m3m3m3m3m3m3m3m3m3m3m3"},
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

	waitForCheckIn(t, srv.db, "default-project", "error-cron")

	var status string
	err := srv.db.QueryRow(`SELECT status FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, "default-project", "error-cron").Scan(&status)
	if err != nil {
		t.Fatalf("query check-in: %v", err)
	}
	if status != "error" {
		t.Fatalf("check-in status = %q, want %q", status, "error")
	}
}

// TestMonitorCheckInInProgress sends a check_in with status "in_progress" and verifies it is stored.
func TestMonitorCheckInInProgress(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-inprogress-001",
		"monitor_slug": "progress-cron",
		"status":       "in_progress",
		"environment":  "staging",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":  "interval",
				"value": 10,
				"unit":  "minute",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "m4m4m4m4m4m4m4m4m4m4m4m4m4m4m4m4"},
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

	waitForCheckIn(t, srv.db, "default-project", "progress-cron")

	var status string
	err := srv.db.QueryRow(`SELECT status FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, "default-project", "progress-cron").Scan(&status)
	if err != nil {
		t.Fatalf("query check-in: %v", err)
	}
	if status != "in_progress" {
		t.Fatalf("check-in status = %q, want %q", status, "in_progress")
	}
}

// TestMonitorCheckInDuration sends a check_in with a duration field and verifies
// the duration value is persisted.
func TestMonitorCheckInDuration(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-duration-001",
		"monitor_slug": "duration-cron",
		"status":       "ok",
		"duration":     3.5,
		"environment":  "production",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "30 2 * * *",
			},
		},
	})

	body := buildEnvelope(
		map[string]any{"event_id": "m5m5m5m5m5m5m5m5m5m5m5m5m5m5m5m5"},
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

	waitForCheckIn(t, srv.db, "default-project", "duration-cron")

	var duration float64
	err := srv.db.QueryRow(`SELECT duration FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, "default-project", "duration-cron").Scan(&duration)
	if err != nil {
		t.Fatalf("query check-in duration: %v", err)
	}
	if duration != 3.5 {
		t.Fatalf("check-in duration = %v, want 3.5", duration)
	}
}

// TestMonitorListAPI sends check-ins for two monitors, then verifies the
// GET /api/0/projects/{org}/{proj}/monitors/ API returns them.
func TestMonitorListAPI(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Send check-in for first monitor.
	body1 := buildEnvelope(
		map[string]any{"event_id": "m6m6m6m6m6m6m6m6m6m6m6m6m6m6m6m6"},
		envelopeItem{typ: "check_in", payload: jsonPayload(map[string]any{
			"check_in_id":  "ci-list-001",
			"monitor_slug": "list-cron-alpha",
			"status":       "ok",
			"duration":     1.0,
			"environment":  "production",
			"monitor_config": map[string]any{
				"schedule": map[string]any{"type": "crontab", "crontab": "0 * * * *"},
			},
		})},
	)
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body1), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope 1 status = %d, want 200", resp.StatusCode)
	}

	// Send check-in for second monitor.
	body2 := buildEnvelope(
		map[string]any{"event_id": "m7m7m7m7m7m7m7m7m7m7m7m7m7m7m7m7"},
		envelopeItem{typ: "check_in", payload: jsonPayload(map[string]any{
			"check_in_id":  "ci-list-002",
			"monitor_slug": "list-cron-beta",
			"status":       "error",
			"environment":  "production",
			"monitor_config": map[string]any{
				"schedule": map[string]any{"type": "interval", "value": 60, "unit": "minute"},
			},
		})},
	)
	resp = doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body2), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope 2 status = %d, want 200", resp.StatusCode)
	}

	// Wait for both check-ins to be persisted.
	waitForCheckIn(t, srv.db, "default-project", "list-cron-alpha")
	waitForCheckIn(t, srv.db, "default-project", "list-cron-beta")

	// Query the monitors list API.
	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/monitors/", srv.pat, nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("monitors list status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	var monitors []struct {
		Slug       string `json:"slug"`
		LastStatus string `json:"lastStatus"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode monitors: %v", err)
	}
	if len(monitors) < 2 {
		t.Fatalf("expected at least 2 monitors, got %d: %+v", len(monitors), monitors)
	}

	slugs := map[string]bool{}
	for _, m := range monitors {
		slugs[m.Slug] = true
	}
	if !slugs["list-cron-alpha"] {
		t.Fatal("missing monitor list-cron-alpha")
	}
	if !slugs["list-cron-beta"] {
		t.Fatal("missing monitor list-cron-beta")
	}
}

// TestMonitorAuthRequired verifies that the monitors API requires authentication.
func TestMonitorAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Envelope endpoint without auth header should be rejected.
	checkInPayload := jsonPayload(map[string]any{
		"check_in_id":  "ci-noauth-001",
		"monitor_slug": "noauth-cron",
		"status":       "ok",
	})
	body := buildEnvelope(
		map[string]any{"event_id": "m8m8m8m8m8m8m8m8m8m8m8m8m8m8m8m8"},
		envelopeItem{typ: "check_in", payload: checkInPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type": "application/x-sentry-envelope",
		// No X-Sentry-Auth header.
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("envelope without auth: status = %d, want 401", resp.StatusCode)
	}

	// Monitors list API without auth should be rejected.
	resp2 := doRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/monitors/", nil, nil)
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("monitors list without auth: status = %d, want 401", resp2.StatusCode)
	}
}
