package metrics

import (
	"net"
	"net/http"
	"strings"

	"urgentry/internal/httputil"
)

// Handler returns an http.HandlerFunc that serves the metrics snapshot as JSON.
// Access is restricted to localhost requests. Remote clients must provide a
// valid Bearer token matching the MetricsToken field.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhost(r.RemoteAddr) {
			if !m.hasValidAuth(r) {
				httputil.WriteError(w, http.StatusForbidden, "metrics endpoint is localhost-only")
				return
			}
		}
		snap := m.Snapshot()
		httputil.WriteJSON(w, http.StatusOK, snap)
	}
}

// isLocalhost returns true if addr is a loopback address.
func isLocalhost(addr string) bool {
	host, _, _ := net.SplitHostPort(addr)
	return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == ""
}

// hasValidAuth checks for a valid Bearer token in the Authorization header.
func (m *Metrics) hasValidAuth(r *http.Request) bool {
	if m.MetricsToken == "" {
		return false // no token configured, remote access denied
	}
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	return token != "" && token != auth && token == m.MetricsToken
}
