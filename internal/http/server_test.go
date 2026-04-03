package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/config"
	"urgentry/internal/httputil"
	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func newTestServer(t *testing.T) roleTestServer {
	t.Helper()
	return newRoleTestServer(t, "all")
}

func TestValidateDepsRequiresRoleSpecificRuntimeServices(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	blobs := store.NewMemoryBlobStore()
	jobStore := sqlite.NewJobStore(db)
	proc := &issue.Processor{
		Events:   sqlite.NewEventStore(db),
		Groups:   sqlite.NewGroupStore(db),
		Blobs:    blobs,
		Releases: sqlite.NewReleaseStore(db),
	}
	pipe := pipeline.NewDurable(proc, jobStore, 1, 1)
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobs, jobStore, 1)
	deps := sqliteServerDeps(t, db, dataDir, keyStore, authStore, pipe, blobs, nativeCrashes)
	cfg := config.Config{
		SessionCookieName: "urgentry_session",
		CSRFCookieName:    "urgentry_csrf",
	}

	tests := []struct {
		name string
		role string
		mut  func(*Deps)
		want string
	}{
		{
			name: "api auth store",
			role: "api",
			mut:  func(d *Deps) { d.AuthStore = nil },
			want: "auth store",
		},
		{
			name: "api nested query service",
			role: "api",
			mut:  func(d *Deps) { d.API.Queries = nil },
			want: "query service",
		},
		{
			name: "api nested web analytics",
			role: "api",
			mut:  func(d *Deps) { d.Web.Analytics.Dashboards = nil },
			want: "dashboard analytics service",
		},
		{
			name: "ingest key store",
			role: "ingest",
			mut:  func(d *Deps) { d.KeyStore = nil },
			want: "key store",
		},
		{
			name: "ingest native crashes",
			role: "ingest",
			mut:  func(d *Deps) { d.Ingest.NativeCrashes = nil },
			want: "native crash store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := deps
			tt.mut(&bad)
			err := ValidateDeps(tt.role, cfg, bad)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateDeps error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewServerPanicsOnInvalidDeps(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when runtime deps are invalid")
		}
	}()
	NewServer("api", config.Config{}, Deps{DB: db})
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	resp, err := http.Get(srv.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

func TestReadyz(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	resp, err := http.Get(srv.server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body readyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status = %v, want ready", body.Status)
	}
	// SQLite-backed test server should report a healthy database check.
	foundDB := false
	for _, c := range body.Checks {
		if c.Name == "database" {
			foundDB = true
			if c.Status != "ok" {
				t.Errorf("database check status = %q, want ok", c.Status)
			}
		}
	}
	if !foundDB {
		t.Error("readyz response missing database check")
	}
}

func TestReadyzUnhealthyDB(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	// Close the underlying database to simulate a failed dependency.
	srv.db.Close()

	resp, err := http.Get(srv.server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body readyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "unavailable" {
		t.Errorf("status = %q, want unavailable", body.Status)
	}
	for _, c := range body.Checks {
		if c.Name == "database" && c.Status != "error" {
			t.Errorf("database check status = %q, want error", c.Status)
		}
	}
}

func TestDashboardMountedWithSQLite(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()
	client, _ := loginSessionClient(t, srv)

	resp, err := client.Get(srv.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestIssueListMountedWithSQLite(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()
	client, _ := loginSessionClient(t, srv)

	resp, err := client.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStoreEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	payload := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","platform":"go","level":"error","exception":{"values":[{"type":"TestError","value":"test"}]}}`
	resp, err := http.Post(srv.server.URL+"/api/default-project/store/?sentry_key="+srv.projectKey, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/1/store/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, body)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["id"] == nil || body["id"] == "" {
		t.Error("response missing id field")
	}
}

func TestEnvelopeEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	envelope := "{\"event_id\":\"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1\",\"dsn\":\"https://abc@o1.ingest.example.com/1\"}\n" +
		"{\"type\":\"event\",\"length\":63}\n" +
		"{\"event_id\":\"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1\",\"level\":\"error\"}"

	resp, err := http.Post(srv.server.URL+"/api/default-project/envelope/?sentry_key="+srv.projectKey, "application/x-sentry-envelope", strings.NewReader(envelope))
	if err != nil {
		t.Fatalf("POST /api/1/envelope/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, body)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["id"] == nil || body["id"] == "" {
		t.Error("response missing id field")
	}
}

func TestStoreEndpointCORS(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.server.URL+"/api/default-project/store/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/1/store/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Errorf("CORS Allow-Origin = %q, want *", acao)
	}

	acam := resp.Header.Get("Access-Control-Allow-Methods")
	if !strings.Contains(acam, "POST") {
		t.Errorf("CORS Allow-Methods = %q, want to contain POST", acam)
	}
}

func TestMaintenanceModeBlocksWritesButAllowsReads(t *testing.T) {
	srv := newTestServer(t)
	defer srv.server.Close()

	lifecycle := sqlite.NewLifecycleStore(srv.db)
	if _, err := lifecycle.SetMaintenanceMode(context.Background(), true, "upgrade window", testNowUTC()); err != nil {
		t.Fatalf("SetMaintenanceMode: %v", err)
	}

	loginClient := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	loginResp, err := loginClient.Post(srv.server.URL+"/login/", "application/x-www-form-urlencoded", strings.NewReader("email="+srv.email+"&password="+srv.pass+"&next=%2Fissues%2F"))
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", loginResp.StatusCode)
	}

	readResp, err := http.Get(srv.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", readResp.StatusCode)
	}

	apiResp, err := http.Post(srv.server.URL+"/api/default-project/store/?sentry_key="+srv.projectKey, "application/json", strings.NewReader(`{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef"}`))
	if err != nil {
		t.Fatalf("POST /api/default-project/store/: %v", err)
	}
	apiBody, _ := io.ReadAll(apiResp.Body)
	apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/default-project/store/ status = %d, want 503", apiResp.StatusCode)
	}
	var apiError httputil.APIErrorBody
	if err := json.Unmarshal(apiBody, &apiError); err != nil {
		t.Fatalf("decode api maintenance error: %v", err)
	}
	if apiError.Code != "maintenance_mode" || !strings.Contains(apiError.Detail, "maintenance mode") {
		t.Fatalf("store response = %+v, want maintenance code and message", apiError)
	}

	webResp, err := http.Post(srv.server.URL+"/dashboards/", "application/x-www-form-urlencoded", strings.NewReader("title=Blocked"))
	if err != nil {
		t.Fatalf("POST /dashboards/: %v", err)
	}
	webBody, _ := io.ReadAll(webResp.Body)
	webResp.Body.Close()
	if webResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /dashboards/ status = %d, want 503", webResp.StatusCode)
	}
	if !strings.Contains(string(webBody), "maintenance mode") {
		t.Fatalf("dashboard response = %s, want maintenance message", webBody)
	}

	readyResp, err := http.Get(srv.server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz status = %d, want 200", readyResp.StatusCode)
	}
}

func testNowUTC() time.Time {
	return time.Date(2026, time.March, 30, 23, 30, 0, 0, time.UTC)
}
