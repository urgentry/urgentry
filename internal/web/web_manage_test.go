package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"urgentry/internal/auth"
)

func TestManagePagesRequireAuthAndRender(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	pages := []struct {
		path    string
		contain string
	}{
		{"/manage/", "Admin Console"},
		{"/manage/organizations/", "Organizations"},
		{"/manage/projects/", "Projects"},
		{"/manage/users/", "Users"},
		{"/manage/settings/", "Retention Settings"},
		{"/manage/status/", "Go Version"},
	}

	for _, pg := range pages {
		pg := pg
		t.Run(pg.path, func(t *testing.T) {
			// Unauthenticated → redirect.
			resp := sessionRequest(t, client, http.MethodGet, srv.URL+pg.path, "", "", "", nil)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("unauthenticated status = %d, want 303", resp.StatusCode)
			}
			resp.Body.Close()

			// Authenticated → 200 with expected content.
			resp = sessionRequest(t, client, http.MethodGet, srv.URL+pg.path, sessionToken, csrf, "", nil)
			body := getBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("authenticated status = %d, want 200; body: %s", resp.StatusCode, body)
			}
			if !strings.Contains(body, pg.contain) {
				t.Fatalf("page %s: expected %q in body", pg.path, pg.contain)
			}
		})
	}
}

func TestAdminAliasRedirectsToManage(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/admin/", "", "", "", nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login/?next=/admin/") {
		t.Fatalf("unauthenticated alias status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/admin/", sessionToken, csrf, "", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/manage/" {
		t.Fatalf("authenticated alias status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()
}

func TestManageDashboardShowsCounts(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/manage/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"Organizations", "Projects", "Users", "Database Size", "Uptime"} {
		if !strings.Contains(body, want) {
			t.Errorf("manage dashboard: missing %q", want)
		}
	}
}

func TestManageStatusShowsGoVersion(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/manage/status/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "go") {
		t.Errorf("manage status: expected go version in body")
	}
	if !strings.Contains(body, "Database") {
		t.Errorf("manage status: expected Database section in body")
	}
}

func TestManageUsersListsBootstrapUser(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/manage/users/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "owner@example.com") {
		t.Errorf("manage users: expected bootstrap user email in body")
	}
}

func TestManageOrganizationsListsOrg(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/manage/organizations/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "test-org") {
		t.Errorf("manage organizations: expected 'test-org' in body")
	}
}

func TestManageProjectsCreatesProjectWithDefaultKey(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(db *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		if _, err := db.Exec(`INSERT INTO teams (id, organization_id, slug, name) VALUES ('team-1', 'test-org', 'backend', 'Backend')`); err != nil {
			t.Fatalf("seed team: %v", err)
		}
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	form := url.Values{
		"name":     {"Mobile App"},
		"slug":     {"mobile-app"},
		"team":     {"test-org/backend"},
		"platform": {"javascript"},
	}
	resp := sessionRequest(t, client, http.MethodPost, srv.URL+"/manage/projects/", sessionToken, csrf, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if resp.StatusCode != http.StatusSeeOther {
		body := getBody(t, resp)
		t.Fatalf("create project status = %d, want 303; body: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Location"); got != "/settings/project/mobile-app/keys/" {
		t.Fatalf("create project redirect = %q, want keys page", got)
	}
	var selectedCookie bool
	for _, cookie := range resp.Cookies() {
		if cookie.Name == selectedProjectCookie && cookie.Value == "test-org/mobile-app" {
			selectedCookie = true
		}
	}
	resp.Body.Close()
	if !selectedCookie {
		t.Fatal("create project response did not set selected project cookie")
	}

	var projectID string
	if err := db.QueryRow(`SELECT id FROM projects WHERE organization_id = 'test-org' AND slug = 'mobile-app'`).Scan(&projectID); err != nil {
		t.Fatalf("created project lookup: %v", err)
	}
	var keyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_keys WHERE project_id = ? AND status = 'active'`, projectID).Scan(&keyCount); err != nil {
		t.Fatalf("created key count: %v", err)
	}
	if keyCount != 1 {
		t.Fatalf("created key count = %d, want 1", keyCount)
	}

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/api/ui/projects", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("project switcher status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"value":"test-org/mobile-app"`, `"settingsUrl":"/settings/project/mobile-app/general/"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project switcher missing %q in %s", want, body)
		}
	}
}

func TestManageSidebarLinkPresentInNav(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(_ *sql.DB, _ *auth.Authorizer, _ string, deps Dependencies) Dependencies {
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/manage/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if !strings.Contains(body, "/manage/") {
		t.Errorf("expected /manage/ link in nav sidebar")
	}
	if !strings.Contains(body, `aria-label="Admin"`) {
		t.Errorf("expected Admin nav item in sidebar")
	}
}
