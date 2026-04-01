//go:build integration

package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type compatDashboard struct {
	ID      string                  `json:"id"`
	Widgets []compatDashboardWidget `json:"widgets"`
}

type compatDashboardWidget struct {
	SavedSearchID string `json:"savedSearchId"`
	Query         struct {
		Dataset string `json:"dataset"`
	} `json:"query"`
}

func TestDiscoverCompatibilityHarness(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	var orgSlug, projectID string
	if err := srv.db.QueryRow(`SELECT o.slug, p.id FROM organizations o JOIN projects p ON p.organization_id = o.id ORDER BY o.id, p.id LIMIT 1`).Scan(&orgSlug, &projectID); err != nil {
		t.Fatalf("load default project: %v", err)
	}
	seedDiscoverCompatData(t, srv, projectID)

	client := loginClient(t, srv)
	csrf := csrfTokenForClient(t, client, srv.server.URL)

	saveForm := url.Values{
		"name":          {"Compat checkout"},
		"dataset":       {"transactions"},
		"query":         {"checkout"},
		"visualization": {"table"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.server.URL+"/discover/save-query", strings.NewReader(saveForm.Encode()))
	if err != nil {
		t.Fatalf("new save query request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /discover/save-query: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save query status = %d, want 303", resp.StatusCode)
	}
	redirect, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse save redirect: %v", err)
	}
	savedID := redirect.Query().Get("saved")
	if savedID == "" {
		t.Fatalf("expected saved query id in redirect, got %q", resp.Header.Get("Location"))
	}

	createDashboardBody, _ := json.Marshal(map[string]any{
		"title":       "Compat dashboard",
		"description": "Cross-project checkout view",
		"visibility":  "organization",
	})
	resp = apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/organizations/"+orgSlug+"/dashboards/", srv.pat, bytes.NewReader(createDashboardBody), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create dashboard status = %d, want 201", resp.StatusCode)
	}
	var dashboard compatDashboard
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	_ = resp.Body.Close()

	createWidgetBody, _ := json.Marshal(map[string]any{
		"title":         "Compat widget",
		"kind":          "table",
		"savedSearchId": savedID,
	})
	resp = apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/organizations/"+orgSlug+"/dashboards/"+dashboard.ID+"/widgets/", srv.pat, bytes.NewReader(createWidgetBody), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create widget status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/organizations/"+orgSlug+"/dashboards/"+dashboard.ID+"/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get dashboard status = %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		t.Fatalf("decode dashboard detail: %v", err)
	}
	_ = resp.Body.Close()
	if len(dashboard.Widgets) != 1 {
		t.Fatalf("dashboard widgets = %d, want 1", len(dashboard.Widgets))
	}
	if dashboard.Widgets[0].SavedSearchID != savedID {
		t.Fatalf("widget saved search = %q, want %q", dashboard.Widgets[0].SavedSearchID, savedID)
	}
	if dashboard.Widgets[0].Query.Dataset != "transactions" {
		t.Fatalf("widget dataset = %q, want transactions", dashboard.Widgets[0].Query.Dataset)
	}

	resp, err = client.Get(srv.server.URL + "/dashboards/" + dashboard.ID + "/")
	if err != nil {
		t.Fatalf("GET /dashboards/{id}/: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard page status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Compat widget") || !strings.Contains(body, "/traces/0123456789abcdef0123456789abcdef/") {
		t.Fatalf("unexpected dashboard page body: %s", body)
	}

	resp, err = client.Get(srv.server.URL + "/discover/?scope=issues&query=Error")
	if err != nil {
		t.Fatalf("GET /discover/: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover page status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "AllowedError") {
		t.Fatalf("expected default-org issue in body: %s", body)
	}
	if strings.Contains(body, "ForbiddenError") {
		t.Fatalf("unexpected cross-org issue in body: %s", body)
	}
}

func seedDiscoverCompatData(t *testing.T, srv *compatServer, projectID string) {
	t.Helper()

	now := time.Now().UTC()
	if _, err := srv.db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
		 VALUES ('grp-compat-1', ?, 'urgentry-v1', 'grp-compat-1', 'AllowedError', 'main.go', 'error', 'unresolved', ?, ?, 1, 201)`,
		projectID, now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert default group: %v", err)
	}
	if _, err := srv.db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-compat-err-1', ?, 'evt-compat-err-1', 'grp-compat-1', 'backend@1.2.3', 'production', 'go', 'error', 'error', 'AllowedError', 'AllowedError', 'main.go', ?, '{}', '{}'),
			('evt-compat-txn-1', ?, 'evt-compat-txn-1', 'grp-compat-1', 'backend@1.2.3', 'production', 'go', 'info', 'transaction', 'checkout', 'checkout', 'txn.go', ?, '{}', '{}')`,
		projectID, now.Add(-45*time.Minute).Format(time.RFC3339),
		projectID, now.Add(-30*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert default events: %v", err)
	}
	if _, err := srv.db.Exec(
		`INSERT INTO transactions
			(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
		 VALUES
			('txn-compat-1', ?, 'evt-compat-txn-1', '0123456789abcdef0123456789abcdef', '0123456789abcdef', '', 'checkout', 'http.server', 'ok', 'go', 'production', 'backend@1.2.3', ?, ?, 140, '{}', '{}', '{}', ?)`,
		projectID,
		now.Add(-31*time.Minute).Format(time.RFC3339Nano),
		now.Add(-30*time.Minute).Format(time.RFC3339Nano),
		now.Add(-31*time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert default transaction: %v", err)
	}
	if _, err := srv.db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('other-org', 'other-org', 'Other Org', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	if _, err := srv.db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES ('other-proj', 'other-org', 'other-project', 'Other Project', 'go', 'active', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert other project: %v", err)
	}
	if _, err := srv.db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
		 VALUES ('grp-other-compat-1', 'other-proj', 'urgentry-v1', 'grp-other-compat-1', 'ForbiddenError', 'other.go', 'error', 'unresolved', ?, ?, 1, 202)`,
		now.Add(-90*time.Minute).Format(time.RFC3339), now.Add(-80*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert outsider group: %v", err)
	}
}

func csrfTokenForClient(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == "urgentry_csrf" {
			return cookie.Value
		}
	}
	t.Fatal("missing urgentry_csrf cookie")
	return ""
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}
