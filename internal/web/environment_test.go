package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestListEnvironmentsAPI(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed events with different environments.
	now := time.Now().UTC().Format(time.RFC3339)
	insertGroup(t, db, "grp-env-1", "EnvError", "app.go", "error", "unresolved")
	for _, env := range []string{"production", "staging", "development"} {
		_, err := db.Exec(
			`INSERT INTO events (id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, environment, tags_json, payload_json)
			 VALUES (?, 'test-proj', ?, 'grp-env-1', 'error', 'EnvError', 'boom', 'go', 'app.go', ?, ?, '{}', '{}')`,
			"evt-env-"+env, "evt-env-"+env, now, env,
		)
		if err != nil {
			t.Fatalf("insert event env=%s: %v", env, err)
		}
	}

	resp, err := http.Get(srv.URL + "/api/ui/environments")
	if err != nil {
		t.Fatalf("GET /api/ui/environments: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		t.Fatalf("content-type = %q, want JSON", ct)
	}

	var result struct {
		Environments []string `json:"environments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(result.Environments) != 3 {
		t.Fatalf("environments count = %d, want 3: %v", len(result.Environments), result.Environments)
	}
	// Should be sorted alphabetically.
	if result.Environments[0] != "development" || result.Environments[1] != "production" || result.Environments[2] != "staging" {
		t.Fatalf("unexpected order: %v", result.Environments)
	}
}

func TestListEnvironmentsAPIEmpty(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ui/environments")
	if err != nil {
		t.Fatalf("GET /api/ui/environments: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result struct {
		Environments []string `json:"environments"`
		Selected     string   `json:"selected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(result.Environments) != 0 {
		t.Fatalf("environments count = %d, want 0", len(result.Environments))
	}
	if result.Selected != "" {
		t.Fatalf("selected = %q, want empty", result.Selected)
	}
}

func TestListEnvironmentsAPIReturnsSelected(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed an event to have at least one environment.
	now := time.Now().UTC().Format(time.RFC3339)
	insertGroup(t, db, "grp-env-sel", "EnvError", "app.go", "error", "unresolved")
	if _, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, environment, tags_json, payload_json)
		 VALUES ('evt-env-sel', 'test-proj', 'evt-env-sel', 'grp-env-sel', 'error', 'EnvError', 'boom', 'go', 'app.go', ?, 'production', '{}', '{}')`,
		now,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Send request with the cookie set.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/ui/environments", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_environment", Value: "production"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/ui/environments with cookie: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Environments []string `json:"environments"`
		Selected     string   `json:"selected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if result.Selected != "production" {
		t.Fatalf("selected = %q, want production", result.Selected)
	}
}

func TestSetEnvironmentCookie(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Set environment to "staging".
	form := url.Values{
		"environment": {"staging"},
		"return_to":   {"/issues/"},
	}
	resp, err := client.PostForm(srv.URL+"/settings/environment", form)
	if err != nil {
		t.Fatalf("POST /settings/environment: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/issues/" {
		t.Fatalf("redirect location = %q, want /issues/", loc)
	}

	// Check cookie was set.
	var found bool
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "urgentry_environment" {
			found = true
			if cookie.Value != "staging" {
				t.Fatalf("cookie value = %q, want staging", cookie.Value)
			}
			if cookie.Path != "/" {
				t.Fatalf("cookie path = %q, want /", cookie.Path)
			}
		}
	}
	if !found {
		t.Fatal("urgentry_environment cookie not set")
	}
}

func TestSetEnvironmentClearsCookie(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Set to "all" (clear filter).
	form := url.Values{"environment": {"all"}}
	resp, err := client.PostForm(srv.URL+"/settings/environment", form)
	if err != nil {
		t.Fatalf("POST /settings/environment: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "urgentry_environment" && cookie.Value != "all" {
			t.Fatalf("cookie value = %q, want all", cookie.Value)
		}
	}
}

func TestSetEnvironmentRedirectsToReferer(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// No return_to, no referer -> defaults to /.
	form := url.Values{"environment": {"production"}}
	resp, err := client.PostForm(srv.URL+"/settings/environment", form)
	if err != nil {
		t.Fatalf("POST /settings/environment: %v", err)
	}
	resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("redirect location = %q, want /", loc)
	}
}

func TestBaseTemplateContainsEnvSelector(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed an event with an environment so the dropdown renders.
	insertGroup(t, db, "grp-env-sel", "EnvSelTest", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-env-sel", "grp-env-sel", "EnvSelTest", "error", "env test")
	_, _ = db.Exec(`UPDATE events SET environment = 'production' WHERE event_id = 'evt-env-sel'`)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `class="topbar-btn"`) {
		t.Fatal("expected global environment button in page body")
	}
	if !strings.Contains(body, `setGlobalEnvironment`) {
		t.Fatal("expected setGlobalEnvironment JS function in page body")
	}
	if !strings.Contains(body, `>production</button>`) {
		t.Fatal("expected rendered production environment button in page body")
	}
}
