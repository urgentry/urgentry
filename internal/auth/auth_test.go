package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseSentryAuth_Valid(t *testing.T) {
	header := "Sentry sentry_key=abc123,sentry_version=7,sentry_client=sentry.python/1.0"
	key, err := ParseSentryAuth(header)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "abc123" {
		t.Fatalf("got key %q, want %q", key, "abc123")
	}
}

func TestParseSentryAuth_WithSpaces(t *testing.T) {
	header := "Sentry sentry_key=abc123, sentry_version=7, sentry_client=sentry.python/1.0"
	key, err := ParseSentryAuth(header)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "abc123" {
		t.Fatalf("got key %q, want %q", key, "abc123")
	}
}

func TestParseSentryAuth_Empty(t *testing.T) {
	_, err := ParseSentryAuth("")
	if err == nil {
		t.Fatal("expected error for empty header")
	}
}

func TestParseSentryAuth_MissingPrefix(t *testing.T) {
	_, err := ParseSentryAuth("Bearer token123")
	if err == nil {
		t.Fatal("expected error for non-Sentry header")
	}
}

func TestParseSentryAuth_MissingKey(t *testing.T) {
	_, err := ParseSentryAuth("Sentry sentry_version=7,sentry_client=sentry.python/1.0")
	if err == nil {
		t.Fatal("expected error when sentry_key is missing")
	}
}

func TestParseSentryAuth_EmptyKeyValue(t *testing.T) {
	_, err := ParseSentryAuth("Sentry sentry_key=,sentry_version=7")
	if err == nil {
		t.Fatal("expected error for empty sentry_key value")
	}
}

func TestExtractProjectID(t *testing.T) {
	tests := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{"/api/42/store/", "42", false},
		{"/api/proj-1/envelope/", "proj-1", false},
		{"/api/123/store/", "123", false},
		{"/healthz", "", true},
		{"/api//store/", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := ExtractProjectID(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("ExtractProjectID(%q): err=%v, wantErr=%v", tt.path, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ExtractProjectID(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestMemoryKeyStore_LookupKey(t *testing.T) {
	store := NewMemoryKeyStore(
		&ProjectKey{PublicKey: "key1", ProjectID: "1", Status: "active"},
		&ProjectKey{PublicKey: "key2", ProjectID: "2", Status: "disabled"},
	)

	pk, err := store.LookupKey(context.Background(), "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pk.ProjectID != "1" {
		t.Fatalf("got project ID %q, want %q", pk.ProjectID, "1")
	}

	_, err = store.LookupKey(context.Background(), "nonexistent")
	if err != ErrKeyNotFound {
		t.Fatalf("got err=%v, want ErrKeyNotFound", err)
	}
}

func TestMemoryKeyStore_AddKey(t *testing.T) {
	store := NewMemoryKeyStore()
	store.AddKey(&ProjectKey{PublicKey: "k", ProjectID: "p", Status: "active"})
	pk, err := store.LookupKey(context.Background(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pk.ProjectID != "p" {
		t.Fatalf("got project ID %q, want %q", pk.ProjectID, "p")
	}
}

// okHandler is a simple handler that writes 200 OK.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	pk := ProjectKeyFromContext(r.Context())
	if pk != nil {
		w.Header().Set("X-Project-ID", pk.ProjectID)
	}
	w.WriteHeader(http.StatusOK)
})

func testMiddleware(store KeyStore) func(http.Handler) http.Handler {
	return Middleware(store, NewFixedWindowRateLimiter(time.Minute), 60)
}

func TestMiddleware_ValidHeader(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "abc", ProjectID: "42", Status: "active"})
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/42/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=abc,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("X-Project-ID"); got != "42" {
		t.Fatalf("X-Project-ID = %q, want %q", got, "42")
	}
}

func TestMiddleware_QueryParamFallback(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "qkey", ProjectID: "7", Status: "active"})
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/7/store/?sentry_key=qkey", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("X-Project-ID"); got != "7" {
		t.Fatalf("X-Project-ID = %q, want %q", got, "7")
	}
}

func TestMiddleware_ProjectMismatch(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "abc", ProjectID: "42", Status: "active"})
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/99/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=abc,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_MissingKey(t *testing.T) {
	store := NewMemoryKeyStore()
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/1/store/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if got := w.Header().Get("X-Sentry-Error"); got == "" {
		t.Fatal("expected X-Sentry-Error header to be set")
	}
}

func TestMiddleware_UnknownKey(t *testing.T) {
	store := NewMemoryKeyStore()
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/1/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=unknown,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_DisabledKey(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "dis", ProjectID: "3", Status: "disabled"})
	handler := testMiddleware(store)(okHandler)

	req := httptest.NewRequest("POST", "/api/3/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=dis,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	body, _ := io.ReadAll(w.Body)
	if len(body) == 0 {
		t.Fatal("expected JSON error body")
	}
}

func TestMiddleware_RateLimited(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "rl", ProjectID: "9", Status: "active", RateLimit: 1})
	handler := Middleware(store, NewFixedWindowRateLimiter(time.Minute), 60)(okHandler)

	req := httptest.NewRequest("POST", "/api/9/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=rl,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", w.Code, http.StatusOK)
	}

	req = httptest.NewRequest("POST", "/api/9/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=rl,sentry_version=7")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header")
	}
	if got := w.Header().Get("X-Sentry-Rate-Limits"); got == "" {
		t.Fatal("expected X-Sentry-Rate-Limits header")
	}
}

func TestMiddleware_SuccessWithoutLimiterOmitsRateLimitHeader(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "nolimit", ProjectID: "10", Status: "active"})
	handler := Middleware(store, nil, 60)(okHandler)

	req := httptest.NewRequest("POST", "/api/10/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=nolimit,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("X-Sentry-Rate-Limits"); got != "" {
		t.Fatalf("X-Sentry-Rate-Limits = %q, want empty/absent", got)
	}
}

func TestMiddleware_SuccessWithLimiterSetsEmptyRateLimitHeader(t *testing.T) {
	store := NewMemoryKeyStore(&ProjectKey{PublicKey: "rl-ok", ProjectID: "11", Status: "active", RateLimit: 10})
	handler := Middleware(store, NewFixedWindowRateLimiter(time.Minute), 60)(okHandler)

	req := httptest.NewRequest("POST", "/api/11/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=rl-ok,sentry_version=7")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("X-Sentry-Rate-Limits"); got != "" {
		t.Fatalf("X-Sentry-Rate-Limits = %q, want empty string", got)
	}
}

func TestProjectKeyFromContext_Nil(t *testing.T) {
	pk := ProjectKeyFromContext(context.Background())
	if pk != nil {
		t.Fatalf("expected nil, got %v", pk)
	}
}
