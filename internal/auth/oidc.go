package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Standard OIDC errors.
var (
	ErrOIDCNotConfigured   = errors.New("OIDC provider not configured for this organization")
	ErrOIDCDiscoveryFailed = errors.New("OIDC discovery endpoint unreachable")
	ErrOIDCTokenExchange   = errors.New("OIDC token exchange failed")
	ErrOIDCInvalidState    = errors.New("OIDC state parameter mismatch")
	ErrOIDCClaimsMissing   = errors.New("required OIDC claims (email) missing from token")
)

// OIDCOrgConfig stores the OIDC configuration for one organization.
// Supports Google, Okta, Azure AD, and any standard OIDC-compliant provider.
type OIDCOrgConfig struct {
	OrganizationID string `json:"organizationId"`
	Issuer         string `json:"issuer"`         // e.g. https://accounts.google.com
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	RedirectURI    string `json:"redirectUri"`     // e.g. https://urgentry.example.com/auth/oidc/callback
	Scopes         string `json:"scopes"`          // space-separated, default "openid email profile"
	Enabled        bool   `json:"enabled"`
}

// EffectiveScopes returns the scopes to request, falling back to "openid email profile".
func (c *OIDCOrgConfig) EffectiveScopes() string {
	if strings.TrimSpace(c.Scopes) != "" {
		return c.Scopes
	}
	return "openid email profile"
}

// OIDCDiscoveryDocument is the subset of the OpenID Connect discovery response
// needed for the authorization code flow.
type OIDCDiscoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// OIDCTokenResponse is the provider's token endpoint reply.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// OIDCUserInfo holds the auto-provisioned user claims.
type OIDCUserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// OIDCConfigStore persists per-organization OIDC configs.
type OIDCConfigStore interface {
	GetOIDCConfig(ctx context.Context, organizationID string) (*OIDCOrgConfig, error)
	SaveOIDCConfig(ctx context.Context, cfg *OIDCOrgConfig) error
}

// OIDCUserProvisioner creates or looks up a user from OIDC claims.
type OIDCUserProvisioner interface {
	EnsureOIDCUser(ctx context.Context, organizationID, email, displayName, oidcSub string) (*User, error)
}

// OIDCSessionCreator creates a session for an OIDC-authenticated user.
type OIDCSessionCreator interface {
	CreateSession(ctx context.Context, userID, userAgent, ipAddress string, ttl time.Duration) (rawToken string, principal *Principal, err error)
}

// OIDCProvider implements the OIDC authorization code flow.
type OIDCProvider struct {
	ConfigStore  OIDCConfigStore
	Provisioner  OIDCUserProvisioner
	Sessions     OIDCSessionCreator
	HTTPClient   *http.Client
	SessionTTL   time.Duration

	mu        sync.RWMutex
	discovery map[string]*cachedDiscovery // keyed by issuer
	states    map[string]*oidcPendingState
}

type cachedDiscovery struct {
	doc       *OIDCDiscoveryDocument
	fetchedAt time.Time
}

type oidcPendingState struct {
	organizationID string
	createdAt      time.Time
}

// NewOIDCProvider creates an OIDCProvider with the given dependencies.
func NewOIDCProvider(configStore OIDCConfigStore, provisioner OIDCUserProvisioner, sessions OIDCSessionCreator) *OIDCProvider {
	return &OIDCProvider{
		ConfigStore: configStore,
		Provisioner: provisioner,
		Sessions:    sessions,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
		SessionTTL:  30 * 24 * time.Hour,
		discovery:   make(map[string]*cachedDiscovery),
		states:      make(map[string]*oidcPendingState),
	}
}

// Discover fetches (and caches for 1 hour) the OIDC discovery document for the issuer.
func (p *OIDCProvider) Discover(ctx context.Context, issuer string) (*OIDCDiscoveryDocument, error) {
	p.mu.RLock()
	cached, ok := p.discovery[issuer]
	p.mu.RUnlock()
	if ok && time.Since(cached.fetchedAt) < time.Hour {
		return cached.doc, nil
	}

	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOIDCDiscoveryFailed, err)
	}

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOIDCDiscoveryFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrOIDCDiscoveryFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrOIDCDiscoveryFailed, err)
	}

	var doc OIDCDiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: parse: %w", ErrOIDCDiscoveryFailed, err)
	}

	p.mu.Lock()
	p.discovery[issuer] = &cachedDiscovery{doc: &doc, fetchedAt: time.Now()}
	p.mu.Unlock()

	return &doc, nil
}

// AuthorizationURL builds the redirect URL to start the OIDC authorization code flow.
func (p *OIDCProvider) AuthorizationURL(ctx context.Context, organizationID string) (string, error) {
	cfg, err := p.ConfigStore.GetOIDCConfig(ctx, organizationID)
	if err != nil || cfg == nil || !cfg.Enabled {
		return "", ErrOIDCNotConfigured
	}

	doc, err := p.Discover(ctx, cfg.Issuer)
	if err != nil {
		return "", err
	}

	state := p.generateState(organizationID)

	params := url.Values{
		"response_type": {"code"},
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {cfg.RedirectURI},
		"scope":         {cfg.EffectiveScopes()},
		"state":         {state},
	}

	return doc.AuthorizationEndpoint + "?" + params.Encode(), nil
}

// HandleCallback processes the /auth/oidc/callback request. It exchanges the
// authorization code for tokens, fetches user info, auto-provisions the user,
// and returns a session token.
func (p *OIDCProvider) HandleCallback(ctx context.Context, code, state, userAgent, ipAddress string) (sessionToken string, user *User, err error) {
	// Validate state
	orgID, ok := p.consumeState(state)
	if !ok {
		return "", nil, ErrOIDCInvalidState
	}

	cfg, err := p.ConfigStore.GetOIDCConfig(ctx, orgID)
	if err != nil || cfg == nil || !cfg.Enabled {
		return "", nil, ErrOIDCNotConfigured
	}

	doc, err := p.Discover(ctx, cfg.Issuer)
	if err != nil {
		return "", nil, err
	}

	// Exchange authorization code for tokens
	tokenResp, err := p.exchangeCode(ctx, doc.TokenEndpoint, cfg, code)
	if err != nil {
		return "", nil, err
	}

	// Fetch user info
	userInfo, err := p.fetchUserInfo(ctx, doc.UserInfoEndpoint, tokenResp.AccessToken)
	if err != nil {
		return "", nil, err
	}

	if strings.TrimSpace(userInfo.Email) == "" {
		return "", nil, ErrOIDCClaimsMissing
	}

	displayName := userInfo.Name
	if displayName == "" {
		displayName = userInfo.Email
	}

	// Auto-provision user
	provisionedUser, err := p.Provisioner.EnsureOIDCUser(ctx, orgID, userInfo.Email, displayName, userInfo.Sub)
	if err != nil {
		return "", nil, fmt.Errorf("provision OIDC user: %w", err)
	}

	// Create session
	token, _, err := p.Sessions.CreateSession(ctx, provisionedUser.ID, userAgent, ipAddress, p.SessionTTL)
	if err != nil {
		return "", nil, fmt.Errorf("create OIDC session: %w", err)
	}

	return token, provisionedUser, nil
}

// exchangeCode performs the token exchange with the OIDC provider.
func (p *OIDCProvider) exchangeCode(ctx context.Context, tokenEndpoint string, cfg *OIDCOrgConfig, code string) (*OIDCTokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {cfg.RedirectURI},
		"client_id":    {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrOIDCTokenExchange, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOIDCTokenExchange, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrOIDCTokenExchange, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", ErrOIDCTokenExchange, resp.StatusCode, string(body))
	}

	var tokenResp OIDCTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("%w: parse: %w", ErrOIDCTokenExchange, err)
	}

	return &tokenResp, nil
}

// fetchUserInfo calls the OIDC userinfo endpoint with the access token.
func (p *OIDCProvider) fetchUserInfo(ctx context.Context, userInfoEndpoint, accessToken string) (*OIDCUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("userinfo read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo status %d: %s", resp.StatusCode, string(body))
	}

	var info OIDCUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("userinfo parse: %w", err)
	}

	return &info, nil
}

func (p *OIDCProvider) generateState(organizationID string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	state := base64.RawURLEncoding.EncodeToString(b)

	p.mu.Lock()
	defer p.mu.Unlock()

	// Evict stale states (older than 10 minutes)
	now := time.Now()
	for k, v := range p.states {
		if now.Sub(v.createdAt) > 10*time.Minute {
			delete(p.states, k)
		}
	}

	p.states[state] = &oidcPendingState{
		organizationID: organizationID,
		createdAt:      now,
	}

	return state
}

func (p *OIDCProvider) consumeState(state string) (organizationID string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pending, found := p.states[state]
	if !found {
		return "", false
	}
	delete(p.states, state)

	if time.Since(pending.createdAt) > 10*time.Minute {
		return "", false
	}

	return pending.organizationID, true
}

func (p *OIDCProvider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

// MemoryOIDCConfigStore is an in-memory OIDCConfigStore for testing and development.
type MemoryOIDCConfigStore struct {
	mu      sync.RWMutex
	configs map[string]*OIDCOrgConfig // keyed by organizationID
}

// NewMemoryOIDCConfigStore creates an empty in-memory OIDC config store.
func NewMemoryOIDCConfigStore() *MemoryOIDCConfigStore {
	return &MemoryOIDCConfigStore{
		configs: make(map[string]*OIDCOrgConfig),
	}
}

// GetOIDCConfig returns the OIDC config for the organization, or ErrOIDCNotConfigured.
func (s *MemoryOIDCConfigStore) GetOIDCConfig(_ context.Context, organizationID string) (*OIDCOrgConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.configs[organizationID]
	if !ok {
		return nil, ErrOIDCNotConfigured
	}
	cp := *cfg
	return &cp, nil
}

// SaveOIDCConfig persists the OIDC config for an organization.
func (s *MemoryOIDCConfigStore) SaveOIDCConfig(_ context.Context, cfg *OIDCOrgConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.OrganizationID) == "" {
		return fmt.Errorf("organization ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *cfg
	s.configs[cp.OrganizationID] = &cp
	return nil
}
