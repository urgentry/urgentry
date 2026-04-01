package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusOK, map[string]string{"hello": "world"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["hello"] != "world" {
		t.Fatalf("expected hello=world, got %q", body["hello"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusBadRequest, "something broke")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	if se := w.Header().Get("X-Sentry-Error"); se != "something broke" {
		t.Fatalf("expected X-Sentry-Error header, got %q", se)
	}

	var body APIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body.Detail != "something broke" {
		t.Fatalf("expected detail=something broke, got %q", body.Detail)
	}
	if body.Code != "" {
		t.Fatalf("expected empty code, got %q", body.Code)
	}
}

func TestWriteAPIError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteAPIError(w, APIError{
		Status: http.StatusTooManyRequests,
		Code:   "query_guard_blocked",
		Detail: "query denied",
	})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", w.Code)
	}
	if se := w.Header().Get("X-Sentry-Error"); se != "query denied" {
		t.Fatalf("expected X-Sentry-Error header, got %q", se)
	}

	var body APIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body.Detail != "query denied" || body.Code != "query_guard_blocked" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestWriteAPIErrorDefaults(t *testing.T) {
	w := httptest.NewRecorder()
	WriteAPIError(w, APIError{})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}

	var body APIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body.Detail != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("unexpected detail: %+v", body)
	}
}
