package ingest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWALMonitorNoOp verifies that a nil walPath monitor never blocks.
func TestWALMonitorNoOp(t *testing.T) {
	m := NewWALMonitor("", defaultWALSizeLimitBytes)
	if m.WALSizeExceeded() {
		t.Fatal("empty walPath monitor should never exceed")
	}
}

// TestWALMonitorNilSafe verifies that a nil *WALMonitor never panics.
func TestWALMonitorNilSafe(t *testing.T) {
	var m *WALMonitor
	if m.WALSizeExceeded() {
		t.Fatal("nil WALMonitor should never exceed")
	}
}

// TestWALMonitorMissingFile verifies that a missing WAL file is treated as
// not exceeded (fresh database has no WAL yet).
func TestWALMonitorMissingFile(t *testing.T) {
	m := NewWALMonitor("/nonexistent/path/urgentry.db-wal", defaultWALSizeLimitBytes)
	if m.WALSizeExceeded() {
		t.Fatal("missing WAL file should not be treated as exceeded")
	}
}

// TestWALMonitorBelowLimit verifies that a small WAL file is not exceeded.
func TestWALMonitorBelowLimit(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	if err := os.WriteFile(walPath, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWALMonitor(walPath, defaultWALSizeLimitBytes)
	if m.WALSizeExceeded() {
		t.Fatal("1 KB WAL should not exceed 500 MB limit")
	}
}

// TestWALMonitorExceeded verifies that a WAL file over the limit is detected.
func TestWALMonitorExceeded(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	// Write 10 bytes; use a 5-byte limit so it always triggers.
	if err := os.WriteFile(walPath, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWALMonitor(walPath, 5)
	if !m.WALSizeExceeded() {
		t.Fatal("10-byte WAL should exceed 5-byte limit")
	}
}

// TestWALMonitorCaching verifies the 5-second cache is respected: even after
// the file shrinks, the cached positive result persists until the TTL expires.
func TestWALMonitorCaching(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	if err := os.WriteFile(walPath, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWALMonitor(walPath, 5)
	if !m.WALSizeExceeded() {
		t.Fatal("should be exceeded initially")
	}

	// Shrink the file below the limit.
	if err := os.WriteFile(walPath, make([]byte, 1), 0o644); err != nil {
		t.Fatal(err)
	}

	// The cached value (exceeded=true) should still be returned within the TTL.
	if !m.WALSizeExceeded() {
		t.Fatal("cached exceeded result should still be returned within TTL")
	}
}

// TestWALMonitorCacheExpiry verifies that the cache expires after the interval
// and picks up the new file size.
func TestWALMonitorCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	if err := os.WriteFile(walPath, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWALMonitor(walPath, 5)
	// First call populates cache as exceeded.
	if !m.WALSizeExceeded() {
		t.Fatal("should be exceeded initially")
	}

	// Shrink the file and force the cache to expire by backdating lastChecked.
	if err := os.WriteFile(walPath, make([]byte, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-(walCheckInterval + time.Second)).UnixNano()
	m.lastChecked.Store(expired)

	// Next call should re-stat and find the file below limit.
	if m.WALSizeExceeded() {
		t.Fatal("after cache expiry with small file, should not be exceeded")
	}
}

// TestWALMonitorFromEnvNoDataDir verifies nil is returned when dataDir is empty.
func TestWALMonitorFromEnvNoDataDir(t *testing.T) {
	m := NewWALMonitorFromEnv("")
	if m != nil {
		t.Fatal("NewWALMonitorFromEnv with empty dataDir should return nil")
	}
}

// TestWALMonitorFromEnvCustomLimit verifies URGENTRY_WAL_SIZE_LIMIT is parsed.
func TestWALMonitorFromEnvCustomLimit(t *testing.T) {
	t.Setenv("URGENTRY_WAL_SIZE_LIMIT", "1024")
	dir := t.TempDir()
	m := NewWALMonitorFromEnv(dir)
	if m == nil {
		t.Fatal("expected non-nil WALMonitor")
	}
	if m.limitBytes != 1024 {
		t.Fatalf("limitBytes = %d, want 1024", m.limitBytes)
	}
}

// TestEnvelopeHandlerWALCircuitBreaker verifies that the envelope handler returns
// 503 with Retry-After: 30 when the WAL monitor signals overload.
func TestEnvelopeHandlerWALCircuitBreaker(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	// 10 bytes, limit 5 bytes — always exceeded.
	if err := os.WriteFile(walPath, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	monitor := NewWALMonitor(walPath, 5)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		WALMonitor: monitor,
	})

	// Minimal valid envelope: header line + empty items.
	body := []byte("{\"event_id\":\"aabbccdd11223344aabbccdd11223344\"}\n")
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want \"30\"", got)
	}
}

// TestEnvelopeHandlerWALCircuitBreakerPassthrough verifies that requests pass
// through normally when the WAL is below the limit.
func TestEnvelopeHandlerWALCircuitBreakerPassthrough(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "urgentry.db-wal")
	// 1 byte, limit 1 MB — never exceeded.
	if err := os.WriteFile(walPath, make([]byte, 1), 0o644); err != nil {
		t.Fatal(err)
	}

	monitor := NewWALMonitor(walPath, 1<<20)
	handler := EnvelopeHandlerWithDeps(IngestDeps{
		WALMonitor: monitor,
	})

	body := []byte("{\"event_id\":\"aabbccdd11223344aabbccdd11223344\"}\n")
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
