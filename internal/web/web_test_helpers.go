package web

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

func testHandlerDeps(db *sql.DB, blobs store.BlobStore, dataDir string, authz *auth.Authorizer) Dependencies {
	return Dependencies{
		WebStore:       sqlite.NewWebStore(db),
		Replays:        sqlite.NewReplayStore(db, blobs),
		Queries:        telemetryquery.NewSQLiteService(db, blobs),
		DB:             db,
		BlobStore:      blobs,
		DataDir:        dataDir,
		Auth:           authz,
		Control:        controlplane.SQLiteServices(db),
		OperatorAudits: sqlite.NewOperatorAuditStore(db),
		QueryGuard:     sqlite.NewQueryGuardStore(db),
		NativeControl:  sqlite.NewNativeControlStore(db, blobs, sqlite.NewOperatorAuditStore(db)),
		Analytics:      analyticsservice.SQLiteServices(db),
	}
}

func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
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

	blobs := store.NewMemoryBlobStore()
	handler := NewHandlerWithDeps(testHandlerDeps(db, blobs, dataDir, nil))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return httptest.NewServer(mux), db
}

func setupAuthorizedTestServer(t *testing.T) (*httptest.Server, *sql.DB, string, string) {
	t.Helper()
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

	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "password123!",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	authz := auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 24*time.Hour)
	sessionToken, principal, err := authz.Login(context.Background(), bootstrap.Email, bootstrap.Password, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	blobs := store.NewMemoryBlobStore()
	handler := NewHandlerWithDeps(testHandlerDeps(db, blobs, dataDir, authz))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return httptest.NewServer(mux), db, sessionToken, principal.CSRFToken
}

func setupAuthorizedTestServerWithDeps(t *testing.T, customize func(db *sql.DB, authz *auth.Authorizer, dataDir string, deps Dependencies) Dependencies) (*httptest.Server, *sql.DB, string, string) {
	t.Helper()
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

	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "password123!",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	authz := auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 24*time.Hour)
	sessionToken, principal, err := authz.Login(context.Background(), bootstrap.Email, bootstrap.Password, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	deps := customize(db, authz, dataDir, testHandlerDeps(db, store.NewMemoryBlobStore(), dataDir, authz))
	handler := NewHandlerWithDeps(deps)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return httptest.NewServer(mux), db, sessionToken, principal.CSRFToken
}

func getBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func htmxPost(t *testing.T, url, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func sessionRequest(t *testing.T, client *http.Client, method, target, sessionToken, csrf, contentType string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	return resp
}

func insertGroup(t *testing.T, db *sql.DB, id, title, culprit, level, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
		 VALUES (?, 'test-proj', 'urgentry-v1', ?, ?, ?, ?, ?, ?, ?, 1, NULL)`,
		id, id, title, culprit, level, status, now, now,
	)
	if err != nil {
		t.Fatalf("insert group %s: %v", id, err)
	}
	_, _ = db.Exec(`UPDATE groups SET short_id = (SELECT COALESCE(MAX(short_id), 0) + 1 FROM groups) WHERE id = ? AND short_id IS NULL`, id)
}

func insertEvent(t *testing.T, db *sql.DB, eventID, groupID, title, level, message string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	tags := `{"environment":"production","browser":"Chrome"}`
	payload := `{"event_id":"` + eventID + `","exception":{"values":[{"type":"Error","value":"` + message + `","stacktrace":{"frames":[{"filename":"app.go","function":"main","lineno":42}]}}]}}`
	_, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES (?, 'test-proj', ?, ?, ?, ?, ?, 'go', 'main.go', ?, ?, ?, 'user@example.com')`,
		eventID+"-internal", eventID, groupID, level, title, message, now, tags, payload,
	)
	if err != nil {
		t.Fatalf("insert event %s: %v", eventID, err)
	}
}

type replayReadStoreSpy struct {
	base               store.ReplayReadStore
	listTimelineCalls  int
	failOnTimelineList bool
}

func (s *replayReadStoreSpy) ListReplays(ctx context.Context, projectID string, limit int) ([]store.ReplayManifest, error) {
	return s.base.ListReplays(ctx, projectID, limit)
}

func (s *replayReadStoreSpy) GetReplay(ctx context.Context, projectID, replayID string) (*store.ReplayRecord, error) {
	return s.base.GetReplay(ctx, projectID, replayID)
}

func (s *replayReadStoreSpy) ListReplayTimeline(ctx context.Context, projectID, replayID string, filter store.ReplayTimelineFilter) ([]store.ReplayTimelineItem, error) {
	s.listTimelineCalls++
	if s.failOnTimelineList {
		return nil, errors.New("unexpected replay timeline query")
	}
	return s.base.ListReplayTimeline(ctx, projectID, replayID, filter)
}

type webStoreSpy struct {
	store.WebStore
	defaultProjectCalls int
	getIssueCalls       int
	getIssuesCalls      int
}

func (s *webStoreSpy) DefaultProjectID(ctx context.Context) (string, error) {
	s.defaultProjectCalls++
	return s.WebStore.DefaultProjectID(ctx)
}

func (s *webStoreSpy) GetIssue(ctx context.Context, id string) (*store.WebIssue, error) {
	s.getIssueCalls++
	return s.WebStore.GetIssue(ctx, id)
}

func (s *webStoreSpy) GetIssues(ctx context.Context, ids []string) (map[string]store.WebIssue, error) {
	s.getIssuesCalls++
	return s.WebStore.GetIssues(ctx, ids)
}
