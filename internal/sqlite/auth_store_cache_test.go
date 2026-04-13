package sqlite

import (
	"context"
	"testing"

	"urgentry/internal/auth"
)

func TestAuthStorePATCacheInvalidatesOnRevoke(t *testing.T) {
	t.Parallel()

	db := openStoreTestDB(t)
	store := NewAuthStore(db)
	result, err := store.EnsureBootstrapAccess(context.Background(), BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_cache_test_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	if result == nil || result.PAT == "" {
		t.Fatalf("bootstrap result = %+v, want PAT", result)
	}

	principal, err := store.AuthenticatePAT(context.Background(), result.PAT)
	if err != nil {
		t.Fatalf("AuthenticatePAT first: %v", err)
	}
	if principal == nil || principal.User == nil {
		t.Fatalf("principal = %+v, want user", principal)
	}

	tokens, err := store.ListPersonalAccessTokens(context.Background(), principal.User.ID)
	if err != nil {
		t.Fatalf("ListPersonalAccessTokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("len(tokens) = %d, want 1", len(tokens))
	}
	if err := store.RevokePersonalAccessToken(context.Background(), tokens[0].ID, principal.User.ID); err != nil {
		t.Fatalf("RevokePersonalAccessToken: %v", err)
	}

	if _, err := store.AuthenticatePAT(context.Background(), result.PAT); err != auth.ErrInvalidCredentials {
		t.Fatalf("AuthenticatePAT after revoke = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthStoreAutomationCacheInvalidatesOnRevoke(t *testing.T) {
	t.Parallel()

	db := openStoreTestDB(t)
	store := NewAuthStore(db)
	result, err := store.EnsureBootstrapAccess(context.Background(), BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_cache_test_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	if _, err := EnsureDefaultKey(context.Background(), db); err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}
	bootstrapPrincipal, err := store.AuthenticatePAT(context.Background(), result.PAT)
	if err != nil {
		t.Fatalf("AuthenticatePAT bootstrap: %v", err)
	}
	if bootstrapPrincipal == nil || bootstrapPrincipal.User == nil {
		t.Fatalf("bootstrap principal = %+v, want user", bootstrapPrincipal)
	}

	raw, err := store.CreateAutomationToken(context.Background(), "default-project", "Bench", bootstrapPrincipal.User.ID, []string{auth.ScopeProjectRead}, nil, "gaut_cache_token")
	if err != nil {
		t.Fatalf("CreateAutomationToken: %v", err)
	}

	principal, err := store.AuthenticateAutomationToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("AuthenticateAutomationToken first: %v", err)
	}
	if principal == nil || principal.ProjectID != "default-project" {
		t.Fatalf("principal = %+v, want default-project", principal)
	}

	tokens, err := store.ListAutomationTokens(context.Background(), "default-project")
	if err != nil {
		t.Fatalf("ListAutomationTokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("len(tokens) = %d, want 1", len(tokens))
	}
	if err := store.RevokeAutomationToken(context.Background(), tokens[0].ID, "default-project"); err != nil {
		t.Fatalf("RevokeAutomationToken: %v", err)
	}

	if _, err := store.AuthenticateAutomationToken(context.Background(), raw); err != auth.ErrInvalidCredentials {
		t.Fatalf("AuthenticateAutomationToken after revoke = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthStoreProjectResolutionCacheReturnsClones(t *testing.T) {
	t.Parallel()

	db := openStoreTestDB(t)
	store := NewAuthStore(db)
	if _, err := EnsureDefaultKey(context.Background(), db); err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}

	project, err := store.ResolveProjectBySlug(context.Background(), "urgentry-org", "default")
	if err != nil {
		t.Fatalf("ResolveProjectBySlug first: %v", err)
	}
	if project == nil {
		t.Fatal("ResolveProjectBySlug returned nil")
	}
	project.OrganizationSlug = "mutated"

	again, err := store.ResolveProjectBySlug(context.Background(), "urgentry-org", "default")
	if err != nil {
		t.Fatalf("ResolveProjectBySlug second: %v", err)
	}
	if again == nil {
		t.Fatal("ResolveProjectBySlug second returned nil")
	}
	if again.OrganizationSlug != "urgentry-org" {
		t.Fatalf("cached project leaked caller mutation: %+v", again)
	}
}
