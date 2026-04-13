package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

type testLifecycleStore struct {
	state    *store.InstallState
	err      error
	getCalls int
}

func (s *testLifecycleStore) GetInstallState(context.Context) (*store.InstallState, error) {
	s.getCalls++
	return s.state, s.err
}

func (s *testLifecycleStore) SyncInstallState(context.Context, store.InstallStateSync) (*store.InstallState, error) {
	return s.state, s.err
}

func (s *testLifecycleStore) SetMaintenanceMode(context.Context, bool, string, time.Time) (*store.InstallState, error) {
	return s.state, s.err
}

func TestMaintenanceBlocksAPIMutations(t *testing.T) {
	handler := Maintenance(&testLifecycleStore{
		state: &store.InstallState{
			MaintenanceMode:   true,
			MaintenanceReason: "upgrade window",
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/default-project/store/", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
	if got := resp.Header().Get("Retry-After"); got != maintenanceRetryAfter {
		t.Fatalf("Retry-After = %q, want %q", got, maintenanceRetryAfter)
	}
	var body httputil.APIErrorBody
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "maintenance_mode" {
		t.Fatalf("code = %q, want maintenance_mode", body.Code)
	}
	if !strings.Contains(body.Detail, "upgrade window") {
		t.Fatalf("body = %+v, want maintenance reason", body)
	}
}

func TestMaintenanceAllowsReadsAndAuth(t *testing.T) {
	handler := Maintenance(&testLifecycleStore{
		state: &store.InstallState{MaintenanceMode: true},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/issues/"},
		{method: http.MethodHead, path: "/healthz"},
		{method: http.MethodOptions, path: "/api/default-project/store/"},
		{method: http.MethodPost, path: "/login/"},
		{method: http.MethodPost, path: "/logout"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNoContent {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, resp.Code, http.StatusNoContent)
		}
	}
}

func TestMaintenanceCachesInstallStateForHotWritePath(t *testing.T) {
	lifecycle := &testLifecycleStore{
		state: &store.InstallState{MaintenanceMode: false},
	}
	prevNow := maintenanceNow
	defer func() { maintenanceNow = prevNow }()
	now := time.Date(2026, 4, 13, 2, 0, 0, 0, time.UTC)
	maintenanceNow = func() time.Time { return now }

	handler := Maintenance(lifecycle)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/default-project/store/", nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want %d", i+1, resp.Code, http.StatusNoContent)
		}
	}
	if lifecycle.getCalls != 1 {
		t.Fatalf("getCalls after cached requests = %d, want 1", lifecycle.getCalls)
	}

	now = now.Add(maintenanceCacheTTL + time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/api/default-project/store/", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("post-expiry status = %d, want %d", resp.Code, http.StatusNoContent)
	}
	if lifecycle.getCalls != 2 {
		t.Fatalf("getCalls after cache expiry = %d, want 2", lifecycle.getCalls)
	}
}
