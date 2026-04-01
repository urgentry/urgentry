package ingest

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urgentry/internal/middleware"
	"urgentry/internal/pipeline"
)

// buildEnvelope constructs a raw Sentry envelope from a header line and
// item pairs (each pair is an item-header line followed by a payload line).
func buildEnvelope(headerLine string, items ...string) []byte {
	parts := append([]string{headerLine}, items...)
	return []byte(strings.Join(parts, "\n") + "\n")
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// singleEventEnvelope returns a minimal error-event envelope.
func singleEventEnvelope() []byte {
	return buildEnvelope(
		`{"event_id":"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1","dsn":"https://key@o1.ingest.example.com/1","sent_at":"2026-03-25T12:00:00.000Z"}`,
		`{"type":"event","length":0}`,
		`{"event_id":"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1","timestamp":"2026-03-25T12:00:00Z","platform":"python","level":"error","exception":{"values":[{"type":"ValueError","value":"bad input","stacktrace":{"frames":[{"filename":"app.py","function":"handler","lineno":10,"in_app":true}]}}]}}`,
	)
}

// transactionEnvelope returns a minimal transaction envelope.
func transactionEnvelope() []byte {
	return buildEnvelope(
		`{"event_id":"txn00000000000000000000000000001","dsn":"https://key@o1.ingest.example.com/1","sent_at":"2026-03-25T12:00:00.000Z"}`,
		`{"type":"transaction","length":0}`,
		`{"event_id":"txn00000000000000000000000000001","type":"transaction","timestamp":"2026-03-25T12:00:00Z","start_timestamp":"2026-03-25T11:59:59.500Z","platform":"python","transaction":"/api/checkout","contexts":{"trace":{"trace_id":"aaaa","span_id":"bbbb","op":"http.server"}},"spans":[{"span_id":"cccc","parent_span_id":"bbbb","op":"db.query","description":"SELECT 1","start_timestamp":"2026-03-25T11:59:59.600Z","timestamp":"2026-03-25T11:59:59.800Z"}]}`,
	)
}

// multiItemEnvelope returns an envelope with an event plus an attachment.
func multiItemEnvelope() []byte {
	return buildEnvelope(
		`{"event_id":"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4","dsn":"https://key@o1.ingest.example.com/1","sent_at":"2026-03-25T12:00:00.000Z"}`,
		`{"type":"event","length":0}`,
		`{"event_id":"d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4","timestamp":"2026-03-25T12:00:00Z","platform":"javascript","level":"error","exception":{"values":[{"type":"TypeError","value":"null is not an object"}]}}`,
		`{"type":"attachment","length":23,"filename":"console.log","content_type":"text/plain"}`,
		`[ERROR] null is not obj`,
	)
}

// benchHandler returns an envelope handler with a large-capacity pipeline
// so enqueue never blocks during benchmarks. The pipeline is never started,
// so items accumulate in the buffered channel without being consumed.
func benchHandler() http.Handler {
	pipe := pipeline.New(nil, 1<<22, 1) // 4M buffer, never started
	return EnvelopeHandler(pipe)
}

func BenchmarkEnvelopeHandler_SingleEvent(b *testing.B) {
	handler := benchHandler()
	body := singleEventEnvelope()

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

func BenchmarkEnvelopeHandler_Transaction(b *testing.B) {
	handler := benchHandler()
	body := transactionEnvelope()

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

func BenchmarkEnvelopeHandler_MultiItem(b *testing.B) {
	// No pipeline — attachment-only items don't need the queue, and this
	// avoids filling the queue across iterations.
	handler := EnvelopeHandler(nil)
	body := multiItemEnvelope()

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

func BenchmarkEnvelopeHandler_Compressed(b *testing.B) {
	// Wrap the handler with Decompress middleware to measure the gzip hot path.
	handler := middleware.Decompress(benchHandler())
	raw := singleEventEnvelope()
	compressed := gzipBytes(raw)

	b.ReportAllocs()
	b.SetBytes(int64(len(raw))) // report uncompressed throughput
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(compressed))
		req.Header.Set("Content-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d", rec.Code)
		}
	}
}
