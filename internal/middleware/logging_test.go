package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestRequestLogging_SetsRequestID(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := RequestID(r.Context())
		if reqID == "" {
			t.Error("request ID should be set in context")
		}
		if len(reqID) != 8 {
			t.Errorf("request ID length = %d, want 8", len(reqID))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := RequestLogging(inner)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header should be set")
	}
}

func TestRequestLogging_CapturesStatus(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	handler := RequestLogging(inner)
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRequestLogging_RedactsInviteAcceptTokenPath(t *testing.T) {
	var output bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&output)
	t.Cleanup(func() { log.Logger = previous })

	handler := RequestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/0/invites/ginvite_secret-token-value/accept/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	logged := output.String()
	if strings.Contains(logged, "ginvite_secret-token-value") {
		t.Fatalf("expected invite token to be redacted from logs: %s", logged)
	}
	if !strings.Contains(logged, `"/api/0/invites/[redacted]/accept/"`) {
		t.Fatalf("expected redacted invite accept path in logs: %s", logged)
	}
}

func TestLogFromCtx_WithoutRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	l := LogFromCtx(req.Context())
	// Should not panic, just return a logger without request_id.
	l.Info().Msg("test log without request ID")
}

func TestRedact(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abcdefghijklmnop", "abcd...mnop"},
		{"short", "****"},
		{"12345678", "****"},
		{"123456789", "1234...6789"},
	}
	for _, tt := range tests {
		got := redact(tt.input)
		if got != tt.want {
			t.Errorf("redact(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractSentryKey_QueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/1/store/?sentry_key=abc123", nil)
	key := extractSentryKey(req)
	if key != "abc123" {
		t.Errorf("extractSentryKey = %q, want %q", key, "abc123")
	}
}

func TestExtractSentryKey_Header(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/1/store/", nil)
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key=mykey123, sentry_client=test/1.0")
	key := extractSentryKey(req)
	if key != "mykey123" {
		t.Errorf("extractSentryKey = %q, want %q", key, "mykey123")
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 70.41.3.18")
	ip := clientIP(req)
	if ip != "203.0.113.50" {
		t.Errorf("clientIP = %q, want %q", ip, "203.0.113.50")
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	ip := clientIP(req)
	if ip != "192.168.1.1:12345" {
		t.Errorf("clientIP = %q, want %q", ip, "192.168.1.1:12345")
	}
}

func TestLoggedPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "invite accept", path: "/api/0/invites/ginvite_secret-token-value/accept/", want: "/api/0/invites/[redacted]/accept/"},
		{name: "invite accept no trailing slash", path: "/api/0/invites/ginvite_secret-token-value/accept", want: "/api/0/invites/[redacted]/accept"},
		{name: "other path", path: "/api/0/issues/grp-1/", want: "/api/0/issues/grp-1/"},
	}
	for _, tt := range tests {
		if got := loggedPath(tt.path); got != tt.want {
			t.Fatalf("%s: loggedPath(%q) = %q, want %q", tt.name, tt.path, got, tt.want)
		}
	}
}
