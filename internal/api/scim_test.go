package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
)

type adminWithoutSCIM struct {
	controlplane.AdminStore
}

func TestSCIMUserRoutesMountedAndCRUD(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/scim/v2/Users", pat, map[string]any{
		"userName":    "scim-user@example.com",
		"displayName": "Scim User",
		"active":      true,
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}
	var created scimUserRepr
	decodeBody(t, create, &created)
	if created.ID == "" || created.UserName != "scim-user@example.com" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	list := authzJSONRequest(t, ts, http.MethodGet, `/api/0/organizations/test-org/scim/v2/Users?filter=userName%20eq%20"scim-user@example.com"`, pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var page scimListResponse
	decodeBody(t, list, &page)
	if page.TotalResults != 1 || len(page.Resources) != 1 || page.Resources[0].ID != created.ID {
		t.Fatalf("unexpected list response: %+v", page)
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Users/"+created.ID, pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var fetched scimUserRepr
	decodeBody(t, get, &fetched)
	if fetched.DisplayName != "Scim User" {
		t.Fatalf("get displayName = %q, want %q", fetched.DisplayName, "Scim User")
	}

	patch := authzJSONRequest(t, ts, http.MethodPatch, "/api/0/organizations/test-org/scim/v2/Users/"+created.ID, pat, map[string]any{
		"schemas": []string{scimPatchSchema},
		"Operations": []map[string]any{
			{"op": "replace", "path": "displayName", "value": "SCIM Patched"},
			{"op": "replace", "path": "active", "value": false},
		},
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patch.StatusCode)
	}
	var updated scimUserRepr
	decodeBody(t, patch, &updated)
	if updated.DisplayName != "SCIM Patched" || updated.Active {
		t.Fatalf("unexpected patch response: %+v", updated)
	}
}

func TestSCIMGroupRoutesMountedAndCRUD(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/scim/v2/Groups", pat, map[string]any{
		"displayName": "SCIM Group",
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}
	var created scimGroupRepr
	decodeBody(t, create, &created)
	if created.ID == "" || created.DisplayName != "SCIM Group" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Groups/"+created.ID, pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var fetched scimGroupRepr
	decodeBody(t, get, &fetched)
	if fetched.ID != created.ID || fetched.DisplayName != "SCIM Group" {
		t.Fatalf("unexpected get response: %+v", fetched)
	}

	patch := authzJSONRequest(t, ts, http.MethodPatch, "/api/0/organizations/test-org/scim/v2/Groups/"+created.ID, pat, map[string]any{
		"schemas": []string{scimPatchSchema},
		"Operations": []map[string]any{
			{"op": "replace", "path": "displayName", "value": "SCIM Group Patched"},
		},
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patch.StatusCode)
	}
	var updated scimGroupRepr
	decodeBody(t, patch, &updated)
	if updated.DisplayName != "SCIM Group Patched" {
		t.Fatalf("unexpected patch response: %+v", updated)
	}

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/scim/v2/Groups/"+created.ID, pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()

	missing := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Groups/"+created.ID, pat, nil)
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", missing.StatusCode)
	}
	missing.Body.Close()
}

func TestSCIMRoutesRequireBearerToken(t *testing.T) {
	db := openTestSQLite(t)
	ts, _ := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	resp := doSCIMRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Users", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "application/scim+json") {
		t.Fatalf("content-type = %q, want SCIM JSON", got)
	}
	var problem scimError
	decodeBody(t, resp, &problem)
	if problem.Detail != "Bearer token required." {
		t.Fatalf("detail = %q, want bearer-required error", problem.Detail)
	}

	invalid := doSCIMRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Users", "gpat_nope", nil)
	if invalid.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status = %d, want 401", invalid.StatusCode)
	}
	invalid.Body.Close()
}

func TestSCIMRoutesRejectSessionOnlyAndNonAdminPAT(t *testing.T) {
	db := openTestSQLite(t)
	ts, _ := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	authStore := sqlite.NewAuthStore(db)
	user, err := authStore.AuthenticateUserPassword(context.Background(), "owner@example.com", "test-password-123")
	if err != nil {
		t.Fatalf("AuthenticateUserPassword: %v", err)
	}
	sessionToken, _, err := authStore.CreateSession(context.Background(), user.ID, "test-agent", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/0/organizations/test-org/scim/v2/Users", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("session request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session-only status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	limitedPAT, err := authStore.CreatePersonalAccessToken(context.Background(), user.ID, "Org Reader", []string{auth.ScopeOrgRead}, nil, "gpat_scim_org_read")
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken: %v", err)
	}
	limited := doSCIMRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Users", limitedPAT, nil)
	if limited.StatusCode != http.StatusForbidden {
		t.Fatalf("limited PAT status = %d, want 403", limited.StatusCode)
	}
	limited.Body.Close()
}

func TestSCIMRoutesRejectOtherOrganizationAdminPAT(t *testing.T) {
	db := openTestSQLite(t)
	ts, _ := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-2', 'other-org', 'Other Org')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES ('user-2', 'other@example.com', 'Other Owner', 1, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('member-2', 'org-2', 'user-2', 'owner', ?)`, now); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	authStore := sqlite.NewAuthStore(db)
	pat, err := authStore.CreatePersonalAccessToken(context.Background(), "user-2", "Other Org Admin", []string{auth.ScopeOrgAdmin}, nil, "gpat_scim_other_org")
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken: %v", err)
	}
	resp := doSCIMRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/scim/v2/Users", pat, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSCIMRoutesRemainUnmountedWithoutUserStore(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	deps := sqliteAuthorizedDependencies(t, db, Dependencies{})
	deps.SCIMUsers = nil
	deps.Control.Admin = adminWithoutSCIM{AdminStore: deps.Control.Admin}

	ts := httptest.NewServer(NewRouter(deps))
	defer ts.Close()

	resp := doSCIMRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/scim/v2/Users", "gpat_test_admin_token", map[string]any{
		"userName": "missing-store@example.com",
	})
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405", resp.StatusCode)
	}
	resp.Body.Close()
}

func doSCIMRequest(t *testing.T, ts *httptest.Server, method, path, token string, body any) *http.Response {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
	}

	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}
