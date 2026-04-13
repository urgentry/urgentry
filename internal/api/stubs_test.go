package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSeerModelsPublicShape(t *testing.T) {
	db := openTestSQLite(t)
	ts, _ := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/seer/models/", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=600" {
		t.Fatalf("Cache-Control = %q, want public, max-age=600", got)
	}

	var payload struct {
		Models []any `json:"models"`
	}
	decodeBody(t, resp, &payload)
	if payload.Models == nil {
		t.Fatalf("models = nil, want empty array")
	}
	if len(payload.Models) != 0 {
		t.Fatalf("models = %+v, want empty array", payload.Models)
	}
}

func TestSeerModelsRateLimited(t *testing.T) {
	handler := handleSeerModels()
	for attempt := 0; attempt < 100; attempt++ {
		req := httptest.NewRequest(http.MethodGet, "/api/0/seer/models/", nil)
		req.RemoteAddr = "203.0.113.10:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200 before limit", attempt+1, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/0/seer/models/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on rate limited response")
	}
}
