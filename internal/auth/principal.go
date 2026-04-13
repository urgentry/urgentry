package auth

import "context"

// CredentialKind identifies the credential type used for a request.
type CredentialKind string

const (
	CredentialProjectKey      CredentialKind = "project_key"
	CredentialSession         CredentialKind = "session"
	CredentialPAT             CredentialKind = "personal_access_token"
	CredentialAutomationToken CredentialKind = "project_automation_token"
)

// User is the authenticated local Urgentry user.
type User struct {
	ID          string
	Email       string
	DisplayName string
}

// Principal is the authenticated identity attached to a request.
type Principal struct {
	Kind         CredentialKind
	CredentialID string
	User         *User
	ProjectID    string
	Scopes       map[string]struct{}
	CSRFToken    string
}

// HasScope reports whether a principal explicitly carries a scope.
func (p *Principal) HasScope(scope string) bool {
	if p == nil || len(p.Scopes) == 0 {
		return false
	}
	_, ok := p.Scopes[scope]
	return ok
}

type principalContextKey struct{}

// WithPrincipal stores an authenticated principal in the context.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext retrieves the authenticated principal from the context.
func PrincipalFromContext(ctx context.Context) *Principal {
	principal, _ := ctx.Value(principalContextKey{}).(*Principal)
	return principal
}
