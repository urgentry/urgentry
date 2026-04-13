package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeAuthorizerStore struct {
	user                *User
	authUserErr         error
	createSessionRaw    string
	createSessionResult *Principal
	createSessionErr    error
	sessionPrincipal    *Principal
	sessionErr          error
	patPrincipal        *Principal
	patErr              error
	autoPrincipal       *Principal
	autoErr             error
	organizations       map[string]*Organization
	projectsByID        map[string]*Project
	projectsBySlug      map[string]*Project
	issueProjects       map[string]*Project
	eventProjects       map[string]*Project
	userOrgRoles        map[string]map[string]string
	userProjectRoles    map[string]map[string]string // userID -> projectID -> role
	lastCreateSession   struct {
		userID    string
		userAgent string
		ipAddress string
		ttl       time.Duration
	}
	revokedSessionID string
}

func (s *fakeAuthorizerStore) AuthenticateUserPassword(context.Context, string, string) (*User, error) {
	if s.authUserErr != nil {
		return nil, s.authUserErr
	}
	return s.user, nil
}

func (s *fakeAuthorizerStore) CreateSession(_ context.Context, userID, userAgent, ipAddress string, ttl time.Duration) (string, *Principal, error) {
	s.lastCreateSession.userID = userID
	s.lastCreateSession.userAgent = userAgent
	s.lastCreateSession.ipAddress = ipAddress
	s.lastCreateSession.ttl = ttl
	if s.createSessionErr != nil {
		return "", nil, s.createSessionErr
	}
	return s.createSessionRaw, s.createSessionResult, nil
}

func (s *fakeAuthorizerStore) AuthenticateSession(context.Context, string) (*Principal, error) {
	if s.sessionErr != nil {
		return nil, s.sessionErr
	}
	return s.sessionPrincipal, nil
}

func (s *fakeAuthorizerStore) RevokeSession(context.Context, string) error {
	s.revokedSessionID = s.createSessionResult.CredentialID
	return nil
}

func (s *fakeAuthorizerStore) AuthenticatePAT(context.Context, string) (*Principal, error) {
	if s.patErr != nil {
		return nil, s.patErr
	}
	return s.patPrincipal, nil
}

func (s *fakeAuthorizerStore) AuthenticateAutomationToken(context.Context, string) (*Principal, error) {
	if s.autoErr != nil {
		return nil, s.autoErr
	}
	return s.autoPrincipal, nil
}

func (s *fakeAuthorizerStore) ResolveOrganizationBySlug(_ context.Context, slug string) (*Organization, error) {
	return s.organizations[slug], nil
}

func (s *fakeAuthorizerStore) ResolveProjectByID(_ context.Context, projectID string) (*Project, error) {
	return s.projectsByID[projectID], nil
}

func (s *fakeAuthorizerStore) ResolveProjectBySlug(_ context.Context, orgSlug, projectSlug string) (*Project, error) {
	return s.projectsBySlug[orgSlug+"/"+projectSlug], nil
}

func (s *fakeAuthorizerStore) ResolveIssueProject(_ context.Context, issueID string) (*Project, error) {
	return s.issueProjects[issueID], nil
}

func (s *fakeAuthorizerStore) ResolveEventProject(_ context.Context, eventID string) (*Project, error) {
	return s.eventProjects[eventID], nil
}

func (s *fakeAuthorizerStore) LookupUserOrgRole(_ context.Context, userID, organizationID string) (string, error) {
	if roles := s.userOrgRoles[userID]; roles != nil {
		return roles[organizationID], nil
	}
	return "", nil
}

func (s *fakeAuthorizerStore) ListUserOrgRoles(_ context.Context, userID string) (map[string]string, error) {
	roles := s.userOrgRoles[userID]
	if roles == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(roles))
	for key, value := range roles {
		out[key] = value
	}
	return out, nil
}

func (s *fakeAuthorizerStore) LookupUserProjectRole(_ context.Context, userID, projectID string) (string, error) {
	if roles := s.userProjectRoles[userID]; roles != nil {
		return roles[projectID], nil
	}
	return "", nil
}

func TestAuthorizerLoginAndRevokeSession(t *testing.T) {
	store := &fakeAuthorizerStore{
		user:             &User{ID: "user-1", Email: "owner@example.com"},
		createSessionRaw: "gsess_token",
		createSessionResult: &Principal{
			Kind:         CredentialSession,
			CredentialID: "session-1",
			User:         &User{ID: "user-1", Email: "owner@example.com"},
			CSRFToken:    "csrf-1",
		},
	}

	authz := NewAuthorizer(store, "", "", 0)
	raw, principal, err := authz.Login(context.Background(), "owner@example.com", "secret", "browser", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if raw != "gsess_token" {
		t.Fatalf("raw token = %q, want gsess_token", raw)
	}
	if principal == nil || principal.CredentialID != "session-1" {
		t.Fatalf("principal = %+v, want session-1", principal)
	}
	if authz.SessionCookieName() != "urgentry_session" {
		t.Fatalf("SessionCookieName = %q, want urgentry_session", authz.SessionCookieName())
	}
	if authz.CSRFCookieName() != "urgentry_csrf" {
		t.Fatalf("CSRFCookieName = %q, want urgentry_csrf", authz.CSRFCookieName())
	}
	if store.lastCreateSession.userID != "user-1" || store.lastCreateSession.userAgent != "browser" || store.lastCreateSession.ipAddress != "127.0.0.1" {
		t.Fatalf("create session args = %+v", store.lastCreateSession)
	}
	if store.lastCreateSession.ttl <= 0 {
		t.Fatalf("session ttl = %v, want > 0", store.lastCreateSession.ttl)
	}

	if err := authz.RevokeSession(context.Background(), principal); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if store.revokedSessionID != "session-1" {
		t.Fatalf("revoked session = %q, want session-1", store.revokedSessionID)
	}
}

func TestAuthorizerWebMiddleware(t *testing.T) {
	store := &fakeAuthorizerStore{
		sessionPrincipal: &Principal{
			Kind:         CredentialSession,
			CredentialID: "session-1",
			User:         &User{ID: "user-1"},
			CSRFToken:    "csrf-1",
		},
	}
	authz := NewAuthorizer(store, "urgentry_session", "urgentry_csrf", time.Hour)

	t.Run("redirects when session is missing", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/issues/?cursor=1", nil)

		authz.Web(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler should not run")
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		if location := rec.Header().Get("Location"); location != "/login/?next=/issues/?cursor=1" {
			t.Fatalf("location = %q, want login redirect", location)
		}
	})

	t.Run("injects the authenticated principal", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/issues/", nil)
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: "session-cookie"})

		authz.Web(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := PrincipalFromContext(r.Context())
			if principal == nil || principal.CredentialID != "session-1" {
				t.Fatalf("principal = %+v, want session-1", principal)
			}
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("exposes session authentication helper", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/issues/", nil)
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: "session-cookie"})
		principal, err := authz.AuthenticateSessionRequest(req)
		if err != nil {
			t.Fatalf("AuthenticateSessionRequest: %v", err)
		}
		if principal == nil || principal.CredentialID != "session-1" {
			t.Fatalf("principal = %+v, want session-1", principal)
		}
	})
}

func TestAuthorizerAPIAndHelpers(t *testing.T) {
	project := &Project{ID: "proj-1", Slug: "checkout", OrganizationID: "org-1", OrganizationSlug: "acme"}
	store := &fakeAuthorizerStore{
		patPrincipal: &Principal{
			Kind: CredentialPAT,
			User: &User{ID: "user-1"},
			Scopes: map[string]struct{}{
				ScopeProjectRead:  {},
				ScopeProjectWrite: {},
			},
		},
		autoPrincipal: &Principal{
			Kind:      CredentialAutomationToken,
			ProjectID: "proj-1",
			Scopes: map[string]struct{}{
				ScopeProjectArtifactsWrite: {},
			},
		},
		sessionPrincipal: &Principal{
			Kind:         CredentialSession,
			CredentialID: "session-1",
			User:         &User{ID: "user-1"},
			CSRFToken:    "csrf-1",
		},
		organizations: map[string]*Organization{
			"acme": {ID: "org-1", Slug: "acme"},
		},
		projectsByID: map[string]*Project{
			"proj-1": project,
		},
		projectsBySlug: map[string]*Project{
			"acme/checkout": project,
		},
		issueProjects: map[string]*Project{
			"issue-1": project,
		},
		eventProjects: map[string]*Project{
			"event-1": project,
		},
		userOrgRoles: map[string]map[string]string{
			"user-1": {"org-1": "admin"},
			"user-2": {"org-2": "viewer"},
		},
	}
	authz := NewAuthorizer(store, "urgentry_session", "urgentry_csrf", time.Hour)

	t.Run("authorizes PAT project reads", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/0/projects/acme/checkout/", nil)
		req.Header.Set("Authorization", "Bearer gpat_token")
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		if ok := authz.API(Policy{Scope: ScopeProjectRead, Resource: ResourceProjectPath})(rec, req); !ok {
			t.Fatalf("status = %d, want auth success", rec.Code)
		}
		principal := PrincipalFromContext(req.Context())
		if principal == nil || principal.Kind != CredentialPAT {
			t.Fatalf("principal = %+v, want PAT", principal)
		}
	})

	t.Run("authorizes automation tokens only when allowed", func(t *testing.T) {
		store.patErr = ErrInvalidCredentials
		defer func() { store.patErr = nil }()

		req := httptest.NewRequest(http.MethodPost, "/api/0/projects/acme/checkout/files/", nil)
		req.Header.Set("Authorization", "Bearer gaut_token")
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		if ok := authz.API(Policy{Scope: ScopeProjectArtifactsWrite, Resource: ResourceProjectPath, AllowAutomation: true})(rec, req); !ok {
			t.Fatalf("status = %d, want auth success", rec.Code)
		}
	})

	t.Run("rejects automation tokens when not allowed", func(t *testing.T) {
		store.patErr = ErrInvalidCredentials
		defer func() { store.patErr = nil }()

		req := httptest.NewRequest(http.MethodGet, "/api/0/projects/acme/checkout/", nil)
		req.Header.Set("Authorization", "Bearer gaut_token")
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		if ok := authz.API(Policy{Scope: ScopeProjectRead, Resource: ResourceProjectPath})(rec, req); ok {
			t.Fatal("expected auth failure")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("validates csrf and authorize helpers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/issues/issue-1/status", nil)
		req.Header.Set("X-CSRF-Token", "csrf-1")
		req = req.WithContext(WithPrincipal(req.Context(), store.sessionPrincipal))
		if !authz.ValidateCSRF(req) {
			t.Fatal("expected CSRF validation to pass")
		}
		if err := authz.AuthorizeIssue(req, "issue-1", ScopeProjectRead); err != nil {
			t.Fatalf("AuthorizeIssue: %v", err)
		}
		if err := authz.AuthorizeProject(req, "proj-1", ScopeProjectWrite); err != nil {
			t.Fatalf("AuthorizeProject: %v", err)
		}
	})

	t.Run("uses org role lookup for membership-wide policies", func(t *testing.T) {
		store.patPrincipal = &Principal{
			Kind: CredentialPAT,
			User: &User{ID: "user-1"},
			Scopes: map[string]struct{}{
				ScopeOrgRead: {},
			},
		}
		req := httptest.NewRequest(http.MethodGet, "/api/0/organizations/", nil)
		req.Header.Set("Authorization", "Bearer gpat_token")
		rec := httptest.NewRecorder()

		if ok := authz.API(Policy{Scope: ScopeOrgRead, Resource: ResourceAnyMembership})(rec, req); !ok {
			t.Fatalf("status = %d, want auth success", rec.Code)
		}
	})
}

func TestScopeImplicationHelpers(t *testing.T) {
	if !scopeImplies(ScopeOrgAdmin, ScopeProjectWrite) {
		t.Fatal("org admin should imply project write")
	}
	if !scopeImplies(ScopeProjectWrite, ScopeProjectRead) {
		t.Fatal("project write should imply project read")
	}
	if scopeImplies(ScopeProjectRead, ScopeProjectWrite) {
		t.Fatal("project read should not imply project write")
	}
	if !roleAllowsScope("viewer", ScopeProjectRead) {
		t.Fatal("viewer should allow project read")
	}
	if !roleAllowsScope("viewer", ScopeOrgQueryRead) {
		t.Fatal("viewer should allow org query read")
	}
	if roleAllowsScope("viewer", ScopeProjectWrite) {
		t.Fatal("viewer should not allow project write")
	}
	if !principalHasScope(&Principal{Scopes: map[string]struct{}{ScopeProjectTokensWrite: {}}}, ScopeProjectTokensRead) {
		t.Fatal("write token scope should imply read")
	}
}

func TestProjectRoleAllowsScope(t *testing.T) {
	// owner: full control
	if !projectRoleAllowsScope("owner", ScopeProjectWrite) {
		t.Fatal("project owner should allow project write")
	}
	if !projectRoleAllowsScope("owner", ScopeOrgAdmin) {
		t.Fatal("project owner should allow org admin (full control)")
	}

	// admin: settings, alerts, integrations, triage, queries
	if !projectRoleAllowsScope("admin", ScopeProjectWrite) {
		t.Fatal("project admin should allow project write")
	}
	if !projectRoleAllowsScope("admin", ScopeIssueWrite) {
		t.Fatal("project admin should allow issue write")
	}
	if !projectRoleAllowsScope("admin", ScopeProjectKeysWrite) {
		t.Fatal("project admin should allow project keys write")
	}

	// member: triage issues, queries, read-only settings
	if !projectRoleAllowsScope("member", ScopeIssueWrite) {
		t.Fatal("project member should allow issue write")
	}
	if !projectRoleAllowsScope("member", ScopeProjectRead) {
		t.Fatal("project member should allow project read")
	}
	if projectRoleAllowsScope("member", ScopeProjectWrite) {
		t.Fatal("project member should NOT allow project write")
	}
	if projectRoleAllowsScope("member", ScopeProjectKeysWrite) {
		t.Fatal("project member should NOT allow project keys write")
	}

	// viewer: read-only
	if !projectRoleAllowsScope("viewer", ScopeProjectRead) {
		t.Fatal("project viewer should allow project read")
	}
	if !projectRoleAllowsScope("viewer", ScopeOrgQueryRead) {
		t.Fatal("project viewer should allow org query read")
	}
	if projectRoleAllowsScope("viewer", ScopeProjectWrite) {
		t.Fatal("project viewer should NOT allow project write")
	}
	if projectRoleAllowsScope("viewer", ScopeIssueWrite) {
		t.Fatal("project viewer should NOT allow issue write")
	}
}

func TestProjectRoleOverridesOrgRole(t *testing.T) {
	project := &Project{ID: "proj-1", Slug: "checkout", OrganizationID: "org-1", OrganizationSlug: "acme"}
	store := &fakeAuthorizerStore{
		sessionPrincipal: &Principal{
			Kind:         CredentialSession,
			CredentialID: "session-1",
			User:         &User{ID: "user-1"},
			CSRFToken:    "csrf-1",
		},
		organizations: map[string]*Organization{
			"acme": {ID: "org-1", Slug: "acme"},
		},
		projectsByID: map[string]*Project{
			"proj-1": project,
		},
		projectsBySlug: map[string]*Project{
			"acme/checkout": project,
		},
		// User is org admin, but project viewer
		userOrgRoles: map[string]map[string]string{
			"user-1": {"org-1": "admin"},
		},
		userProjectRoles: map[string]map[string]string{
			"user-1": {"proj-1": "viewer"},
		},
	}
	authz := NewAuthorizer(store, "urgentry_session", "urgentry_csrf", time.Hour)

	t.Run("viewer project role blocks write despite org admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/0/projects/acme/checkout/settings/", nil)
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: "session-cookie"})
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		ok := authz.API(Policy{Scope: ScopeProjectWrite, Resource: ResourceProjectPath})(rec, req)
		if ok {
			t.Fatal("expected project viewer to be denied write access")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("viewer project role allows read", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/0/projects/acme/checkout/", nil)
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: "session-cookie"})
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		ok := authz.API(Policy{Scope: ScopeProjectRead, Resource: ResourceProjectPath})(rec, req)
		if !ok {
			t.Fatalf("expected project viewer to be allowed read, status = %d", rec.Code)
		}
	})

	t.Run("no project role uses org role only", func(t *testing.T) {
		store.userProjectRoles = nil // no project-level assignment
		req := httptest.NewRequest(http.MethodPut, "/api/0/projects/acme/checkout/settings/", nil)
		req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: "session-cookie"})
		req.SetPathValue("org_slug", "acme")
		req.SetPathValue("proj_slug", "checkout")
		rec := httptest.NewRecorder()

		ok := authz.API(Policy{Scope: ScopeProjectWrite, Resource: ResourceProjectPath})(rec, req)
		if !ok {
			t.Fatalf("expected org admin without project role to be allowed, status = %d", rec.Code)
		}
	})
}

func TestIsValidProjectRole(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member", "viewer"} {
		if !IsValidProjectRole(role) {
			t.Fatalf("expected %q to be valid", role)
		}
	}
	if IsValidProjectRole("superadmin") {
		t.Fatal("expected 'superadmin' to be invalid")
	}
	if IsValidProjectRole("") {
		t.Fatal("expected empty string to be invalid")
	}
}

func TestAuthorizerRejectsInvalidCredentials(t *testing.T) {
	store := &fakeAuthorizerStore{
		authUserErr: errors.New("bad password"),
		sessionErr:  ErrInvalidCredentials,
		patErr:      ErrInvalidCredentials,
		autoErr:     ErrInvalidCredentials,
	}
	authz := NewAuthorizer(store, "urgentry_session", "urgentry_csrf", time.Hour)
	if _, _, err := authz.Login(context.Background(), "owner@example.com", "wrong", "", ""); err == nil {
		t.Fatal("expected login failure")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/0/organizations/", nil)
	rec := httptest.NewRecorder()
	if ok := authz.API(Policy{Scope: ScopeOrgRead, Resource: ResourceAnyMembership})(rec, req); ok {
		t.Fatal("expected auth failure")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
