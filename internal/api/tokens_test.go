package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

type stubAuthStore struct{}

func (stubAuthStore) AuthenticateUserPassword(context.Context, string, string) (*auth.User, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) CreateSession(context.Context, string, string, string, time.Duration) (string, *auth.Principal, error) {
	return "", nil, errors.New("not implemented")
}
func (stubAuthStore) AuthenticateSession(context.Context, string) (*auth.Principal, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) RevokeSession(context.Context, string) error {
	return errors.New("not implemented")
}
func (stubAuthStore) AuthenticatePAT(context.Context, string) (*auth.Principal, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) AuthenticateAutomationToken(context.Context, string) (*auth.Principal, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) ResolveOrganizationBySlug(context.Context, string) (*auth.Organization, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) ResolveProjectByID(context.Context, string) (*auth.Project, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) ResolveProjectBySlug(context.Context, string, string) (*auth.Project, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) ResolveIssueProject(context.Context, string) (*auth.Project, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) ResolveEventProject(context.Context, string) (*auth.Project, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) LookupUserOrgRole(context.Context, string, string) (string, error) {
	return "", errors.New("not implemented")
}
func (stubAuthStore) ListUserOrgRoles(context.Context, string) (map[string]string, error) {
	return nil, errors.New("not implemented")
}
func (stubAuthStore) LookupUserProjectRole(context.Context, string, string) (string, error) {
	return "", errors.New("not implemented")
}

type fakeTokenManager struct {
	createdUserID string
}

func (m *fakeTokenManager) CreatePersonalAccessToken(context.Context, string, string, []string, *time.Time, string) (string, error) {
	m.createdUserID = "user-123"
	return "gpat_test_created_token", nil
}

func (m *fakeTokenManager) ListPersonalAccessTokens(context.Context, string) ([]auth.PersonalAccessTokenRecord, error) {
	return []auth.PersonalAccessTokenRecord{{
		ID:          "pat-1",
		Label:       "CI token",
		TokenPrefix: tokenPrefix("gpat_test_created_token"),
		CreatedAt:   time.Now().UTC(),
	}}, nil
}

func (m *fakeTokenManager) RevokePersonalAccessToken(context.Context, string, string) error {
	return nil
}

func (m *fakeTokenManager) CreateAutomationToken(context.Context, string, string, string, []string, *time.Time, string) (string, error) {
	return "", errors.New("not implemented")
}

func (m *fakeTokenManager) ListAutomationTokens(context.Context, string) ([]auth.AutomationTokenRecord, error) {
	return nil, errors.New("not implemented")
}

func (m *fakeTokenManager) RevokeAutomationToken(context.Context, string, string) error {
	return errors.New("not implemented")
}

func newSessionAuthorizedAPIServer(t *testing.T) (*httptest.Server, *sql.DB, string, string) {
	t.Helper()

	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org-id",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_session_test_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}

	authz := auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 24*time.Hour)
	sessionToken, principal, err := authz.Login(context.Background(), bootstrap.Email, bootstrap.Password, "api-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if principal == nil {
		t.Fatal("expected session principal")
	}

	server := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		Auth:         authz,
		TokenManager: authStore,
	})))
	t.Cleanup(server.Close)
	return server, db, sessionToken, principal.CSRFToken
}

func sessionJSONRequest(t *testing.T, ts *httptest.Server, method, path, sessionToken, csrf string, body any) *http.Response {
	t.Helper()

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}

	req, err := http.NewRequest(method, ts.URL+path, &payload)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestPersonalAccessTokenLifecycle(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)

	create := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/users/me/personal-access-tokens/", sessionToken, csrf, map[string]any{
		"label":  "CI token",
		"scopes": []string{auth.ScopeOrgQueryRead, auth.ScopeProjectRead, auth.ScopeReleaseRead},
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create PAT status = %d, want 201", create.StatusCode)
	}

	var created CreatedPersonalAccessToken
	decodeBody(t, create, &created)
	if created.Token == "" || created.ID == "" {
		t.Fatalf("created PAT = %+v, want token and id", created)
	}
	if created.Label != "CI token" {
		t.Fatalf("label = %q, want CI token", created.Label)
	}
	if !containsString(created.Scopes, auth.ScopeOrgQueryRead) {
		t.Fatalf("scopes = %v, want %s", created.Scopes, auth.ScopeOrgQueryRead)
	}

	list := sessionJSONRequest(t, ts, http.MethodGet, "/api/0/users/me/personal-access-tokens/", sessionToken, "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list PATs status = %d, want 200", list.StatusCode)
	}
	var tokens []PersonalAccessToken
	decodeBody(t, list, &tokens)
	if len(tokens) < 2 {
		t.Fatalf("token count = %d, want at least bootstrap + created", len(tokens))
	}

	revoke := sessionJSONRequest(t, ts, http.MethodDelete, "/api/0/users/me/personal-access-tokens/"+created.ID+"/", sessionToken, csrf, nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke PAT status = %d, want 204", revoke.StatusCode)
	}
	revoke.Body.Close()

	list = sessionJSONRequest(t, ts, http.MethodGet, "/api/0/users/me/personal-access-tokens/", sessionToken, "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list PATs after revoke status = %d, want 200", list.StatusCode)
	}
	decodeBody(t, list, &tokens)
	foundRevoked := false
	for _, token := range tokens {
		if token.ID == created.ID && token.RevokedAt != nil {
			foundRevoked = true
		}
	}
	if !foundRevoked {
		t.Fatalf("expected revoked PAT %s in %+v", created.ID, tokens)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAutomationTokenLifecycle(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)

	create := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/automation-tokens/", sessionToken, csrf, map[string]any{
		"label":  "artifact upload",
		"scopes": []string{auth.ScopeProjectArtifactsWrite},
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create automation token status = %d, want 201", create.StatusCode)
	}

	var created CreatedAutomationToken
	decodeBody(t, create, &created)
	if created.Token == "" || created.ID == "" {
		t.Fatalf("created automation token = %+v, want token and id", created)
	}
	if created.ProjectID != "test-proj-id" {
		t.Fatalf("projectID = %q, want test-proj-id", created.ProjectID)
	}

	list := sessionJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/automation-tokens/", sessionToken, "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list automation tokens status = %d, want 200", list.StatusCode)
	}
	var tokens []AutomationToken
	decodeBody(t, list, &tokens)
	if len(tokens) != 1 || tokens[0].ID != created.ID {
		t.Fatalf("tokens = %+v, want created token", tokens)
	}

	revoke := sessionJSONRequest(t, ts, http.MethodDelete, "/api/0/projects/test-org/test-project/automation-tokens/"+created.ID+"/", sessionToken, csrf, nil)
	if revoke.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke automation token status = %d, want 204", revoke.StatusCode)
	}
	revoke.Body.Close()

	list = sessionJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/automation-tokens/", sessionToken, "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list automation tokens after revoke status = %d, want 200", list.StatusCode)
	}
	decodeBody(t, list, &tokens)
	if len(tokens) != 1 || tokens[0].RevokedAt == nil {
		t.Fatalf("tokens after revoke = %+v, want revoked token", tokens)
	}
}

func TestCreatePersonalAccessTokenUpsertsPrincipalShadow(t *testing.T) {
	db := openTestSQLite(t)
	manager := &fakeTokenManager{}
	authz := auth.NewAuthorizer(stubAuthStore{}, "urgentry_session", "urgentry_csrf", time.Hour)
	handler := handleCreatePersonalAccessToken(authz, manager, sqlite.NewPrincipalShadowStore(db), func(http.ResponseWriter, *http.Request) bool {
		return true
	})

	body := bytes.NewBufferString(`{"label":"CI token","scopes":["org:read"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/0/users/me/personal-access-tokens/", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-token")
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{
		Kind: auth.CredentialSession,
		User: &auth.User{
			ID:          "user-123",
			Email:       "owner@example.com",
			DisplayName: "Owner",
		},
		CSRFToken: "csrf-token",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 'user-123'`).Scan(&count); err != nil {
		t.Fatalf("query shadow user: %v", err)
	}
	if count != 1 {
		t.Fatalf("shadow user count = %d, want 1", count)
	}
}

func TestCreatePersonalAccessTokenRequiresCSRFErrorCode(t *testing.T) {
	ts, _, sessionToken, _ := newSessionAuthorizedAPIServer(t)

	resp := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/users/me/personal-access-tokens/", sessionToken, "", map[string]any{
		"label": "CI token",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body := decodeAPIError(t, resp)
	if body.Code != "csrf_failed" {
		t.Fatalf("error body = %+v, want csrf_failed", body)
	}
}

func TestCreatePersonalAccessTokenRejectsInvalidScopesWithErrorCode(t *testing.T) {
	db := openTestSQLite(t)
	manager := &fakeTokenManager{}
	authz := auth.NewAuthorizer(stubAuthStore{}, "urgentry_session", "urgentry_csrf", time.Hour)
	handler := handleCreatePersonalAccessToken(authz, manager, sqlite.NewPrincipalShadowStore(db), func(http.ResponseWriter, *http.Request) bool {
		return true
	})

	body := bytes.NewBufferString(`{"label":"CI token","scopes":["org:bogus"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/0/users/me/personal-access-tokens/", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-token")
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{
		Kind: auth.CredentialSession,
		User: &auth.User{
			ID:          "user-123",
			Email:       "owner@example.com",
			DisplayName: "Owner",
		},
		CSRFToken: "csrf-token",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var errBody httputil.APIErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errBody.Code != "invalid_token_scopes" {
		t.Fatalf("error body = %+v, want invalid_token_scopes", errBody)
	}
}
