package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testToken = "Bearer gpat_test_admin_token"

// newTestServer creates a test server with seeded data.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db := openTestSQLite(t)
	seedIntegrationFixture(t, db)

	router := NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{}))
	return httptest.NewServer(router)
}

func seedIntegrationFixture(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('test-org-id', 'my-org', 'My Organization')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO teams (id, organization_id, slug, name) VALUES ('test-team-id', 'test-org-id', 'backend', 'Backend Team')`); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects (id, organization_id, team_id, slug, name, platform, status) VALUES ('test-proj-id', 'test-org-id', 'test-team-id', 'my-project', 'My Project', 'python', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label) VALUES ('key-test', 'test-proj-id', 'test-public-key', 'active', 'Default')`); err != nil {
		t.Fatalf("seed project key: %v", err)
	}

	insertSQLiteGroup(t, db, "1", "ValueError: invalid literal", "app.main in handler", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-1", "1", "ValueError: invalid literal", "error")
	insertSQLiteEvent(t, db, "evt-2", "1", "ValueError: invalid literal", "error")
	insertSQLiteReleaseWithOrg(t, db, "rel-1", "1.0.0")

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE events SET message = ?, platform = ?, culprit = ?, occurred_at = ? WHERE event_id = 'evt-1'`, "invalid literal for int()", "python", "app.main in handler", now); err != nil {
		t.Fatalf("update evt-1: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET message = ?, platform = ?, culprit = ?, occurred_at = ? WHERE event_id = 'evt-2'`, "another occurrence", "python", "app.main in handler", now); err != nil {
		t.Fatalf("update evt-2: %v", err)
	}
}

func insertSQLiteReleaseWithOrg(t *testing.T, db *sql.DB, id, version string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO releases (id, organization_id, version, date_released, created_at)
		 VALUES (?, 'test-org-id', ?, ?, ?)`,
		id, version, now, now,
	); err != nil {
		t.Fatalf("insert release %s: %v", version, err)
	}
}

func authGet(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func noAuthGet(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func authPost(t *testing.T, ts *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func authPut(t *testing.T, ts *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("PUT", ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Auth tests
// ---------------------------------------------------------------------------

func TestAuth_RequiredOnAllEndpoints(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	endpoints := []string{
		"/api/0/organizations/",
		"/api/0/organizations/my-org/",
		"/api/0/projects/",
		"/api/0/projects/my-org/my-project/",
		"/api/0/organizations/my-org/projects/",
		"/api/0/organizations/my-org/teams/",
		"/api/0/organizations/my-org/releases/",
		"/api/0/projects/my-org/my-project/keys/",
		"/api/0/projects/my-org/my-project/issues/",
		"/api/0/projects/my-org/my-project/events/",
		"/api/0/issues/1/",
		"/api/0/issues/1/events/",
		"/api/0/issues/1/events/latest/",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp := noAuthGet(t, ts, ep)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 for %s without auth, got %d", ep, resp.StatusCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Organization tests
// ---------------------------------------------------------------------------

func TestListOrganizations(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var orgs []Organization
	decodeBody(t, resp, &orgs)
	if len(orgs) != 1 {
		t.Fatalf("expected 1 org, got %d", len(orgs))
	}
	if orgs[0].Slug != "my-org" {
		t.Fatalf("expected slug my-org, got %q", orgs[0].Slug)
	}

	// Check Link header exists.
	if link := resp.Header.Get("Link"); link == "" {
		t.Fatal("expected Link header on list response")
	}
}

func TestGetOrganization(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/my-org/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var org Organization
	decodeBody(t, resp, &org)
	if org.Slug != "my-org" {
		t.Fatalf("expected slug my-org, got %q", org.Slug)
	}
	if org.Name != "My Organization" {
		t.Fatalf("expected name 'My Organization', got %q", org.Name)
	}
}

func TestGetOrganization_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/nonexistent/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Project tests
// ---------------------------------------------------------------------------

func TestListAllProjects(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var projects []Project
	decodeBody(t, resp, &projects)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
}

func TestGetProject(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var proj Project
	decodeBody(t, resp, &proj)
	if proj.Slug != "my-project" {
		t.Fatalf("expected slug my-project, got %q", proj.Slug)
	}
}

func TestGetProject_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/nonexistent/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestListOrgProjects(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/my-org/projects/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var projects []Project
	decodeBody(t, resp, &projects)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
}

func TestCreateProject(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/teams/my-org/backend/projects/", map[string]string{
		"name":     "New Project",
		"slug":     "new-project",
		"platform": "go",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var proj Project
	decodeBody(t, resp, &proj)
	if proj.Slug != "new-project" {
		t.Fatalf("expected slug new-project, got %q", proj.Slug)
	}
	if proj.Platform != "go" {
		t.Fatalf("expected platform go, got %q", proj.Platform)
	}
}

func TestCreateProject_TeamNotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/teams/my-org/nonexistent/projects/", map[string]string{
		"name": "Fail",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Keys tests
// ---------------------------------------------------------------------------

func TestListKeys(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/keys/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var keys []ProjectKey
	decodeBody(t, resp, &keys)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].DSN.Public == "" {
		t.Fatal("expected DSN public to be set")
	}
	if keys[0].DSN.Minidump == "" || keys[0].DSN.Security == "" || keys[0].DSN.OTLPLogs == "" {
		t.Fatalf("expected expanded DSN URLs, got %+v", keys[0].DSN)
	}
}

func TestCreateKey(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/my-org/my-project/keys/", map[string]string{
		"label": "CI Key",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var key ProjectKey
	decodeBody(t, resp, &key)
	if key.Label != "CI Key" {
		t.Fatalf("expected label 'CI Key', got %q", key.Label)
	}
	if key.DSN.Public == "" {
		t.Fatal("expected DSN public to be set")
	}
	if key.DSN.Secret == "" {
		t.Fatal("expected DSN secret to be set")
	}
	if key.DSN.CSP == "" || key.DSN.Crons == "" || key.DSN.Unreal == "" {
		t.Fatalf("expected Sentry-style DSN sub-endpoints, got %+v", key.DSN)
	}
}

// ---------------------------------------------------------------------------
// Issue tests
// ---------------------------------------------------------------------------

func TestListProjectIssues(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/issues/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var issues []Issue
	decodeBody(t, resp, &issues)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].ShortID == "" {
		t.Fatal("expected shortId to be set")
	}
	if issues[0].ProjectRef.Slug != "my-project" {
		t.Fatalf("expected project slug my-project, got %q", issues[0].ProjectRef.Slug)
	}
	if issues[0].ProjectRef.Name != "My Project" || issues[0].ProjectRef.Platform != "python" {
		t.Fatalf("expected project ref name/platform, got %+v", issues[0].ProjectRef)
	}
	if issues[0].Count != "1" {
		t.Fatalf("expected string count 1, got %#v", issues[0].Count)
	}
}

func TestGetIssue(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/1/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var iss Issue
	decodeBody(t, resp, &iss)
	if iss.Title != "ValueError: invalid literal" {
		t.Fatalf("expected title, got %q", iss.Title)
	}
	if iss.Status != "unresolved" {
		t.Fatalf("expected status unresolved, got %q", iss.Status)
	}
	if iss.ProjectRef.Slug != "my-project" || iss.ProjectRef.Name != "My Project" || iss.ProjectRef.Platform != "python" {
		t.Fatalf("expected project ref for issue detail, got %+v", iss.ProjectRef)
	}
	if iss.Count != "1" {
		t.Fatalf("expected string count 1, got %#v", iss.Count)
	}
}

func TestGetIssue_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/9999/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUpdateIssue_Resolve(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/issues/1/", map[string]string{
		"status": "resolved",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var iss Issue
	decodeBody(t, resp, &iss)
	if iss.Status != "resolved" {
		t.Fatalf("expected status resolved, got %q", iss.Status)
	}
	if iss.ProjectRef.Name != "My Project" || iss.ProjectRef.Platform != "python" {
		t.Fatalf("expected project ref on update response, got %+v", iss.ProjectRef)
	}
	if iss.Count != "1" {
		t.Fatalf("expected string count 1, got %#v", iss.Count)
	}
}

func TestUpdateIssue_InvalidStatus(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/issues/1/", map[string]string{
		"status": "invalid",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestListIssueEvents(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/1/events/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var events []Event
	decodeBody(t, resp, &events)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestGetLatestIssueEvent(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/1/events/latest/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var evt Event
	decodeBody(t, resp, &evt)
	if evt.EventID == "" {
		t.Fatal("expected event ID to be set")
	}
	assertHasEventTag(t, evt.Tags, "environment", "production")
}

// ---------------------------------------------------------------------------
// Event tests
// ---------------------------------------------------------------------------

func TestListProjectEvents(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/events/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var events []Event
	decodeBody(t, resp, &events)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestGetProjectEvent(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// First, list events to get a valid event ID.
	listResp := authGet(t, ts, "/api/0/projects/my-org/my-project/events/")
	var events []Event
	decodeBody(t, listResp, &events)
	if len(events) == 0 {
		t.Fatal("no events found")
	}

	eid := events[0].EventID
	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/events/"+eid+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var evt Event
	decodeBody(t, resp, &evt)
	if evt.EventID != eid {
		t.Fatalf("expected event ID %q, got %q", eid, evt.EventID)
	}
}

func TestGetProjectEvent_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/my-org/my-project/events/nonexistent/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMonitorCRUD(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	createBody := map[string]any{
		"slug":        "nightly-import",
		"status":      "active",
		"environment": "production",
		"config": map[string]any{
			"schedule": map[string]any{
				"type":  "interval",
				"value": 5,
				"unit":  "minute",
			},
			"timezone": "UTC",
		},
	}
	resp := authPost(t, ts, "/api/0/projects/my-org/my-project/monitors/", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var created Monitor
	decodeBody(t, resp, &created)
	resp.Body.Close()
	if created.Slug != "nightly-import" || created.ProjectID != "test-proj-id" {
		t.Fatalf("unexpected created monitor: %+v", created)
	}

	updateBody := map[string]any{
		"status": "disabled",
		"config": map[string]any{
			"schedule": map[string]any{
				"type":    "crontab",
				"crontab": "*/10 * * * *",
			},
			"timezone": "UTC",
		},
	}
	resp = authPut(t, ts, "/api/0/projects/my-org/my-project/monitors/nightly-import/", updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", resp.StatusCode)
	}
	var updated Monitor
	decodeBody(t, resp, &updated)
	resp.Body.Close()
	if updated.Status != "disabled" || updated.Config.Schedule.Type != "crontab" {
		t.Fatalf("unexpected updated monitor: %+v", updated)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/0/projects/my-org/my-project/monitors/nightly-import/", nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Release tests
// ---------------------------------------------------------------------------

func TestListReleases(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/my-org/releases/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var releases []Release
	decodeBody(t, resp, &releases)
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	if releases[0].Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %q", releases[0].Version)
	}
}

func TestCreateRelease(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/organizations/my-org/releases/", map[string]string{
		"version": "2.0.0",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var rel Release
	decodeBody(t, resp, &rel)
	if rel.Version != "2.0.0" {
		t.Fatalf("expected version 2.0.0, got %q", rel.Version)
	}
}

func TestCreateRelease_MissingVersion(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/organizations/my-org/releases/", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Team tests
// ---------------------------------------------------------------------------

func TestListTeams(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/my-org/teams/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var teams []Team
	decodeBody(t, resp, &teams)
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}
	if teams[0].Slug != "backend" {
		t.Fatalf("expected slug backend, got %q", teams[0].Slug)
	}
}

func TestListTeams_OrgNotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/nonexistent/teams/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Pagination Link header test
// ---------------------------------------------------------------------------

func TestPagination_LinkHeader(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	link := resp.Header.Get("Link")
	if link == "" {
		t.Fatal("expected Link header to be present")
	}
	if !containsStr(link, `rel="previous"`) {
		t.Fatalf("expected previous link in %q", link)
	}
	if !containsStr(link, `rel="next"`) {
		t.Fatalf("expected next link in %q", link)
	}
}
