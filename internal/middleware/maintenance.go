package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

const maintenanceRetryAfter = "60"
const maintenanceCacheTTL = time.Second

var maintenanceNow = func() time.Time { return time.Now().UTC() }

type maintenanceStateCache struct {
	mu       sync.Mutex
	state    *store.InstallState
	expires  time.Time
}

func (c *maintenanceStateCache) load(r *http.Request, lifecycle store.LifecycleStore) (*store.InstallState, error) {
	now := maintenanceNow()
	c.mu.Lock()
	if now.Before(c.expires) {
		state := c.state
		c.mu.Unlock()
		return state, nil
	}
	c.mu.Unlock()

	state, err := lifecycle.GetInstallState(r.Context())

	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.state = state
		c.expires = now.Add(maintenanceCacheTTL)
	}
	return state, err
}

// Maintenance blocks product write traffic while allowing reads and operator auth
// so installs can drain safely before upgrades, restores, or migrations.
func Maintenance(lifecycle store.LifecycleStore) func(http.Handler) http.Handler {
	if lifecycle == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	cache := &maintenanceStateCache{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maintenanceAllows(r) {
				next.ServeHTTP(w, r)
				return
			}
			state, err := cache.load(r, lifecycle)
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
