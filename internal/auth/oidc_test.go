package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDCOrgConfig_EffectiveScopes(t *testing.T) {
	t.Helper()
	tests := []struct {
		name   string
		scopes string
		want   string
	}{
		{"default", "", "openid email profile"},
		{"custom", "openid email", "openid email"},
		{"whitespace only", "   ", "openid email profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &OIDCOrgConfig{Scopes: tt.scopes}
			if got := cfg.EffectiveScopes(); got != tt.want {
				t.Fatalf("EffectiveScopes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMemoryOIDCConfigStore(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	store := NewMemoryOIDCConfigStore()

	// Not found
	_, err := store.GetOIDCConfig(ctx, "org-1")
	if err != ErrOIDCNotConfigured {
		t.Fatalf("expected ErrOIDCNotConfigured, got %v", err)
	}

	// Save
	cfg := &OIDCOrgConfig{
		OrganizationID: "org-1",
		Issuer:         "https://accounts.google.com",
		ClientID:       "client-id",
		ClientSecret:   "client-secret",
		RedirectURI:    "https://urgentry.example.com/auth/oidc/callback",
		Enabled:        true,
	}
	if err := store.SaveOIDCConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveOIDCConfig: %v", err)
	}

	// Retrieve
	got, err := store.GetOIDCConfig(ctx, "org-1")
	if err != nil {
		t.Fatalf("GetOIDCConfig: %v", err)
	}
	if got.Issuer != cfg.Issuer || got.ClientID != cfg.ClientID {
		t.Fatalf("config mismatch: got %+v", got)
	}

	// Save requires org ID
	if err := store.SaveOIDCConfig(ctx, &OIDCOrgConfig{}); err == nil {
		t.Fatal("expected error for empty org ID")
	}
}

type stubOIDCProvisioner struct{}

func (s *stubOIDCProvisioner) EnsureOIDCUser(_ context.Context, _, email, displayName, _ string) (*User, error) {
	return &User{ID: "u-1", Email: email, DisplayName: displayName}, nil
}

type stubOIDCSessionCreator struct{}

func (s *stubOIDCSessionCreator) CreateSession(_ context.Context, userID, _, _ string, _ time.Duration) (string, *Principal, error) {
	return "session-token-abc", &Principal{Kind: CredentialSession, User: &User{ID: userID}}, nil
}

func TestOIDCProvider_Discover(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	doc := OIDCDiscoveryDocument{
		Issuer:                "https://test-issuer.example.com",
		AuthorizationEndpoint: "https://test-issuer.example.com/authorize",
		TokenEndpoint:         "https://test-issuer.example.com/token",
		UserInfoEndpoint:      "https://test-issuer.example.com/userinfo",
		JWKSURI:               "https://test-issuer.example.com/.well-known/jwks.json",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	provider := NewOIDCProvider(NewMemoryOIDCConfigStore(), &stubOIDCProvisioner{}, &stubOIDCSessionCreator{})
	provider.HTTPClient = srv.Client()

	got, err := provider.Discover(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got.AuthorizationEndpoint != doc.AuthorizationEndpoint {
		t.Fatalf("AuthorizationEndpoint = %q, want %q", got.AuthorizationEndpoint, doc.AuthorizationEndpoint)
	}

	// Second call should use cache
	got2, err := provider.Discover(ctx, srv.URL)
	if err != nil {
		t.Fatalf("Discover (cached): %v", err)
	}
	if got2.TokenEndpoint != doc.TokenEndpoint {
		t.Fatalf("cached TokenEndpoint = %q, want %q", got2.TokenEndpoint, doc.TokenEndpoint)
	}
}

func TestOIDCProvider_AuthorizationURL(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	doc := OIDCDiscoveryDocument{
		Issuer:                "https://test-issuer.example.com",
		AuthorizationEndpoint: "https://test-issuer.example.com/authorize",
		TokenEndpoint:         "https://test-issuer.example.com/token",
		UserInfoEndpoint:      "https://test-issuer.example.com/userinfo",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()

	cfgStore := NewMemoryOIDCConfigStore()
	_ = cfgStore.SaveOIDCConfig(ctx, &OIDCOrgConfig{
		OrganizationID: "org-1",
		Issuer:         srv.URL,
		ClientID:       "test-client",
		ClientSecret:   "test-secret",
		RedirectURI:    "https://urgentry.example.com/auth/oidc/callback",
		Enabled:        true,
	})

	provider := NewOIDCProvider(cfgStore, &stubOIDCProvisioner{}, &stubOIDCSessionCreator{})
	provider.HTTPClient = srv.Client()

	authURL, err := provider.AuthorizationURL(ctx, "org-1")
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	if authURL == "" {
		t.Fatal("expected non-empty authorization URL")
	}
	if !containsSubstring(authURL, "client_id=test-client") {
		t.Fatalf("URL missing client_id: %s", authURL)
	}
	if !containsSubstring(authURL, "response_type=code") {
		t.Fatalf("URL missing response_type: %s", authURL)
	}

	// Unconfigured org
	_, err = provider.AuthorizationURL(ctx, "org-missing")
	if err != ErrOIDCNotConfigured {
		t.Fatalf("expected ErrOIDCNotConfigured for missing org, got %v", err)
	}
}

func TestOIDCProvider_HandleCallback(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	tokenResp := OIDCTokenResponse{
		AccessToken: "access-token-123",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		IDToken:     "id-token-xyz",
	}
	userInfo := OIDCUserInfo{
		Sub:   "oidc-sub-456",
		Email: "alice@example.com",
		Name:  "Alice Smith",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(OIDCDiscoveryDocument{
				Issuer:                "https://test-issuer.example.com",
				AuthorizationEndpoint: "https://test-issuer.example.com/authorize",
				TokenEndpoint:         r.Host + "/token",
				UserInfoEndpoint:      r.Host + "/userinfo",
			})
		case "/token":
			_ = json.NewEncoder(w).Encode(tokenResp)
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(userInfo)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgStore := NewMemoryOIDCConfigStore()
	_ = cfgStore.SaveOIDCConfig(ctx, &OIDCOrgConfig{
		OrganizationID: "org-1",
		Issuer:         srv.URL,
		ClientID:       "test-client",
		ClientSecret:   "test-secret",
		RedirectURI:    "https://urgentry.example.com/auth/oidc/callback",
		Enabled:        true,
	})

	provider := NewOIDCProvider(cfgStore, &stubOIDCProvisioner{}, &stubOIDCSessionCreator{})
	provider.HTTPClient = srv.Client()

	// Patch the discovery cache with correct URLs using the test server
	provider.mu.Lock()
	provider.discovery[srv.URL] = &cachedDiscovery{
		doc: &OIDCDiscoveryDocument{
			Issuer:                srv.URL,
			AuthorizationEndpoint: srv.URL + "/authorize",
			TokenEndpoint:         srv.URL + "/token",
			UserInfoEndpoint:      srv.URL + "/userinfo",
		},
		fetchedAt: time.Now(),
	}
	provider.mu.Unlock()

	// Generate a valid state
	state := provider.generateState("org-1")

	token, user, err := provider.HandleCallback(ctx, "auth-code-789", state, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}
	if user == nil {
		t.Fatal("expected non-nil user")
	}
	if user.Email != "alice@example.com" {
		t.Fatalf("Email = %q, want alice@example.com", user.Email)
	}

	// Re-using the same state should fail
	_, _, err = provider.HandleCallback(ctx, "auth-code-789", state, "test-agent", "127.0.0.1")
	if err != ErrOIDCInvalidState {
		t.Fatalf("expected ErrOIDCInvalidState on state reuse, got %v", err)
	}
}

func TestOIDCProvider_StateExpiry(t *testing.T) {
	t.Helper()

	provider := NewOIDCProvider(NewMemoryOIDCConfigStore(), &stubOIDCProvisioner{}, &stubOIDCSessionCreator{})

	// Manually insert an expired state
	provider.mu.Lock()
	provider.states["old-state"] = &oidcPendingState{
		organizationID: "org-1",
		createdAt:      time.Now().Add(-11 * time.Minute),
	}
	provider.mu.Unlock()

	orgID, ok := provider.consumeState("old-state")
	if ok {
		t.Fatalf("expected expired state to be rejected, got orgID=%q", orgID)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsCheck(s, sub))
}

func containsCheck(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
