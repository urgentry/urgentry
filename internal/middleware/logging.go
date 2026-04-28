package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/requestmeta"
	"urgentry/pkg/id"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type contextKey int

const requestIDKey contextKey = iota

// RequestID extracts the request ID from the context, or returns "".
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// LogFromCtx returns a zerolog.Logger enriched with the request_id from context.
func LogFromCtx(ctx context.Context) zerolog.Logger {
	l := log.Logger
	if reqID := RequestID(ctx); reqID != "" {
		l = l.With().Str("request_id", reqID).Logger()
	}
	return l
}

// RequestLogging is middleware that logs every HTTP request with structured fields:
// method, path, status, latency, bytes written, client IP, and request ID.
// Sentry auth keys in the query string are redacted.
func RequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := id.New()[:8]

		// Wrap response writer to capture status code and bytes written.
		rw := &responseWriter{ResponseWriter: w, statusCode: 200}

		// Add request ID to context and response header.
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		w.Header().Set("X-Request-ID", requestID)

		next.ServeHTTP(rw, r.WithContext(ctx))

		// Build log event.
		evt := log.Info().
			Str("request_id", requestID).
			Str("method", r.Method).
			Str("path", loggedPath(r.URL.Path)).
			Int("status", rw.statusCode).
			Int("bytes", rw.bytesWritten).
			Dur("latency", time.Since(start)).
			Str("ip", clientIP(r))

		// Redact sentry auth key if present.
		if sentryKey := extractSentryKey(r); sentryKey != "" {
			evt = evt.Str("sentry_key", redact(sentryKey))
		}

		evt.Msg("request")
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
	wroteHeader  bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

func clientIP(r *http.Request) string {
	return requestmeta.ClientIP(r)
}

func loggedPath(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 6 && parts[1] == "api" && parts[2] == "0" && parts[3] == "invites" && parts[5] == "accept" && strings.TrimSpace(parts[4]) != "" {
		parts[4] = "[redacted]"
		redacted := strings.Join(parts, "/")
		if strings.HasSuffix(path, "/") {
			return redacted + "/"
		}
		return redacted
	}
	return path
}

// extractSentryKey pulls the sentry_key from the query string or X-Sentry-Auth header.
func extractSentryKey(r *http.Request) string {
	if key := r.URL.Query().Get("sentry_key"); key != "" {
		return key
	}
	auth := r.Header.Get("X-Sentry-Auth")
	if auth == "" {
		return ""
	}
	for _, part := range strings.Split(auth, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "sentry_key=") {
			return strings.TrimPrefix(part, "sentry_key=")
		}
	}
	return ""
}

// redact returns a redacted version of a key, showing only the first 4 and last 4 chars.
func redact(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
