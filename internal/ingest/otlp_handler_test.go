package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/trace"
)

func TestOTLPTracesHandlerAcceptsJSON(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: sqlite.NewEventStore(db),
		Groups: sqlite.NewGroupStore(db),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.NewDurable(processor, sqlite.NewJobStore(db), 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	traceID := "0102030405060708090a0b0c0d0e0f10"
	rootID := "1111111111111111"
	body := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"traceId":"` + traceID + `","spanId":"` + rootID + `","name":"GET /checkout","kind":2,"startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}}],"status":{"code":1}}]}]}]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/traces/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	OTLPTracesHandler(pipe, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	waitForTransactionCount(t, db, "1", 1)
}

func TestOTLPTracesHandlerAcceptsGzipJSON(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: sqlite.NewEventStore(db),
		Groups: sqlite.NewGroupStore(db),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.New(processor, 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	body := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"1111111111111111","name":"GET /checkout","startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000"}]}]}]}`)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/traces/", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	OTLPTracesHandler(pipe, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestOTLPTracesHandlerRejectsInvalidIDsWithOTLPStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/traces/", bytes.NewReader([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"AQIDBAUGBwgJCgsMDQ4PEA==","spanId":"1111111111111111","name":"GET /checkout","startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000"}]}]}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	OTLPTracesHandler(nil, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["message"] == "" {
		t.Fatalf("unexpected response body: %+v", resp)
	}
}

func TestOTLPTracesHandlerRejectsUnsupportedProtobufPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/traces/", bytes.NewReader([]byte{0x0a, 0x00}))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()

	OTLPTracesHandler(nil, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-protobuf" {
		t.Fatalf("Content-Type = %q, want application/x-protobuf", got)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected protobuf status body")
	}
}

func TestOTLPTracesHandlerIsIdempotentForRetries(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: store.NewMemoryEventStore(),
		Groups: issue.NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.New(processor, 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	body := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"1111111111111111","name":"GET /checkout","startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000"}]}]}]}`)
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/traces/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		OTLPTracesHandler(pipe, nil).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	}

	waitForTransactionCount(t, db, "1", 1)
}

func TestOTLPLogsHandlerAcceptsJSON(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	seedAttachmentProjectForTest(t, db, "1")

	processor := &issue.Processor{
		Events: store.NewMemoryEventStore(),
		Groups: issue.NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: sqlite.NewTraceStore(db),
	}
	pipe := pipeline.New(processor, 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	body := []byte(`{"resourceLogs":[{"scopeLogs":[{"scope":{"name":"checkout.logger"},"logRecords":[{"timeUnixNano":"1743076800000000000","severityText":"INFO","body":{"stringValue":"cache miss"}}]}]}]}`)
	items, err := trace.TranslateOTLPLogsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPLogsJSON: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("TranslateOTLPLogsJSON len = %d, want 1", len(items))
	}
	req := httptest.NewRequest(http.MethodPost, "/api/1/otlp/v1/logs/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	OTLPLogsHandler(pipe, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
