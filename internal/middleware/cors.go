package middleware

import (
	"net/http"
	"strings"
)

// CORSConfig holds configuration for API CORS.
type CORSConfig struct {
	AllowedOrigins []string // for API endpoints; empty means deny
}

// IngestCORS returns middleware that applies permissive CORS for ingest
// endpoints (/api/*/store/, /api/*/envelope/). All origins are allowed.
func IngestCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isIngestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "x-sentry-auth, x-requested-with, content-type, content-encoding, authorization")
		w.Header().Set("Access-Control-Expose-Headers", "x-sentry-error, x-sentry-rate-limits, retry-after")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// APICORS returns middleware that applies configurable CORS for management
// API endpoints (/api/0/).
func APICORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAPIPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type")
			w.Header().Set("Access-Control-Expose-Headers", "link, x-sentry-rate-limits")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isIngestPath returns true for paths like /api/{id}/store/ or /api/{id}/envelope/.
func isIngestPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	return strings.HasSuffix(path, "/store") || strings.HasSuffix(path, "/envelope")
}

// isAPIPath returns true for management API paths like /api/0/...
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/0/")
}
