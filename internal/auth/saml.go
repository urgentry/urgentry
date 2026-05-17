package auth

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	crewjamsaml "github.com/crewjam/saml"

	"urgentry/internal/httputil"
	"urgentry/internal/requestmeta"
)

// ---------------------------------------------------------------------------
// SAML per-org configuration
// ---------------------------------------------------------------------------

// SAMLConfig stores the SAML settings for one organization, matching the
// oidc_configs table pattern.
type SAMLConfig struct {
	OrganizationID string
	EntityID       string // IdP entity ID
	SSOURL         string // IdP single sign-on URL
	Certificate    string // PEM-encoded IdP signing certificate
	SPEntityID     string // this SP's entity ID
	ACSURL         string // assertion consumer service URL
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SAMLConfigStore persists per-org SAML configuration.
type SAMLConfigStore interface {
	GetSAMLConfig(ctx context.Context, orgID string) (*SAMLConfig, error)
	UpsertSAMLConfig(ctx context.Context, cfg *SAMLConfig) error
}

// SAMLUser is the identity extracted from a SAML assertion.
type SAMLUser struct {
	NameID      string
	Email       string
	DisplayName string
	FirstName   string
	LastName    string
}

// extractSAMLUser pulls common attributes from a verified assertion.
func extractSAMLUser(assertion *crewjamsaml.Assertion) SAMLUser {
	var u SAMLUser
	if assertion == nil {
		return u
	}
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		u.NameID = assertion.Subject.NameID.Value
	}
	for _, statement := range assertion.AttributeStatements {
		for _, attr := range statement.Attributes {
			if len(attr.Values) == 0 {
				continue
			}
			v := attr.Values[0].Value
			name := strings.ToLower(strings.TrimSpace(attr.Name + " " + attr.FriendlyName))
			switch {
			case strings.Contains(name, "emailaddress") ||
				strings.Contains(name, "email") ||
				strings.Contains(name, "mail"):
				u.Email = v
			case strings.Contains(name, "displayname") ||
				strings.Contains(name, "commonname") ||
				strings.Contains(name, " cn") ||
				strings.TrimSpace(name) == "name":
				u.DisplayName = v
			case strings.Contains(name, "givenname") ||
				strings.Contains(name, "firstname"):
				u.FirstName = v
			case strings.Contains(name, "surname") ||
				strings.Contains(name, "lastname"):
				u.LastName = v
			}
		}
	}
	if u.Email == "" && strings.Contains(u.NameID, "@") {
		u.Email = u.NameID
	}
	if u.DisplayName == "" && (u.FirstName != "" || u.LastName != "") {
		u.DisplayName = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	return u
}

func parseSAMLSigningCertificate(pemCert string) (*x509.Certificate, string, error) {
	block, _ := pem.Decode([]byte(pemCert))
	if block == nil {
		return nil, "", errors.New("failed to decode PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse certificate: %w", err)
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, "", errors.New("IdP certificate is not within its validity period")
	}
	return cert, base64.StdEncoding.EncodeToString(cert.Raw), nil
}

func samlServiceProvider(cfg *SAMLConfig) (*crewjamsaml.ServiceProvider, error) {
	if cfg == nil {
		return nil, errors.New("SAML config is required")
	}
	_, certData, err := parseSAMLSigningCertificate(cfg.Certificate)
	if err != nil {
		return nil, err
	}
	acsURL, err := url.Parse(strings.TrimSpace(cfg.ACSURL))
	if err != nil || acsURL.Scheme == "" || acsURL.Host == "" {
		return nil, errors.New("SAML ACS URL must be an absolute URL")
	}
	metadataURL, err := url.Parse(strings.TrimSpace(cfg.SPEntityID))
	if err != nil || metadataURL.Scheme == "" || metadataURL.Host == "" {
		metadataURL = &url.URL{
			Scheme: acsURL.Scheme,
			Host:   acsURL.Host,
			Path:   strings.TrimSuffix(acsURL.Path, "/acs") + "/metadata",
		}
	}
	spEntityID := strings.TrimSpace(cfg.SPEntityID)
	if spEntityID == "" {
		spEntityID = metadataURL.String()
	}
	idpEntityID := strings.TrimSpace(cfg.EntityID)
	if idpEntityID == "" {
		return nil, errors.New("SAML IdP entity ID is required")
	}
	return &crewjamsaml.ServiceProvider{
		EntityID:          spEntityID,
		MetadataURL:       *metadataURL,
		AcsURL:            *acsURL,
		IDPMetadata:       &crewjamsaml.EntityDescriptor{EntityID: idpEntityID},
		IDPCertificate:    &certData,
		AllowIDPInitiated: true,
		AuthnNameIDFormat: crewjamsaml.EmailAddressNameIDFormat,
	}, nil
}

func validateSAMLResponse(r *http.Request, cfg *SAMLConfig) (*crewjamsaml.Assertion, error) {
	sp, err := samlServiceProvider(cfg)
	if err != nil {
		return nil, err
	}
	assertion, err := sp.ParseResponse(r, nil)
	if err != nil {
		return nil, err
	}
	if assertion == nil {
		return nil, errors.New("SAML assertion missing")
	}
	return assertion, nil
}

func assertionReplayExpiry(assertion *crewjamsaml.Assertion, now time.Time) time.Time {
	if assertion != nil && assertion.Conditions != nil && !assertion.Conditions.NotOnOrAfter.IsZero() {
		return assertion.Conditions.NotOnOrAfter.Add(5 * time.Minute)
	}
	return now.Add(5 * time.Minute)
}

// markSAMLAssertionSeen returns false when the assertion ID has already been
// accepted by this provider instance.
func (p *SAMLProvider) markSAMLAssertionSeen(assertion *crewjamsaml.Assertion) bool {
	if assertion == nil || strings.TrimSpace(assertion.ID) == "" {
		return false
	}
	now := time.Now()
	expiresAt := assertionReplayExpiry(assertion, now)
	p.replayMu.Lock()
	defer p.replayMu.Unlock()
	for id, expiry := range p.seenAssertions {
		if !expiry.After(now) {
			delete(p.seenAssertions, id)
		}
	}
	id := strings.TrimSpace(assertion.ID)
	if expiry, ok := p.seenAssertions[id]; ok && expiry.After(now) {
		return false
	}
	p.seenAssertions[id] = expiresAt
	return true
}

func samlPublicError(err error) string {
	if err == nil {
		return "Invalid SAML response."
	}
	var badStatus crewjamsaml.ErrBadStatus
	if errors.As(err, &badStatus) {
		return "Invalid SAML response status."
	}
	return "Invalid SAML response."
}

// idpMetadata is the minimal IdP metadata XML structure.
type idpMetadata struct {
	XMLName  xml.Name `xml:"EntityDescriptor"`
	EntityID string   `xml:"entityID,attr"`
	SSO      []idpSSO `xml:"IDPSSODescriptor>SingleSignOnService"`
	Certs    []string `xml:"IDPSSODescriptor>KeyDescriptor>KeyInfo>X509Data>X509Certificate"`
}

type idpSSO struct {
	Binding  string `xml:"Binding,attr"`
	Location string `xml:"Location,attr"`
}

// ParseIdPMetadata extracts entity ID, SSO URL, and certificate from IdP
// metadata XML.
func ParseIdPMetadata(data []byte) (entityID, ssoURL, certPEM string, err error) {
	var md idpMetadata
	if err := xml.Unmarshal(data, &md); err != nil {
		return "", "", "", fmt.Errorf("parse IdP metadata: %w", err)
	}
	entityID = md.EntityID
	for _, sso := range md.SSO {
		if strings.Contains(sso.Binding, "HTTP-POST") || strings.Contains(sso.Binding, "HTTP-Redirect") {
			ssoURL = sso.Location
			break
		}
	}
	if len(md.Certs) > 0 {
		raw := strings.TrimSpace(md.Certs[0])
		certPEM = "-----BEGIN CERTIFICATE-----\n" + raw + "\n-----END CERTIFICATE-----"
	}
	if entityID == "" {
		return "", "", "", errors.New("IdP metadata missing entityID")
	}
	if ssoURL == "" {
		return "", "", "", errors.New("IdP metadata missing SSO URL")
	}
	return entityID, ssoURL, certPEM, nil
}

// ---------------------------------------------------------------------------
// SAMLProvider wires config, assertion parsing, and user provisioning.
// ---------------------------------------------------------------------------

// SAMLUserProvisioner is the callback the SAML provider uses to find or
// create a local user from a SAML assertion. It mirrors the OIDC provisioning
// flow so callers can reuse the same store logic.
type SAMLUserProvisioner interface {
	FindOrCreateSAMLUser(ctx context.Context, orgID string, user SAMLUser) (*User, error)
}

// SAMLProvider handles SP metadata generation, IdP metadata import, and the
// ACS endpoint.
type SAMLProvider struct {
	configStore SAMLConfigStore
	provisioner SAMLUserProvisioner
	authStore   Store
	sessionTTL  time.Duration

	replayMu       sync.Mutex
	seenAssertions map[string]time.Time
}

// NewSAMLProvider creates a SAML authentication provider.
func NewSAMLProvider(cfgStore SAMLConfigStore, provisioner SAMLUserProvisioner, authStore Store, sessionTTL time.Duration) *SAMLProvider {
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}
	return &SAMLProvider{
		configStore: cfgStore,
		provisioner: provisioner,
		authStore:   authStore,
		sessionTTL:  sessionTTL,

		seenAssertions: make(map[string]time.Time),
	}
}

// ---------------------------------------------------------------------------
// SP Metadata endpoint: GET /auth/saml/metadata
// ---------------------------------------------------------------------------

const spMetadataTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata"
    entityID="%s">
  <md:SPSSODescriptor
      AuthnRequestsSigned="false"
      WantAssertionsSigned="true"
      protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <md:NameIDFormat>urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress</md:NameIDFormat>
    <md:AssertionConsumerService
        Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"
        Location="%s"
        index="0"
        isDefault="true"/>
  </md:SPSSODescriptor>
</md:EntityDescriptor>`

// HandleMetadata returns an http.HandlerFunc that serves SP metadata XML for
// the given organization.
func (p *SAMLProvider) HandleMetadata(orgID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := p.configStore.GetSAMLConfig(r.Context(), orgID)
		if err != nil || cfg == nil {
			httputil.WriteError(w, http.StatusNotFound, "SAML not configured for this organization.")
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, spMetadataTemplate, cfg.SPEntityID, cfg.ACSURL)
	}
}

// ---------------------------------------------------------------------------
// ACS endpoint: POST /auth/saml/acs
// ---------------------------------------------------------------------------

// HandleACS returns an http.HandlerFunc that processes the IdP's SAML
// Response POST, provisions a local user, creates a session, and redirects
// to the app.
func (p *SAMLProvider) HandleACS(orgID, sessionCookieName, csrfCookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httputil.WriteError(w, http.StatusMethodNotAllowed, "POST required.")
			return
		}
		if err := r.ParseForm(); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid form data.")
			return
		}
		encoded := r.FormValue("SAMLResponse")
		if encoded == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing SAMLResponse.")
			return
		}

		cfg, err := p.configStore.GetSAMLConfig(r.Context(), orgID)
		if err != nil || cfg == nil {
			httputil.WriteError(w, http.StatusInternalServerError, "SAML not configured.")
			return
		}

		assertion, err := validateSAMLResponse(r, cfg)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, samlPublicError(err))
			return
		}
		if !p.markSAMLAssertionSeen(assertion) {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid SAML response.")
			return
		}

		samlUser := extractSAMLUser(assertion)
		if samlUser.Email == "" {
			httputil.WriteError(w, http.StatusBadRequest, "SAML assertion missing email.")
			return
		}

		// Provision or find local user (reuses OIDC-style provisioning).
		user, err := p.provisioner.FindOrCreateSAMLUser(r.Context(), orgID, samlUser)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "User provisioning failed.")
			return
		}

		// Create session.
		rawToken, principal, err := p.authStore.CreateSession(r.Context(), user.ID, r.UserAgent(), requestmeta.ClientIP(r), p.sessionTTL)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Session creation failed.")
			return
		}

		secure := requestmeta.IsSecure(r)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    rawToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   int(p.sessionTTL.Seconds()),
		})
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookieName,
			Value:    principal.CSRFToken,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   int(p.sessionTTL.Seconds()),
		})

		// Redirect to RelayState or root.
		next := r.FormValue("RelayState")
		if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	}
}

// RegisterSAMLRoutes registers /auth/saml/metadata and /auth/saml/acs on
// the given mux.
func RegisterSAMLRoutes(mux *http.ServeMux, provider *SAMLProvider, orgID, sessionCookieName, csrfCookieName string) {
	mux.Handle("GET /auth/saml/metadata", provider.HandleMetadata(orgID))
	mux.Handle("POST /auth/saml/acs", provider.HandleACS(orgID, sessionCookieName, csrfCookieName))
}

// RegisterOrgSAMLRoutes registers org-scoped metadata and ACS routes that
// resolve the organization from the path at request time.
func RegisterOrgSAMLRoutes(mux *http.ServeMux, store Store, provider *SAMLProvider, sessionCookieName, csrfCookieName string) {
	if mux == nil || store == nil || provider == nil {
		return
	}

	resolveOrg := func(w http.ResponseWriter, r *http.Request) (*Organization, bool) {
		org, err := store.ResolveOrganizationBySlug(r.Context(), strings.TrimSpace(r.PathValue("org_slug")))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve organization.")
			return nil, false
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return nil, false
		}
		return org, true
	}

	mux.Handle("GET /auth/saml/{org_slug}/metadata", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, ok := resolveOrg(w, r)
		if !ok {
			return
		}
		provider.HandleMetadata(org.ID).ServeHTTP(w, r)
	}))
	mux.Handle("POST /auth/saml/{org_slug}/acs", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, ok := resolveOrg(w, r)
		if !ok {
			return
		}
		provider.HandleACS(org.ID, sessionCookieName, csrfCookieName).ServeHTTP(w, r)
	}))
}

// MemorySAMLConfigStore is an in-memory SAMLConfigStore for tests and
// development.
type MemorySAMLConfigStore struct {
	mu      sync.RWMutex
	configs map[string]*SAMLConfig
}

// NewMemorySAMLConfigStore creates an empty in-memory SAML config store.
func NewMemorySAMLConfigStore() *MemorySAMLConfigStore {
	return &MemorySAMLConfigStore{
		configs: make(map[string]*SAMLConfig),
	}
}

// GetSAMLConfig returns the SAML config for an organization, or nil when it is
// not configured.
func (s *MemorySAMLConfigStore) GetSAMLConfig(_ context.Context, orgID string) (*SAMLConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg, ok := s.configs[orgID]
	if !ok {
		return nil, nil
	}
	configCopy := *cfg
	return &configCopy, nil
}

// UpsertSAMLConfig persists the SAML config for an organization.
func (s *MemorySAMLConfigStore) UpsertSAMLConfig(_ context.Context, cfg *SAMLConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.OrganizationID) == "" {
		return errors.New("organization id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	configCopy := *cfg
	if configCopy.CreatedAt.IsZero() {
		configCopy.CreatedAt = now
	}
	configCopy.UpdatedAt = now
	s.configs[configCopy.OrganizationID] = &configCopy
	return nil
}
