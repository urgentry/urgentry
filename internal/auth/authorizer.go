package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/httputil"
)

const (
	ScopeOrgRead               = "org:read"
	ScopeOrgAdmin              = "org:admin"
	ScopeOrgQueryRead          = "org:query:read"
	ScopeOrgQueryWrite         = "org:query:write"
	ScopeProjectRead           = "project:read"
	ScopeProjectWrite          = "project:write"
	ScopeProjectKeysRead       = "project:keys:read"
	ScopeProjectKeysWrite      = "project:keys:write"
	ScopeProjectTokensRead     = "project:tokens:read"
	ScopeProjectTokensWrite    = "project:tokens:write"
	ScopeProjectArtifactsWrite = "project:artifacts:write"
	ScopeIssueWrite            = "issue:write"
	ScopeReleaseRead           = "release:read"
	ScopeReleaseWrite          = "release:write"
)

var (
	// ErrInvalidCredentials reports an authentication failure.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrExpiredCredentials reports an expired or revoked credential.
	ErrExpiredCredentials = errors.New("expired credentials")
	// ErrForbidden reports an authorization failure.
	ErrForbidden = errors.New("forbidden")
)

// Organization is the auth-visible organization resource.
type Organization struct {
	ID   string
	Slug string
}

// Project is the auth-visible project resource.
type Project struct {
	ID               string
	Slug             string
	OrganizationID   string
	OrganizationSlug string
}

// ProjectRole represents a granular project-level access role.
type ProjectRole string

const (
	ProjectRoleOwner  ProjectRole = "owner"
	ProjectRoleAdmin  ProjectRole = "admin"
	ProjectRoleMember ProjectRole = "member"
	ProjectRoleViewer ProjectRole = "viewer"
)

// ValidProjectRoles lists all valid project roles.
var ValidProjectRoles = []ProjectRole{ProjectRoleOwner, ProjectRoleAdmin, ProjectRoleMember, ProjectRoleViewer}

// IsValidProjectRole reports whether r is a recognized project role.
func IsValidProjectRole(r string) bool {
	for _, v := range ValidProjectRoles {
		if string(v) == r {
			return true
		}
	}
	return false
}

// Store is the backing store needed by the authorizer.
type Store interface {
	AuthenticateUserPassword(ctx context.Context, email, password string) (*User, error)
	CreateSession(ctx context.Context, userID, userAgent, ipAddress string, ttl time.Duration) (rawToken string, principal *Principal, err error)
	AuthenticateSession(ctx context.Context, rawToken string) (*Principal, error)
	RevokeSession(ctx context.Context, sessionID string) error
	AuthenticatePAT(ctx context.Context, rawToken string) (*Principal, error)
	AuthenticateAutomationToken(ctx context.Context, rawToken string) (*Principal, error)
	ResolveOrganizationBySlug(ctx context.Context, slug string) (*Organization, error)
	ResolveProjectByID(ctx context.Context, projectID string) (*Project, error)
	ResolveProjectBySlug(ctx context.Context, orgSlug, projectSlug string) (*Project, error)
	ResolveIssueProject(ctx context.Context, issueID string) (*Project, error)
	ResolveEventProject(ctx context.Context, eventID string) (*Project, error)
	LookupUserOrgRole(ctx context.Context, userID, organizationID string) (string, error)
	ListUserOrgRoles(ctx context.Context, userID string) (map[string]string, error)
	LookupUserProjectRole(ctx context.Context, userID, projectID string) (string, error)
}

// ResourceKind controls which path resource is loaded before authorization.
type ResourceKind int

const (
	ResourceNone ResourceKind = iota
	ResourceAnyMembership
	ResourceOrganizationPath
	ResourceProjectPath
	ResourceProjectIDPath
	ResourceIssuePath
	ResourceEventPath
)

// Policy describes the authorization requirement for an endpoint.
type Policy struct {
	Scope           string
	Resource        ResourceKind
	AllowAutomation bool
}

type resolvedResource struct {
	organizationID string
	projectID      string
}

// Authorizer authenticates and authorizes API and web requests.
type Authorizer struct {
	store             Store
	sessionCookieName string
	csrfCookieName    string
	sessionTTL        time.Duration
}

// NewAuthorizer creates a new request authorizer.
func NewAuthorizer(store Store, sessionCookieName, csrfCookieName string, sessionTTL time.Duration) *Authorizer {
	if sessionCookieName == "" {
		sessionCookieName = "urgentry_session"
	}
	if csrfCookieName == "" {
		csrfCookieName = "urgentry_csrf"
	}
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}
	return &Authorizer{
		store:             store,
		sessionCookieName: sessionCookieName,
		csrfCookieName:    csrfCookieName,
		sessionTTL:        sessionTTL,
	}
}

// SessionCookieName returns the configured session cookie name.
func (a *Authorizer) SessionCookieName() string { return a.sessionCookieName }

// CSRFCookieName returns the configured CSRF cookie name.
func (a *Authorizer) CSRFCookieName() string { return a.csrfCookieName }

// Login validates a local email/password and creates a new session.
func (a *Authorizer) Login(ctx context.Context, email, password, userAgent, ipAddress string) (string, *Principal, error) {
	user, err := a.store.AuthenticateUserPassword(ctx, email, password)
	if err != nil {
		return "", nil, err
	}
	return a.store.CreateSession(ctx, user.ID, userAgent, ipAddress, a.sessionTTL)
}

// RevokeSession revokes the current session principal.
func (a *Authorizer) RevokeSession(ctx context.Context, principal *Principal) error {
	if principal == nil || principal.Kind != CredentialSession || principal.CredentialID == "" {
		return nil
	}
	return a.store.RevokeSession(ctx, principal.CredentialID)
}

// Web authenticates the session cookie and redirects to login when missing.
func (a *Authorizer) Web(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := a.authenticateSessionRequest(r)
		if err != nil {
			target := "/login/"
			if r.URL != nil && r.URL.Path != "" {
				target += "?next=" + r.URL.RequestURI()
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// API returns a route-level auth function for management APIs.
func (a *Authorizer) API(policy Policy) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		principal, err := a.authenticateAPIRequest(r, policy.AllowAutomation)
		if err != nil {
			httputil.WriteError(w, http.StatusUnauthorized, "Authentication credentials were not provided.")
			return false
		}

		resource, err := a.resolveResource(r, policy.Resource)
		if err != nil {
			httputil.WriteError(w, http.StatusNotFound, "Resource not found.")
			return false
		}

		if err := a.authorize(r.Context(), principal, policy.Scope, resource); err != nil {
			httputil.WriteError(w, http.StatusForbidden, "You do not have permission to perform this action.")
			return false
		}

		*r = *r.WithContext(WithPrincipal(r.Context(), principal))
		return true
	}
}

// ValidateCSRF checks the request token against the authenticated session.
func (a *Authorizer) ValidateCSRF(r *http.Request) bool {
	principal := PrincipalFromContext(r.Context())
	if principal == nil || principal.Kind != CredentialSession || principal.CSRFToken == "" {
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if token == "" {
		token = strings.TrimSpace(r.FormValue("_csrf"))
	}
	return token != "" && token == principal.CSRFToken
}

// AuthorizeIssue checks the current principal against an issue-scoped permission.
func (a *Authorizer) AuthorizeIssue(r *http.Request, issueID, scope string) error {
	project, err := a.store.ResolveIssueProject(r.Context(), issueID)
	if err != nil || project == nil {
		return ErrForbidden
	}
	return a.authorize(r.Context(), PrincipalFromContext(r.Context()), scope, &resolvedResource{
		organizationID: project.OrganizationID,
		projectID:      project.ID,
	})
}

// AuthorizeProject checks the current principal against a project-scoped permission.
func (a *Authorizer) AuthorizeProject(r *http.Request, projectID, scope string) error {
	project, err := a.store.ResolveProjectByID(r.Context(), projectID)
	if err != nil || project == nil {
		return ErrForbidden
	}
	return a.authorize(r.Context(), PrincipalFromContext(r.Context()), scope, &resolvedResource{
		organizationID: project.OrganizationID,
		projectID:      project.ID,
	})
}

// AuthorizeAnyMembership checks the current principal against a scope that is valid
// across any organization membership.
func (a *Authorizer) AuthorizeAnyMembership(r *http.Request, scope string) error {
	return a.authorize(r.Context(), PrincipalFromContext(r.Context()), scope, &resolvedResource{})
}

func (a *Authorizer) authenticateAPIRequest(r *http.Request, allowAutomation bool) (*Principal, error) {
	token := extractBearerToken(r)
	if token != "" {
		principal, err := a.store.AuthenticatePAT(r.Context(), token)
		if err == nil {
			return principal, nil
		}
		if allowAutomation {
			if principal, err := a.store.AuthenticateAutomationToken(r.Context(), token); err == nil {
				return principal, nil
			}
		}
		return nil, ErrInvalidCredentials
	}
	return a.authenticateSessionRequest(r)
}

func (a *Authorizer) authenticateSessionRequest(r *http.Request) (*Principal, error) {
	cookie, err := r.Cookie(a.sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, ErrInvalidCredentials
	}
	return a.store.AuthenticateSession(r.Context(), cookie.Value)
}

// AuthenticateSessionRequest validates the current request's session cookie.
func (a *Authorizer) AuthenticateSessionRequest(r *http.Request) (*Principal, error) {
	return a.authenticateSessionRequest(r)
}

func (a *Authorizer) resolveResource(r *http.Request, kind ResourceKind) (*resolvedResource, error) {
	ctx := r.Context()
	switch kind {
	case ResourceNone:
		return &resolvedResource{}, nil
	case ResourceAnyMembership:
		return &resolvedResource{}, nil
	case ResourceOrganizationPath:
		org, err := a.store.ResolveOrganizationBySlug(ctx, r.PathValue("org_slug"))
		if err != nil || org == nil {
			return nil, ErrForbidden
		}
		return &resolvedResource{organizationID: org.ID}, nil
	case ResourceProjectPath:
		project, err := a.store.ResolveProjectBySlug(ctx, r.PathValue("org_slug"), r.PathValue("proj_slug"))
		if err != nil || project == nil {
			return nil, ErrForbidden
		}
		return &resolvedResource{organizationID: project.OrganizationID, projectID: project.ID}, nil
	case ResourceProjectIDPath:
		project, err := a.store.ResolveProjectByID(ctx, r.PathValue("project_id"))
		if err != nil || project == nil {
			return nil, ErrForbidden
		}
		return &resolvedResource{organizationID: project.OrganizationID, projectID: project.ID}, nil
	case ResourceIssuePath:
		project, err := a.store.ResolveIssueProject(ctx, r.PathValue("issue_id"))
		if err != nil || project == nil {
			return nil, ErrForbidden
		}
		return &resolvedResource{organizationID: project.OrganizationID, projectID: project.ID}, nil
	case ResourceEventPath:
		project, err := a.store.ResolveEventProject(ctx, r.PathValue("event_id"))
		if err != nil || project == nil {
			return nil, ErrForbidden
		}
		return &resolvedResource{organizationID: project.OrganizationID, projectID: project.ID}, nil
	default:
		return nil, ErrForbidden
	}
}

func (a *Authorizer) authorize(ctx context.Context, principal *Principal, scope string, resource *resolvedResource) error {
	if principal == nil {
		return ErrInvalidCredentials
	}

	if principal.Kind == CredentialAutomationToken {
		if resource == nil || resource.projectID == "" || principal.ProjectID != resource.projectID {
			return ErrForbidden
		}
		if !principalHasScope(principal, scope) {
			return ErrForbidden
		}
		return nil
	}

	if principal.User == nil {
		return ErrInvalidCredentials
	}

	if resource == nil || resource.organizationID == "" {
		roles, err := a.store.ListUserOrgRoles(ctx, principal.User.ID)
		if err != nil {
			return err
		}
		for _, role := range roles {
			if roleAllowsScope(role, scope) && (principal.Kind != CredentialPAT || principalHasScope(principal, scope)) {
				return nil
			}
		}
		return ErrForbidden
	}

	role, err := a.store.LookupUserOrgRole(ctx, principal.User.ID, resource.organizationID)
	if err != nil {
		return err
	}
	if !roleAllowsScope(role, scope) {
		return ErrForbidden
	}
	if principal.Kind == CredentialPAT && !principalHasScope(principal, scope) {
		return ErrForbidden
	}

	// When a project-level resource is resolved, check the granular project
	// role and deny if it is more restrictive than the org role.
	if resource.projectID != "" {
		projectRole, err := a.store.LookupUserProjectRole(ctx, principal.User.ID, resource.projectID)
		if err != nil {
			return err
		}
		if projectRole != "" && !projectRoleAllowsScope(projectRole, scope) {
			return ErrForbidden
		}
	}

	return nil
}

func roleAllowsScope(role, required string) bool {
	var granted []string
	switch role {
	case "owner":
		granted = []string{ScopeOrgAdmin}
	case "admin":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeOrgQueryWrite,
			ScopeProjectRead,
			ScopeProjectWrite,
			ScopeProjectKeysRead,
			ScopeProjectKeysWrite,
			ScopeProjectTokensRead,
			ScopeProjectTokensWrite,
			ScopeProjectArtifactsWrite,
			ScopeIssueWrite,
			ScopeReleaseRead,
			ScopeReleaseWrite,
		}
	case "member":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeOrgQueryWrite,
			ScopeProjectRead,
			ScopeIssueWrite,
			ScopeReleaseRead,
		}
	case "viewer":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeProjectRead,
			ScopeReleaseRead,
		}
	default:
		return false
	}
	for _, scope := range granted {
		if scopeImplies(scope, required) {
			return true
		}
	}
	return false
}

// projectRoleAllowsScope checks whether a project-level role grants the
// required scope. Project roles are a refinement on top of the org role:
//   - owner:  full control (same as org admin)
//   - admin:  settings, alerts, integrations, triage, queries
//   - member: triage issues, saved queries, read everything
//   - viewer: read-only access
func projectRoleAllowsScope(role, required string) bool {
	var granted []string
	switch role {
	case "owner":
		granted = []string{ScopeOrgAdmin}
	case "admin":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeOrgQueryWrite,
			ScopeProjectRead,
			ScopeProjectWrite,
			ScopeProjectKeysRead,
			ScopeProjectKeysWrite,
			ScopeProjectTokensRead,
			ScopeProjectTokensWrite,
			ScopeProjectArtifactsWrite,
			ScopeIssueWrite,
			ScopeReleaseRead,
			ScopeReleaseWrite,
		}
	case "member":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeOrgQueryWrite,
			ScopeProjectRead,
			ScopeIssueWrite,
			ScopeReleaseRead,
		}
	case "viewer":
		granted = []string{
			ScopeOrgRead,
			ScopeOrgQueryRead,
			ScopeProjectRead,
			ScopeReleaseRead,
		}
	default:
		return false
	}
	for _, scope := range granted {
		if scopeImplies(scope, required) {
			return true
		}
	}
	return false
}

func principalHasScope(principal *Principal, required string) bool {
	if principal == nil {
		return false
	}
	for scope := range principal.Scopes {
		if scopeImplies(scope, required) {
			return true
		}
	}
	return false
}

func scopeImplies(granted, required string) bool {
	if granted == required {
		return true
	}
	switch granted {
	case ScopeOrgAdmin:
		return true
	case ScopeOrgQueryWrite:
		return required == ScopeOrgQueryRead
	case ScopeProjectWrite:
		return required == ScopeProjectRead
	case ScopeProjectKeysWrite:
		return required == ScopeProjectKeysRead
	case ScopeProjectTokensWrite:
		return required == ScopeProjectTokensRead
	case ScopeReleaseWrite:
		return required == ScopeReleaseRead
	}
	return false
}

func extractBearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}
