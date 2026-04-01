package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestValidateDependenciesRequiresQueryServices(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	deps := testHandlerDeps(db, store.NewMemoryBlobStore(), dataDir, nil)
	deps.Queries = nil
	err = ValidateDependencies(deps)
	if err == nil || !strings.Contains(err.Error(), "web and query services") {
		t.Fatalf("ValidateDependencies error = %v, want query service requirement", err)
	}
}

func TestDefaultPageScopeCachesWithinRequestState(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('test-org', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj', 'test-org', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	spy := &webStoreSpy{WebStore: sqlite.NewWebStore(db)}
	deps := testHandlerDeps(db, store.NewMemoryBlobStore(), dataDir, nil)
	deps.WebStore = spy
	handler := NewHandlerWithDeps(deps)

	var got pageScope
	wrapped := withPageRequestState(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, err := handler.defaultPageScope(r.Context())
		if err != nil {
			t.Fatalf("first defaultPageScope: %v", err)
		}
		second, err := handler.defaultPageScope(r.Context())
		if err != nil {
			t.Fatalf("second defaultPageScope: %v", err)
		}
		if first != second {
			t.Fatalf("scopes differ: %+v vs %+v", first, second)
		}
		got = first
		state := pageRequestStateFromContext(r.Context())
		if state == nil {
			t.Fatal("expected page request state")
		}
		if state.metrics["default_page_scope.query"] != 1 {
			t.Fatalf("default_page_scope.query = %d, want 1", state.metrics["default_page_scope.query"])
		}
		if state.metrics["default_page_scope.cache_hit"] != 1 {
			t.Fatalf("default_page_scope.cache_hit = %d, want 1", state.metrics["default_page_scope.cache_hit"])
		}
	}))

	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/discover/", nil))

	if spy.defaultProjectCalls != 1 {
		t.Fatalf("DefaultProjectID calls = %d, want 1", spy.defaultProjectCalls)
	}
	if got.ProjectID != "test-proj" || got.ProjectSlug != "test-project" || got.OrganizationSlug != "test-org" {
		t.Fatalf("default scope = %+v", got)
	}
}

func TestMonitorsPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	store := sqlite.NewMonitorStore(db)
	if _, err := store.SaveCheckIn(t.Context(), &sqlite.MonitorCheckIn{
		ProjectID:   "test-proj",
		CheckInID:   "check-in-1",
		MonitorSlug: "nightly-import",
		Status:      "ok",
		DateCreated: time.Now().UTC(),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}

	resp, err := http.Get(srv.URL + "/monitors/")
	if err != nil {
		t.Fatalf("GET /monitors/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "nightly-import") || !strings.Contains(body, "Monitors") {
		t.Fatalf("expected monitors page content, got %s", body)
	}
}

func TestMonitorDetailPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	store := sqlite.NewMonitorStore(db)
	monitor, err := store.UpsertMonitor(t.Context(), &sqlite.Monitor{
		ProjectID:   "test-proj",
		Slug:        "nightly-import",
		Status:      "active",
		Environment: "production",
		Config: sqlite.MonitorConfig{
			Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
			Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatalf("UpsertMonitor: %v", err)
	}

	if _, err := store.SaveCheckIn(t.Context(), &sqlite.MonitorCheckIn{
		ProjectID:   "test-proj",
		MonitorSlug: "nightly-import",
		Status:      "ok",
		Duration:    1200,
		Release:     "backend@1.2.3",
		Environment: "production",
		DateCreated: time.Now().UTC().Add(-15 * time.Minute),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}
	if _, err := store.MarkMissed(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("MarkMissed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/monitors/" + monitor.ProjectID + "/" + monitor.Slug + "/")
	if err != nil {
		t.Fatalf("GET monitor detail: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Status timeline") || !strings.Contains(body, "Recent check-ins") || !strings.Contains(body, "nightly-import") {
		t.Fatalf("expected monitor detail content, got %s", body)
	}
	if !strings.Contains(body, "Run missed") || !strings.Contains(body, "backend@1.2.3") {
		t.Fatalf("expected timeline and check-in details, got %s", body)
	}
}

func padInt(i int) string {
	s := ""
	if i < 10 {
		s = "0"
	}
	return s + string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestAlertsPage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/alerts/")
	if err != nil {
		t.Fatalf("GET /alerts/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	_ = body
}

func TestFeedbackPage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/feedback/")
	if err != nil {
		t.Fatalf("GET /feedback/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	_ = body
}

func TestFeedbackPageFailsWhenStoreErrors(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	resp, err := http.Get(srv.URL + "/feedback/")
	if err != nil {
		t.Fatalf("GET /feedback/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(body, "Failed to load feedback.") {
		t.Fatalf("body = %q, want feedback error", body)
	}
}

func TestSearchAPI(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/search?q=test")
	if err != nil {
		t.Fatalf("GET /api/search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		t.Errorf("content-type = %q, want JSON", ct)
	}

	// Should be valid JSON.
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func TestSearchAPI_SearchSyntax(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-search-api-1", "TypeError: missing key", "handler.go", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES ('evt-search-api-1', 'test-proj', 'evt-search-api-1', 'grp-search-api-1', '2.0.0', 'production', 'go', 'error', 'log', 'TypeError: missing key', 'missing key', 'handler.go', ?, '{}', '{}')`,
		now,
	); err != nil {
		t.Fatalf("insert search api event: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/search?q=event.type:log")
	if err != nil {
		t.Fatalf("GET /api/search typed query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0]["id"] != "grp-search-api-1" {
		t.Fatalf("unexpected search results: %+v", result.Issues)
	}
}
