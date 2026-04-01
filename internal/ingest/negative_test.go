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
	"testing"
)

func negativeFixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "negative")
}

func loadNegativeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(negativeFixturesDir(t), name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

// postToStore sends a POST to the store handler and returns the response.
func postToStore(t *testing.T, body []byte) *http.Response {
	t.Helper()
	handler := StoreHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

// postToEnvelope sends a POST to the envelope handler and returns the response.
func postToEnvelope(t *testing.T, body []byte) *http.Response {
	t.Helper()
	handler := EnvelopeHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result()
}

func TestNegativeMalformedJSON(t *testing.T) {
	body := loadNegativeFixture(t, "malformed_json.json")
	resp := postToStore(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNegativeDeeplyNested(t *testing.T) {
	body := loadNegativeFixture(t, "deeply_nested.json")
	resp := postToStore(t, body)
	// Must not crash. Accept either 200 or 400.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 200 or 400", resp.StatusCode)
	}
}

func TestNegativeUnicodeEdgeCases(t *testing.T) {
	body := loadNegativeFixture(t, "unicode_edge_cases.json")
	resp := postToStore(t, body)
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	// Verify the event_id is preserved.
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["id"] != "neg002neg002neg002neg002neg002ne" {
		t.Errorf("id = %q, want neg002neg002neg002neg002neg002ne", result["id"])
	}
}

func TestNegativeMissingEventID(t *testing.T) {
	body := loadNegativeFixture(t, "missing_event_id.json")
	resp := postToStore(t, body)
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Server must generate an id.
	if result["id"] == "" {
		t.Error("expected server-generated id, got empty")
	}
	if len(result["id"]) != 32 {
		t.Errorf("generated id length = %d, want 32", len(result["id"]))
	}
}

func TestNegativeFutureTimestamp(t *testing.T) {
	body := loadNegativeFixture(t, "future_timestamp.json")
	resp := postToStore(t, body)
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

func TestNegativeDuplicateEvent(t *testing.T) {
	body := loadNegativeFixture(t, "duplicate_event.json")
	// Send twice. Both should succeed (idempotent).
	resp1 := postToStore(t, body)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("first request: status = %d, want 200", resp1.StatusCode)
	}
	resp2 := postToStore(t, body)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("second request: status = %d, want 200", resp2.StatusCode)
	}
}

func TestNegativeBinaryGarbage(t *testing.T) {
	body := loadNegativeFixture(t, "binary_garbage.bin")
	resp := postToStore(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNegativeTruncatedEnvelope(t *testing.T) {
	body := loadNegativeFixture(t, "truncated_envelope.envelope")
	resp := postToEnvelope(t, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNegativeOversizedEventID(t *testing.T) {
	body := loadNegativeFixture(t, "oversized_event_id.json")
	resp := postToStore(t, body)
	// Must not crash. Accept either 200 or 400.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 200 or 400", resp.StatusCode)
	}
}

// TestNegativeNoCrash is a belt-and-suspenders test: feed every negative
// fixture through the store handler and verify no panics.
func TestNegativeNoCrash(t *testing.T) {
	dir := negativeFixturesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			// Try store handler.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("store handler panicked on %s: %v", entry.Name(), r)
					}
				}()
				postToStore(t, data)
			}()

			// Try envelope handler.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("envelope handler panicked on %s: %v", entry.Name(), r)
					}
				}()
				postToEnvelope(t, data)
			}()
		})
	}
}
