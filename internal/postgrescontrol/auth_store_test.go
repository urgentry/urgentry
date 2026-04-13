package postgrescontrol

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"urgentry/internal/auth"
)

func TestAuthStoreEnsureBootstrapAccess(t *testing.T) {
	t.Parallel()

	store, db := newAuthTestStore(t)
	bootstrap, err := store.EnsureBootstrapAccess(t.Context(), BootstrapOptions{
		DefaultOrganizationID: "org-bootstrap",
		Email:                 "Owner@Example.com",
		DisplayName:           "Owner",
		Password:              "hunter2",
		PersonalAccessToken:   "gpat_bootstrap_seed",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess() error = %v", err)
	}
	if !bootstrap.Created {
		t.Fatalf("bootstrap.Created = false, want true")
	}
	if bootstrap.PAT != "gpat_bootstrap_seed" {
		t.Fatalf("bootstrap.PAT = %q", bootstrap.PAT)
	}

	user, err := store.AuthenticateUserPassword(t.Context(), "owner@example.com", "hunter2")
	if err != nil {
		t.Fatalf("AuthenticateUserPassword() error = %v", err)
	}
	if user.Email != "owner@example.com" {
		t.Fatalf("user.Email = %q, want owner@example.com", user.Email)
	}

	principal, err := store.AuthenticatePAT(t.Context(), "gpat_bootstrap_seed")
	if err != nil {
		t.Fatalf("AuthenticatePAT() error = %v", err)
	}
	if principal.User == nil || principal.User.ID != user.ID {
		t.Fatalf("AuthenticatePAT() user = %#v, want %q", principal.User, user.ID)
	}
	if !principal.HasScope(auth.ScopeOrgAdmin) {
		t.Fatalf("bootstrap PAT missing %q scope", auth.ScopeOrgAdmin)
	}

	role, err := store.LookupUserOrgRole(t.Context(), user.ID, "org-bootstrap")
	if err != nil {
		t.Fatalf("LookupUserOrgRole() error = %v", err)
	}
	if role != "owner" {
		t.Fatalf("role = %q, want owner", role)
	}

	second, err := store.EnsureBootstrapAccess(t.Context(), BootstrapOptions{})
	if err != nil {
		t.Fatalf("second EnsureBootstrapAccess() error = %v", err)
	}
	if second.Created {
		t.Fatalf("second bootstrap.Created = true, want false")
	}

	var auditCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_audit_logs WHERE action = 'bootstrap.created'`).Scan(&auditCount); err != nil {
		t.Fatalf("count bootstrap audit logs: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("bootstrap audit count = %d, want 1", auditCount)
	}
}

func TestAuthStoreRotateBootstrapAccess(t *testing.T) {
	t.Parallel()

	store, db := newAuthTestStore(t)
	bootstrap, err := store.EnsureBootstrapAccess(t.Context(), BootstrapOptions{
		DefaultOrganizationID: "org-bootstrap",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "hunter2",
		PersonalAccessToken:   "gpat_seed_initial_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess() error = %v", err)
	}

	rotated, err := store.RotateBootstrapAccess(t.Context(), bootstrap.Email, "new-hunter2", "gpat_rotated_token_value")
	if err != nil {
		t.Fatalf("RotateBootstrapAccess() error = %v", err)
	}
	if rotated.PAT != "gpat_rotated_token_value" {
		t.Fatalf("RotateBootstrapAccess() PAT = %q", rotated.PAT)
	}

	if _, err := store.AuthenticateUserPassword(t.Context(), bootstrap.Email, bootstrap.Password); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthenticateUserPassword(old) error = %v, want ErrInvalidCredentials", err)
	}
	user, err := store.AuthenticateUserPassword(t.Context(), bootstrap.Email, "new-hunter2")
	if err != nil {
		t.Fatalf("AuthenticateUserPassword(new) error = %v", err)
	}
	if user.Email != bootstrap.Email {
		t.Fatalf("AuthenticateUserPassword(new) email = %q", user.Email)
	}

	if _, err := store.AuthenticatePAT(t.Context(), bootstrap.PAT); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthenticatePAT(old) error = %v, want ErrInvalidCredentials", err)
	}
	principal, err := store.AuthenticatePAT(t.Context(), rotated.PAT)
	if err != nil {
		t.Fatalf("AuthenticatePAT(new) error = %v", err)
	}
	if principal.User == nil || principal.User.Email != bootstrap.Email {
		t.Fatalf("AuthenticatePAT(new) user = %#v", principal.User)
	}

	var activeCount int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM personal_access_tokens WHERE user_id = $1 AND label = $2 AND revoked_at IS NULL`,
		principal.User.ID, defaultPATLabel,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active bootstrap pats: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active bootstrap pats = %d, want 1", activeCount)
	}

	var auditCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_audit_logs WHERE action = 'bootstrap.rotated'`).Scan(&auditCount); err != nil {
		t.Fatalf("count bootstrap rotation audit logs: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("bootstrap rotation audit count = %d, want 1", auditCount)
	}
}

func TestAuthStoreSessionAndTokenFlows(t *testing.T) {
	t.Parallel()

	store, db := newAuthTestStore(t)
	userID, projectID := seedAuthSubjectData(t, db)

	rawSession, principal, err := store.CreateSession(t.Context(), userID, "test-agent", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if principal.Kind != auth.CredentialSession {
		t.Fatalf("session principal kind = %q", principal.Kind)
	}

	authedSession, err := store.AuthenticateSession(t.Context(), rawSession)
	if err != nil {
		t.Fatalf("AuthenticateSession() error = %v", err)
	}
	if authedSession.User == nil || authedSession.User.ID != userID {
		t.Fatalf("AuthenticateSession() user = %#v, want %q", authedSession.User, userID)
	}
	if err := store.RevokeSession(t.Context(), authedSession.CredentialID); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}
	if _, err := store.AuthenticateSession(t.Context(), rawSession); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthenticateSession() after revoke error = %v, want ErrInvalidCredentials", err)
	}

	rawPAT, err := store.CreatePersonalAccessToken(t.Context(), userID, "CLI", []string{auth.ScopeProjectRead, auth.ScopeIssueWrite}, nil, "gpat_flow_seed")
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken() error = %v", err)
	}
	if rawPAT != "gpat_flow_seed" {
		t.Fatalf("CreatePersonalAccessToken() raw = %q", rawPAT)
	}

	patPrincipal, err := store.AuthenticatePAT(t.Context(), rawPAT)
	if err != nil {
		t.Fatalf("AuthenticatePAT() error = %v", err)
	}
	if !patPrincipal.HasScope(auth.ScopeProjectRead) || !patPrincipal.HasScope(auth.ScopeIssueWrite) {
		t.Fatalf("PAT principal scopes = %#v", patPrincipal.Scopes)
	}

	pats, err := store.ListPersonalAccessTokens(t.Context(), userID)
	if err != nil {
		t.Fatalf("ListPersonalAccessTokens() error = %v", err)
	}
	if len(pats) != 1 || pats[0].Label != "CLI" {
		t.Fatalf("ListPersonalAccessTokens() = %#v", pats)
	}
	if err := store.RevokePersonalAccessToken(t.Context(), pats[0].ID, userID); err != nil {
		t.Fatalf("RevokePersonalAccessToken() error = %v", err)
	}
	if _, err := store.AuthenticatePAT(t.Context(), rawPAT); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthenticatePAT() after revoke error = %v, want ErrInvalidCredentials", err)
	}

	rawAutomation, err := store.CreateAutomationToken(t.Context(), projectID, "CI", userID, []string{auth.ScopeProjectArtifactsWrite}, nil, "gauto_flow_seed")
	if err != nil {
		t.Fatalf("CreateAutomationToken() error = %v", err)
	}
	if rawAutomation != "gauto_flow_seed" {
		t.Fatalf("CreateAutomationToken() raw = %q", rawAutomation)
	}

	autoPrincipal, err := store.AuthenticateAutomationToken(t.Context(), rawAutomation)
	if err != nil {
		t.Fatalf("AuthenticateAutomationToken() error = %v", err)
	}
	if autoPrincipal.ProjectID != projectID || !autoPrincipal.HasScope(auth.ScopeProjectArtifactsWrite) {
		t.Fatalf("automation principal = %#v", autoPrincipal)
	}

	autos, err := store.ListAutomationTokens(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListAutomationTokens() error = %v", err)
	}
	if len(autos) != 1 || autos[0].CreatedByUserID != userID {
		t.Fatalf("ListAutomationTokens() = %#v", autos)
	}
	if err := store.RevokeAutomationToken(t.Context(), autos[0].ID, projectID); err != nil {
		t.Fatalf("RevokeAutomationToken() error = %v", err)
	}
	if _, err := store.AuthenticateAutomationToken(t.Context(), rawAutomation); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthenticateAutomationToken() after revoke error = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthStoreProjectAndKeyLookups(t *testing.T) {
	t.Parallel()

	store, db := newAuthTestStore(t)
	userID, projectID := seedAuthSubjectData(t, db)

	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO project_keys (id, project_id, public_key, status, label, rate_limit_per_minute)
		 VALUES ($1, $2, 'public-key-1', 'active', 'Default', 120)`,
		"key-1", projectID,
	); err != nil {
		t.Fatalf("insert project key: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, level, status, created_at, updated_at)
		 VALUES ('group-1', $1, 'v1', 'panic-main', 'panic', 'error', 'unresolved', now(), now())`,
		projectID,
	); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO group_occurrences (id, group_id, event_id, occurred_at)
		 VALUES ('occ-1', 'group-1', 'event-1', now())`,
	); err != nil {
		t.Fatalf("insert group occurrence: %v", err)
	}

	key, err := store.LookupKey(t.Context(), "public-key-1")
	if err != nil {
		t.Fatalf("LookupKey() error = %v", err)
	}
	if key.ProjectID != projectID || key.RateLimit != 120 {
		t.Fatalf("LookupKey() = %#v", key)
	}

	if err := store.TouchProjectKey(t.Context(), "public-key-1"); err != nil {
		t.Fatalf("TouchProjectKey() error = %v", err)
	}
	var lastUsedAt sql.NullTime
	if err := db.QueryRowContext(t.Context(), `SELECT last_used_at FROM project_keys WHERE public_key = 'public-key-1'`).Scan(&lastUsedAt); err != nil {
		t.Fatalf("load last_used_at: %v", err)
	}
	if !lastUsedAt.Valid {
		t.Fatalf("last_used_at not set")
	}

	org, err := store.ResolveOrganizationBySlug(t.Context(), "acme")
	if err != nil {
		t.Fatalf("ResolveOrganizationBySlug() error = %v", err)
	}
	if org == nil || org.ID != "org-1" {
		t.Fatalf("ResolveOrganizationBySlug() = %#v", org)
	}

	projectByID, err := store.ResolveProjectByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ResolveProjectByID() error = %v", err)
	}
	projectBySlug, err := store.ResolveProjectBySlug(t.Context(), "acme", "backend")
	if err != nil {
		t.Fatalf("ResolveProjectBySlug() error = %v", err)
	}
	issueProject, err := store.ResolveIssueProject(t.Context(), "group-1")
	if err != nil {
		t.Fatalf("ResolveIssueProject() error = %v", err)
	}
	eventProject, err := store.ResolveEventProject(t.Context(), "event-1")
	if err != nil {
		t.Fatalf("ResolveEventProject() error = %v", err)
	}
	for _, project := range []*auth.Project{projectByID, projectBySlug, issueProject, eventProject} {
		if project == nil || project.ID != projectID || project.OrganizationID != "org-1" {
			t.Fatalf("resolved project = %#v", project)
		}
	}

	role, err := store.LookupUserOrgRole(t.Context(), userID, "org-1")
	if err != nil {
		t.Fatalf("LookupUserOrgRole() error = %v", err)
	}
	if role != "owner" {
		t.Fatalf("LookupUserOrgRole() = %q, want owner", role)
	}
	roles, err := store.ListUserOrgRoles(t.Context(), userID)
	if err != nil {
		t.Fatalf("ListUserOrgRoles() error = %v", err)
	}
	if roles["org-1"] != "owner" {
		t.Fatalf("ListUserOrgRoles() = %#v", roles)
	}
}

func TestEnsureDefaultKey(t *testing.T) {
	t.Parallel()

	db := openMigratedTestDatabase(t)

	first, err := EnsureDefaultKey(t.Context(), db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey() error = %v", err)
	}
	second, err := EnsureDefaultKey(t.Context(), db)
	if err != nil {
		t.Fatalf("second EnsureDefaultKey() error = %v", err)
	}
	if first != second {
		t.Fatalf("EnsureDefaultKey() = %q then %q, want stable key", first, second)
	}

	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_keys`).Scan(&count); err != nil {
		t.Fatalf("count project keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("project key count = %d, want 1", count)
	}
}

func newAuthTestStore(t *testing.T) (*AuthStore, *sql.DB) {
	t.Helper()

	db := openMigratedTestDatabase(t)
	return NewAuthStore(db), db
}

func seedAuthSubjectData(t *testing.T, db *sql.DB) (userID string, projectID string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), `
INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme');
INSERT INTO users (id, email, display_name, is_active) VALUES ('user-1', 'owner@example.com', 'Owner', TRUE);
INSERT INTO user_password_credentials (user_id, password_hash, password_algo) VALUES ('user-1', '$2a$10$0A1A55WS5cM4EmhKWvIY/.uj1JrVN6a8GN28AL5soMgqd7qV3CyJe', 'bcrypt');
INSERT INTO organization_members (id, organization_id, user_id, role) VALUES ('member-1', 'org-1', 'user-1', 'owner');
INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'backend', 'Backend', 'go', 'active');
`); err != nil {
		t.Fatalf("seed auth subject data: %v", err)
	}
	return "user-1", "proj-1"
}
