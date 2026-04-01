package api

import (
	"net/http"

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
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, http.StatusOK, []any{})
	}
}
