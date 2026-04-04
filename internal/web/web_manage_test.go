package web

import (
	"database/sql"
	"net/http"
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
