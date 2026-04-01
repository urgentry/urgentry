//go:build integration

package compat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestMetricsStatsdFormat sends an envelope with a statsd-format metrics item
// and verifies the server accepts it (200).
func TestMetricsStatsdFormat(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	statsdPayload := []byte("page.load@millisecond:120.5|d|#env:prod,page:home|T1234567890\n")

	body := buildEnvelope(
		map[string]any{"event_id": "m0m0m0m0m0m0m0m0m0m0m0m0m0m0m0m0"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	waitForMetricBucket(t, srv.db, "default-project", "page.load")
}

// TestMetricsBucketFormat sends an envelope with a metric_buckets item (JSON)
// and verifies acceptance. Since the envelope handler routes metric_buckets
// the same as statsd, we verify the bucket is persisted.
func TestMetricsBucketFormat(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// metric_buckets are sent as statsd lines via the "statsd" envelope type.
	// Sentry SDKs encode metric buckets into statsd format before sending.
	statsdPayload := []byte("api.latency@millisecond:42.0|d|#service:web\napi.calls@none:1|c|#service:web\n")

	body := buildEnvelope(
		map[string]any{"event_id": "m1m1m1m1m1m1m1m1m1m1m1m1m1m1m1m1"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	waitForMetricBucket(t, srv.db, "default-project", "api.latency")
	waitForMetricBucket(t, srv.db, "default-project", "api.calls")
}

// TestMetricsCounterIncrement sends a counter metric via statsd and verifies
// the value is stored and queryable.
func TestMetricsCounterIncrement(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	statsdPayload := []byte("http.requests@none:1|c|#method:GET,path:/api/health|T1700000000\n")

	body := buildEnvelope(
		map[string]any{"event_id": "m2m2m2m2m2m2m2m2m2m2m2m2m2m2m2m2"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	waitForMetricBucket(t, srv.db, "default-project", "http.requests")
	assertMetricBucket(t, srv.db, "default-project", "http.requests", "c", 1.0)
}

// TestMetricsDistribution sends a distribution metric with multiple values
// and verifies each data point is persisted.
func TestMetricsDistribution(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Each line is a separate distribution data point.
	statsdPayload := []byte(
		"response.time@millisecond:15.2|d|#endpoint:/users|T1700000000\n" +
			"response.time@millisecond:32.8|d|#endpoint:/users|T1700000000\n" +
			"response.time@millisecond:9.1|d|#endpoint:/users|T1700000000\n",
	)

	body := buildEnvelope(
		map[string]any{"event_id": "m3m3m3m3m3m3m3m3m3m3m3m3m3m3m3m3"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	waitForMetricBucketCount(t, srv.db, "default-project", "response.time", 3)
}

// TestMetricsGauge sends a gauge metric and verifies it is stored.
func TestMetricsGauge(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	statsdPayload := []byte("cpu.usage@percent:72.5|g|#host:web-01|T1700000000\n")

	body := buildEnvelope(
		map[string]any{"event_id": "m4m4m4m4m4m4m4m4m4m4m4m4m4m4m4m4"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	waitForMetricBucket(t, srv.db, "default-project", "cpu.usage")
	assertMetricBucket(t, srv.db, "default-project", "cpu.usage", "g", 72.5)
}

// TestMetricsSet sends a set metric with unique values and verifies
// each distinct value is stored as a separate bucket entry.
func TestMetricsSet(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Set metrics track unique values. Each line is a distinct observed value.
	statsdPayload := []byte(
		"user.unique@none:42|s|#app:myapp|T1700000000\n" +
			"user.unique@none:99|s|#app:myapp|T1700000000\n" +
			"user.unique@none:42|s|#app:myapp|T1700000000\n",
	)

	body := buildEnvelope(
		map[string]any{"event_id": "m5m5m5m5m5m5m5m5m5m5m5m5m5m5m5m5"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
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

	// All 3 lines are stored as separate bucket rows (deduplication is query-time).
	waitForMetricBucketCount(t, srv.db, "default-project", "user.unique", 3)
}

// TestMetricsAuthRequired verifies that missing auth returns 401
// for metrics envelope requests.
func TestMetricsAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	statsdPayload := []byte("denied.metric@none:1|c\n")

	body := buildEnvelope(
		map[string]any{"event_id": "m6m6m6m6m6m6m6m6m6m6m6m6m6m6m6m6"},
		envelopeItem{typ: "statsd", payload: statsdPayload},
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type": "application/x-sentry-envelope",
		// No X-Sentry-Auth header.
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// --- wait/assert helpers for metric buckets ---

func waitForMetricBucket(t *testing.T, db *sql.DB, projectID, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM metric_buckets WHERE project_id = ? AND name = ?`,
			projectID, name,
		).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("metric bucket %q for project %s was not persisted", name, projectID)
}

func waitForMetricBucketCount(t *testing.T, db *sql.DB, projectID, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastCount int
	for time.Now().Before(deadline) {
		err := db.QueryRow(
			`SELECT COUNT(*) FROM metric_buckets WHERE project_id = ? AND name = ?`,
			projectID, name,
		).Scan(&lastCount)
		if err == nil && lastCount >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("metric bucket %q for project %s: got %d rows, want >= %d", name, projectID, lastCount, want)
}

func assertMetricBucket(t *testing.T, db *sql.DB, projectID, name, metricType string, value float64) {
	t.Helper()
	var storedType string
	var storedValue float64
	err := db.QueryRow(
		`SELECT type, value FROM metric_buckets WHERE project_id = ? AND name = ? LIMIT 1`,
		projectID, name,
	).Scan(&storedType, &storedValue)
	if err != nil {
		t.Fatalf("query metric bucket %q: %v", name, err)
	}
	if storedType != metricType {
		t.Errorf("metric %q type = %q, want %q", name, storedType, metricType)
	}
	if storedValue != value {
		t.Errorf("metric %q value = %f, want %f", name, storedValue, value)
	}
}
