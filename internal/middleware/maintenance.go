package middleware

import (
	"net/http"
	"strings"

	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

const maintenanceRetryAfter = "60"

// Maintenance blocks product write traffic while allowing reads and operator auth
// so installs can drain safely before upgrades, restores, or migrations.
func Maintenance(lifecycle store.LifecycleStore) func(http.Handler) http.Handler {
	if lifecycle == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maintenanceAllows(r) {
				next.ServeHTTP(w, r)
				return
			}
			state, err := lifecycle.GetInstallState(r.Context())
			if err != nil || state == nil || !state.MaintenanceMode {
				next.ServeHTTP(w, r)
				return
			}

			msg := "urgentry is in maintenance mode; write operations are temporarily disabled"
			if reason := strings.TrimSpace(state.MaintenanceReason); reason != "" {
				msg += ": " + reason
			}
			w.Header().Set("Retry-After", maintenanceRetryAfter)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				httputil.WriteAPIError(w, httputil.APIError{
					Status: http.StatusServiceUnavailable,
					Code:   "maintenance_mode",
					Detail: msg,
				})
				return
			}
			http.Error(w, msg, http.StatusServiceUnavailable)
		})
	}
}

func maintenanceAllows(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	switch r.URL.Path {
	case "/login/", "/logout":
		return true
	default:
		return false
	}
}
