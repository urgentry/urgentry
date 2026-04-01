//go:build integration

package compat

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestStoreBasicEvent posts a basic event to /api/{project_id}/store/ and verifies
// it is accepted (200) and persisted asynchronously.
func TestStoreBasicEvent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}

	// Verify the event is persisted by the pipeline workers.
	waitForEvent(t, srv.db, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
}

// TestStoreLegacyAuth tests authentication via the X-Sentry-Auth header (DSN-based).
func TestStoreLegacyAuth(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := []byte(`{"message":"legacy auth test","event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb01"}`)

	tests := []struct {
		name   string
		header string
		status int
	}{
		{
			name:   "full sentry auth header",
			header: "Sentry sentry_key=" + srv.projectKey + ",sentry_version=7,sentry_client=test/1.0",
			status: http.StatusOK,
		},
		{
			name:   "minimal sentry auth header",
			header: "Sentry sentry_key=" + srv.projectKey,
			status: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": tt.header,
			})
			resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
		})
	}
}

// TestStoreQueryStringAuth tests authentication via ?sentry_key=xxx query parameter.
func TestStoreQueryStringAuth(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := []byte(`{"message":"query string auth","event_id":"cccccccccccccccccccccccccccccc01"}`)

	urlWithKey := srv.server.URL + "/api/default-project/store/?sentry_key=" + srv.projectKey
	resp := doRequest(t, http.MethodPost, urlWithKey, bytes.NewReader(payload), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}
}

// TestStoreCORSOptions verifies OPTIONS preflight on the store endpoint returns
// proper CORS headers (Access-Control-Allow-Origin, Allow-Methods, Allow-Headers).
func TestStoreCORSOptions(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodOptions, srv.server.URL+"/api/default-project/store/", nil, map[string]string{
		"Origin":                        "https://example.com",
		"Access-Control-Request-Method": "POST",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200", resp.StatusCode)
	}

	checks := []struct {
		header string
		want   string
	}{
		{"Access-Control-Allow-Origin", "*"},
		{"Access-Control-Allow-Methods", "POST"},
		{"Access-Control-Allow-Headers", "x-sentry-auth"},
	}

	for _, c := range checks {
		got := resp.Header.Get(c.header)
		if got == "" {
			t.Fatalf("missing header %s", c.header)
		}
		if !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
			t.Fatalf("%s = %q, want it to contain %q", c.header, got, c.want)
		}
	}
}

// TestStoreAuthRejection verifies invalid DSN key returns 401.
func TestStoreAuthRejection(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := []byte(`{"message":"should be rejected"}`)

	tests := []struct {
		name    string
		headers map[string]string
		url     string
	}{
		{
			name: "missing auth",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			url: srv.server.URL + "/api/default-project/store/",
		},
		{
			name: "invalid sentry key in header",
			headers: map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": "Sentry sentry_key=invalid-key-12345,sentry_version=7",
			},
			url: srv.server.URL + "/api/default-project/store/",
		},
		{
			name: "invalid sentry key in query string",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			url: srv.server.URL + "/api/default-project/store/?sentry_key=invalid-key-12345",
		},
		{
			name: "bearer token not accepted for ingest",
			headers: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer " + srv.pat,
			},
			url: srv.server.URL + "/api/default-project/store/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, tt.url, bytes.NewReader(payload), tt.headers)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestStoreGzipPayload posts a gzip-compressed payload and verifies decompression works.
func TestStoreGzipPayload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	raw := fixtureBytes(t, "store", "basic_error.json")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", &buf, map[string]string{
		"Content-Type":     "application/json",
		"Content-Encoding": "gzip",
		"X-Sentry-Auth":    srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}

	waitForEvent(t, srv.db, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
}

// TestStoreDeflatePayload posts a deflate-compressed payload and verifies decompression works.
func TestStoreDeflatePayload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	raw := fixtureBytes(t, "store", "basic_error.json")

	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	if _, err := fw.Write(raw); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", &buf, map[string]string{
		"Content-Type":     "application/json",
		"Content-Encoding": "deflate",
		"X-Sentry-Auth":    srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing id field")
	}

	waitForEvent(t, srv.db, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
}

// TestStoreRateLimit verifies that when rate limited, the store endpoint returns 429
// with Retry-After and X-Sentry-Rate-Limits headers.
func TestStoreRateLimit(t *testing.T) {
	// Use a rate limit of 1 request per minute to guarantee the second request is throttled.
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 1})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")

	// First request should succeed.
	first := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	// Second request should be rate-limited.
	second := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer second.Body.Close()

	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header on 429 response")
	}
	if second.Header.Get("X-Sentry-Rate-Limits") == "" {
		t.Fatal("missing X-Sentry-Rate-Limits header on 429 response")
	}
}

// TestStoreEmptyBody verifies that POSTing an empty body returns 400.
func TestStoreEmptyBody(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(""), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	if resp.Header.Get("X-Sentry-Error") == "" {
		t.Fatal("missing X-Sentry-Error header on empty body response")
	}
}

// TestStoreMalformedJSON verifies that posting invalid JSON returns 400.
func TestStoreMalformedJSON(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	tests := []struct {
		name string
		body string
	}{
		{name: "truncated object", body: `{"message": "hello`},
		{name: "not json", body: `this is not json at all`},
		{name: "array instead of object", body: `[1, 2, 3]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(tt.body), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}

			if resp.Header.Get("X-Sentry-Error") == "" {
				t.Fatal("missing X-Sentry-Error header on malformed JSON response")
			}
		})
	}
}

// TestStoreEventIDReturned verifies the response body contains an event_id.
func TestStoreEventIDReturned(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	tests := []struct {
		name    string
		payload string
		wantID  string
	}{
		{
			name:    "explicit event_id",
			payload: `{"event_id":"dddddddddddddddddddddddddddddd01","message":"with id"}`,
			wantID:  "dddddddddddddddddddddddddddddd01",
		},
		{
			name:    "no event_id generates one",
			payload: `{"message":"without id"}`,
			wantID:  "", // any non-empty string is acceptable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(tt.payload), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
			}

			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			gotID := result["id"]
			if gotID == "" {
				t.Fatal("response missing id field")
			}
			if tt.wantID != "" && gotID != tt.wantID {
				t.Fatalf("id = %q, want %q", gotID, tt.wantID)
			}
		})
	}
}

// TestStoreConcurrent fires multiple concurrent POSTs and verifies all succeed.
func TestStoreConcurrent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 600})
	defer srv.close()

	const concurrency = 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			payload := []byte(`{"message":"concurrent test"}`)
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				errs <- &storeError{status: resp.StatusCode, body: string(body)}
				return
			}

			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				errs <- err
				return
			}
			if result["id"] == "" {
				errs <- &storeError{status: 200, body: "response missing id"}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent request failed: %v", err)
	}

	// Wait for all events to be persisted.
	waitForProjectEventCount(t, srv.db, "default-project", concurrency)
}

// storeError is a simple error type for concurrent test failures.
type storeError struct {
	status int
	body   string
}

func (e *storeError) Error() string {
	return "status=" + http.StatusText(e.status) + " body=" + e.body
}
