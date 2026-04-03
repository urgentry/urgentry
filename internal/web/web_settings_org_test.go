package web

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestOrgSettingsPages(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed a member so the members page has data.
	if _, err := db.Exec(`INSERT OR IGNORE INTO users (id, email, display_name, is_active) VALUES ('u-org-1', 'alice@example.com', 'Alice', 1)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('mem-1', 'test-org', 'u-org-1', 'member', datetime('now'))`); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	// Seed a team.
	if _, err := db.Exec(`INSERT OR IGNORE INTO teams (id, organization_id, slug, name, created_at) VALUES ('team-1', 'test-org', 'backend', 'Backend', datetime('now'))`); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	routes := []struct {
		path    string
		contain string
	}{
		{"/settings/org/", "Organization Settings"},
		{"/settings/org/members/", "Members"},
		{"/settings/org/teams/", "Teams"},
		{"/settings/org/auth/", "OIDC"},
		{"/settings/org/audit-log/", "Audit Log"},
	}

	for _, tc := range routes {
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body := getBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body: %s", tc.path, resp.StatusCode, body)
			continue
		}
		if !strings.Contains(body, tc.contain) {
			t.Errorf("GET %s: expected %q in body", tc.path, tc.contain)
		}
	}
}

func TestOrgSettingsMembersShowsSeededData(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	if _, err := db.Exec(`INSERT OR IGNORE INTO users (id, email, display_name, is_active) VALUES ('u-member-1', 'bob@example.com', 'Bob', 1)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('mem-bob', 'test-org', 'u-member-1', 'admin', datetime('now'))`); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	resp, err := http.Get(srv.URL + "/settings/org/members/")
	if err != nil {
		t.Fatalf("GET /settings/org/members/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "bob@example.com") {
		t.Errorf("expected bob@example.com in members page")
	}
}

func TestOrgSettingsTeamsShowsSeededData(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	if _, err := db.Exec(`INSERT OR IGNORE INTO teams (id, organization_id, slug, name, created_at) VALUES ('team-frontend', 'test-org', 'frontend', 'Frontend', datetime('now'))`); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	resp, err := http.Get(srv.URL + "/settings/org/teams/")
	if err != nil {
		t.Fatalf("GET /settings/org/teams/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "frontend") {
		t.Errorf("expected team slug 'frontend' in teams page")
	}
}

func TestOrgSettingsAuthWithOIDC(t *testing.T) {
	oidcStore := auth.NewMemoryOIDCConfigStore()

	srv, db, sessionToken, _ := setupAuthorizedTestServerWithDeps(t, func(db2 *sql.DB, authz *auth.Authorizer, dataDir string, deps Dependencies) Dependencies {
		var orgID string
		_ = db2.QueryRow(`SELECT id FROM organizations WHERE slug = 'test-org'`).Scan(&orgID)
		if orgID != "" {
			_ = oidcStore.SaveOIDCConfig(t.Context(), &auth.OIDCOrgConfig{
				OrganizationID: orgID,
				Issuer:         "https://accounts.example.com",
				ClientID:       "client-abc",
				Enabled:        true,
			})
		}
		deps.OIDCConfigs = oidcStore
		return deps
	})
	defer srv.Close()
	_ = db

	client := &http.Client{}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/settings/org/auth/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /settings/org/auth/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "accounts.example.com") {
		t.Errorf("expected OIDC issuer in auth page")
	}
}

func TestOrgSettingsAuditLogShowsEntries(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	auditStore := sqlite.NewOperatorAuditStore(db)
	if err := auditStore.Record(t.Context(), store.OperatorAuditRecord{
		OrganizationID: "test-org",
		Action:         "project.create",
		Source:         "api",
		Status:         "ok",
	}); err != nil {
		t.Fatalf("Record audit: %v", err)
	}

	resp, err := http.Get(srv.URL + "/settings/org/audit-log/")
	if err != nil {
		t.Fatalf("GET /settings/org/audit-log/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "project.create") {
		t.Errorf("expected audit action in page")
	}
}
