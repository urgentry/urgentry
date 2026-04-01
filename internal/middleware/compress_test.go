package middleware

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urgentry/internal/httputil"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func deflateBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate writer: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return buf.Bytes()
}

// echoHandler reads the full body and writes it back.
var echoHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
})

func TestDecompress_Gzip(t *testing.T) {
	payload := []byte("hello gzip world")
	compressed := gzipBytes(t, payload)

	req := httptest.NewRequest("POST", "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	got := w.Body.String()
	if got != string(payload) {
		t.Fatalf("body = %q, want %q", got, string(payload))
	}
}

func TestDecompress_Deflate(t *testing.T) {
	payload := []byte("hello deflate world")
	compressed := deflateBytes(t, payload)

	req := httptest.NewRequest("POST", "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "deflate")
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	got := w.Body.String()
	if got != string(payload) {
		t.Fatalf("body = %q, want %q", got, string(payload))
	}
}

func TestDecompress_NoEncoding(t *testing.T) {
	payload := []byte("plain body")

	req := httptest.NewRequest("POST", "/", bytes.NewReader(payload))
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	got := w.Body.String()
	if got != string(payload) {
		t.Fatalf("body = %q, want %q", got, string(payload))
	}
}

func TestDecompress_Identity(t *testing.T) {
	payload := []byte("identity body")

	req := httptest.NewRequest("POST", "/", bytes.NewReader(payload))
	req.Header.Set("Content-Encoding", "identity")
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	got := w.Body.String()
	if got != string(payload) {
		t.Fatalf("body = %q, want %q", got, string(payload))
	}
}

func TestDecompress_InvalidGzip(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("not valid gzip"))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body httputil.APIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "invalid_gzip" {
		t.Fatalf("error code = %q, want invalid_gzip", body.Code)
	}
}

func TestDecompress_UnsupportedEncoding(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("data"))
	req.Header.Set("Content-Encoding", "br")
	w := httptest.NewRecorder()

	Decompress(echoHandler).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body httputil.APIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "unsupported_content_encoding" {
		t.Fatalf("error code = %q, want unsupported_content_encoding", body.Code)
	}
}

func TestDecompress_ContentEncodingCleared(t *testing.T) {
	payload := []byte("check header cleared")
	compressed := gzipBytes(t, payload)

	var gotEncoding string
	inspector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	Decompress(inspector).ServeHTTP(w, req)

	if gotEncoding != "" {
		t.Fatalf("Content-Encoding should be cleared after decompression, got %q", gotEncoding)
	}
}
