package profiling

import (
	"net"
	"net/http"
	netpprof "net/http/pprof"
	"strings"

	"github.com/felixge/fgprof"

	"urgentry/internal/httputil"
)

// Register mounts guarded pprof and fgprof handlers on the provided mux.
// Handlers are intended for local profiling and should remain disabled by default.
func Register(mux *http.ServeMux, token string) {
	mux.Handle("/debug/pprof/", protect(token, http.HandlerFunc(netpprof.Index)))
	mux.Handle("/debug/pprof/cmdline", protect(token, http.HandlerFunc(netpprof.Cmdline)))
	mux.Handle("/debug/pprof/profile", protect(token, http.HandlerFunc(netpprof.Profile)))
	mux.Handle("/debug/pprof/symbol", protect(token, http.HandlerFunc(netpprof.Symbol)))
	mux.Handle("/debug/pprof/trace", protect(token, http.HandlerFunc(netpprof.Trace)))

	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+name, protect(token, netpprof.Handler(name)))
	}

	mux.Handle("/debug/fgprof", protect(token, fgprof.Handler()))
}

func protect(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			if !hasValidAuth(r, token) {
				httputil.WriteError(w, http.StatusForbidden, "profiling endpoint requires a bearer token")
				return
			}
		} else if !isLocalhost(r.RemoteAddr) {
			httputil.WriteError(w, http.StatusForbidden, "profiling endpoint is localhost-only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalhost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == ""
}

func hasValidAuth(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	got := strings.TrimPrefix(auth, "Bearer ")
	return got != "" && got != auth && got == token
}
