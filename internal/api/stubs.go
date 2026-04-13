package api

import (
	"net/http"
	"time"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// handleRelayUsage handles GET /api/0/organizations/{org_slug}/relay_usage/.
// Returns relay usage statistics. In Urgentry's architecture, the ingest role
// serves as the relay equivalent — there is no separate relay service.
func handleRelayUsage(audits *sqlite.AuditStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if audits == nil {
			httputil.WriteJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		entries, err := audits.ListTrustedRelayUsage(r.Context(), PathParam(r, "org_slug"), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load trusted relay usage.")
			return
		}
		resp := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			resp = append(resp, map[string]any{
				"relay":      entry.RelayID,
				"version":    "trusted",
				"public_key": entry.RelayID,
				"first_seen": entry.FirstSeen.Format(time.RFC3339),
				"last_seen":  entry.LastSeen.Format(time.RFC3339),
			})
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
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
