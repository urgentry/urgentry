package ingest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"urgentry/internal/pipeline"
)

func storeFixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "store")
}

func loadStoreFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeFixturesDir(t), name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func TestStoreAllFixtures(t *testing.T) {
	fixtures := []string{
		"basic_error.json",
		"js_browser_error.json",
		"js_node_error.json",
		"go_error.json",
		"python_full_realistic.json",
		"dotnet_error.json",
		"epoch_timestamp.json",
		"java_error.json",
		"ruby_error.json",
		"tags_array_format.json",
	}

	handler := StoreHandler(nil)

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			body := loadStoreFixture(t, name)

			req := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
			}

			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if result["id"] == "" {
				t.Error("response missing id")
			}

			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestStoreMalformedJSON(t *testing.T) {
	handler := StoreHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", strings.NewReader("{not valid json}"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if resp.Header.Get("X-Sentry-Error") == "" {
		t.Error("missing X-Sentry-Error header")
	}
}

func TestStoreEmptyBody(t *testing.T) {
	handler := StoreHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", strings.NewReader(""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStoreOversizedBody(t *testing.T) {
	handler := StoreHandler(nil)

	// Create a body larger than 1MB.
	big := make([]byte, maxStoreBodySize+1)
	for i := range big {
		big[i] = 'A'
	}

	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(big))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestStoreMethodNotAllowed(t *testing.T) {
	handler := StoreHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/1/store/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestStoreReturnsEventID(t *testing.T) {
	handler := StoreHandler(nil)
	body := loadStoreFixture(t, "basic_error.json")

	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// basic_error.json has event_id = a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4
	if result["id"] != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4" {
		t.Errorf("id = %q, want a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4", result["id"])
	}
}

func TestStoreQueueFullReturns503(t *testing.T) {
	pipe := pipeline.New(nil, 1, 1)
	handler := StoreHandler(pipe)
	body := loadStoreFixture(t, "basic_error.json")

	first := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(body))
	handler.ServeHTTP(first, req1)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(body))
	handler.ServeHTTP(second, req2)
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}
