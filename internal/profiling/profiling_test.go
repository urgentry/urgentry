package profiling

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfilingAccessControl(t *testing.T) {
	routes := []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/fgprof"}
	tests := []struct {
		name       string
		token      string
		remoteAddr string
		auth       string
		wantStatus int
	}{
		{name: "localhost allowed", remoteAddr: "127.0.0.1:1234", wantStatus: http.StatusOK},
		{name: "localhost without token denied when token configured", token: "secret", remoteAddr: "127.0.0.1:1234", wantStatus: http.StatusForbidden},
		{name: "remote without token denied", remoteAddr: "203.0.113.10:4444", wantStatus: http.StatusForbidden},
		{name: "remote with token allowed", token: "secret", remoteAddr: "203.0.113.10:4444", auth: "Bearer secret", wantStatus: http.StatusOK},
		{name: "localhost with token allowed", token: "secret", remoteAddr: "127.0.0.1:1234", auth: "Bearer secret", wantStatus: http.StatusOK},
	}

	for _, route := range routes {
		route := route
		t.Run(route, func(t *testing.T) {
			for _, tc := range tests {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					mux := http.NewServeMux()
					Register(mux, tc.token)

					reqPath := route
					if route == "/debug/fgprof" {
						reqPath += "?seconds=1"
					}
					req := httptest.NewRequest(http.MethodGet, reqPath, nil)
					req.RemoteAddr = tc.remoteAddr
					if tc.auth != "" {
						req.Header.Set("Authorization", tc.auth)
					}
					rec := httptest.NewRecorder()

					mux.ServeHTTP(rec, req)

					if rec.Code != tc.wantStatus {
						t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
					}
				})
			}
		})
	}
}
