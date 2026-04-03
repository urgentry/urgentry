package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var noop = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestIngestCORS_Preflight(t *testing.T) {
	handler := IngestCORS(noop)

	req := httptest.NewRequest(http.MethodOptions,"/api/42/store/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "*")
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "POST, OPTIONS" {
		t.Fatalf("Allow-Methods = %q, want %q", got, "POST, OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected Allow-Headers to be set")
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Fatal("expected Expose-Headers to be set")
	}
}

func TestIngestCORS_PostResponse(t *testing.T) {
	handler := IngestCORS(noop)

	req := httptest.NewRequest(http.MethodPost,"/api/42/store/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "*")
	}
}

func TestIngestCORS_EnvelopeEndpoint(t *testing.T) {
	handler := IngestCORS(noop)

	req := httptest.NewRequest(http.MethodOptions,"/api/99/envelope/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "*")
	}
}

func TestIngestCORS_NonIngestPassthrough(t *testing.T) {
	handler := IngestCORS(noop)

	req := httptest.NewRequest(http.MethodGet,"/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS headers on non-ingest path, got Allow-Origin=%q", got)
	}
}

func TestAPICORS_AllowedOrigin(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	handler := APICORS(cfg)(noop)

	req := httptest.NewRequest(http.MethodGet,"/api/0/organizations/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "https://app.example.com")
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want %q", got, "Origin")
	}
}

func TestAPICORS_DisallowedOrigin(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	handler := APICORS(cfg)(noop)

	req := httptest.NewRequest(http.MethodGet,"/api/0/organizations/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin for disallowed origin, got %q", got)
	}
}

func TestAPICORS_Preflight(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	handler := APICORS(cfg)(noop)

	req := httptest.NewRequest(http.MethodOptions,"/api/0/projects/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected Allow-Methods to be set")
	}
}

func TestAPICORS_NonAPIPassthrough(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	handler := APICORS(cfg)(noop)

	req := httptest.NewRequest(http.MethodGet,"/healthz", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS headers on non-API path, got Allow-Origin=%q", got)
	}
}

func TestIsIngestPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/1/store/", true},
		{"/api/1/store", true},
		{"/api/99/envelope/", true},
		{"/api/99/envelope", true},
		{"/api/0/organizations/", false},
		{"/healthz", false},
	}
	for _, tt := range tests {
		if got := isIngestPath(tt.path); got != tt.want {
			t.Errorf("isIngestPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestIsAPIPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/0/organizations/", true},
		{"/api/0/projects/", true},
		{"/api/1/store/", false},
		{"/healthz", false},
	}
	for _, tt := range tests {
		if got := isAPIPath(tt.path); got != tt.want {
			t.Errorf("isAPIPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
