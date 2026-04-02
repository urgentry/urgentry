package api

import (
	"net/http"
	"time"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/httputil"
)

// handleRelayUsage handles GET /api/0/organizations/{org_slug}/relay_usage/.
// Stub returning empty usage data.
func handleRelayUsage(auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, []any{})
	}
}

// handleReleaseThresholdStatuses handles GET /api/0/organizations/{org_slug}/release-threshold-statuses/.
// Stub returning empty threshold status data.
func handleReleaseThresholdStatuses(auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{})
	}
}

// handleSeerModels handles GET /api/0/seer/models/.
// Stub returning empty AI models list.
func handleSeerModels() http.HandlerFunc {
	limiter := authpkg.NewFixedWindowRateLimiter(time.Minute)
	return func(w http.ResponseWriter, r *http.Request) {
		if retryAfter, allowed := limiter.Allow("seer-models:"+requestClientIP(r), 100, time.Now().UTC()); !allowed {
			writeRateLimitError(w, retryAfter, "Rate limit exceeded.")
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=600")
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"models": []any{}})
	}
}
