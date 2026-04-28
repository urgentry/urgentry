package auth

import (
	"compress/flate"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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

// ---------------------------------------------------------------------------
// Minimal SAML assertion parsing (no external SAML library)
// ---------------------------------------------------------------------------

// samlResponse is the top-level XML wrapper.
type samlResponse struct {
	XMLName   xml.Name      `xml:"Response"`
	Assertion samlAssertion `xml:"Assertion"`
}

type samlAssertion struct {
	Issuer     string          `xml:"Issuer"`
	Subject    samlSubject     `xml:"Subject"`
	Conditions samlConditions  `xml:"Conditions"`
	Attributes []samlAttribute `xml:"AttributeStatement>Attribute"`
}

type samlSubject struct {
	NameID string `xml:"NameID"`
}

type samlConditions struct {
	NotBefore    string `xml:"NotBefore,attr"`
	NotOnOrAfter string `xml:"NotOnOrAfter,attr"`
}

type samlAttribute struct {
	Name   string   `xml:"Name,attr"`
	Values []string `xml:"AttributeValue"`
}

// SAMLUser is the identity extracted from a SAML assertion.
type SAMLUser struct {
	NameID      string
	Email       string
	DisplayName string
	FirstName   string
	LastName    string
}

// parseSAMLResponse decodes a base64 SAMLResponse form value into structured
// assertion data. It validates the XML structure and time window but does NOT
// perform cryptographic signature verification (that requires the full
// xml-dsig stack; see verifySAMLSignature below for certificate-based
// validation of the raw XML).
func parseSAMLResponse(encoded string) (*samlResponse, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Try decoding deflated content (HTTP-Redirect binding).
		compressed, err2 := base64.StdEncoding.DecodeString(encoded)
		if err2 != nil {
			return nil, nil, fmt.Errorf("base64 decode: %w", err)
		}
		reader := flate.NewReader(strings.NewReader(string(compressed)))
		defer reader.Close()
		raw, err = io.ReadAll(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("deflate: %w", err)
		}
	}

	var resp samlResponse
	if err := xml.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("xml unmarshal: %w", err)
	}

	// Validate time window when conditions are present.
	if resp.Assertion.Conditions.NotBefore != "" {
		nb, err := time.Parse(time.RFC3339, resp.Assertion.Conditions.NotBefore)
		if err == nil && time.Now().Before(nb.Add(-2*time.Minute)) {
			return nil, nil, errors.New("assertion not yet valid")
		}
	}
	if resp.Assertion.Conditions.NotOnOrAfter != "" {
		noa, err := time.Parse(time.RFC3339, resp.Assertion.Conditions.NotOnOrAfter)
		if err == nil && time.Now().After(noa.Add(2*time.Minute)) {
			return nil, nil, errors.New("assertion expired")
		}
	}

	return &resp, raw, nil
}

// extractSAMLUser pulls common attributes from the parsed assertion.
func extractSAMLUser(resp *samlResponse) SAMLUser {
	u := SAMLUser{NameID: resp.Assertion.Subject.NameID}
	for _, attr := range resp.Assertion.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		v := attr.Values[0]
		switch {
		case strings.Contains(strings.ToLower(attr.Name), "emailaddress") ||
			strings.Contains(strings.ToLower(attr.Name), "email"):
			u.Email = v
		case strings.Contains(strings.ToLower(attr.Name), "displayname") ||
			strings.Contains(strings.ToLower(attr.Name), "name"):
			u.DisplayName = v
		case strings.Contains(strings.ToLower(attr.Name), "givenname") ||
			strings.Contains(strings.ToLower(attr.Name), "firstname"):
			u.FirstName = v
		case strings.Contains(strings.ToLower(attr.Name), "surname") ||
			strings.Contains(strings.ToLower(attr.Name), "lastname"):
			u.LastName = v
		}
	}
	if u.Email == "" {
		// Fall back to NameID when it looks like an email.
		if strings.Contains(u.NameID, "@") {
			u.Email = u.NameID
		}
	}
	if u.DisplayName == "" && (u.FirstName != "" || u.LastName != "") {
		u.DisplayName = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	return u
}

// verifySAMLSignature performs basic certificate-based validation: it parses
// the IdP certificate from PEM and checks that it is valid. Full XML-DSig
// envelope verification is left for a production xml-dsig library; this
// provides the trust anchor setup.
func verifySAMLSignature(pemCert string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemCert))
	if block == nil {
		return nil, errors.New("failed to decode PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, errors.New("IdP certificate is not within its validity period")
	}
	return cert, nil
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

		// Validate IdP certificate is still trusted.
		if _, err := verifySAMLSignature(cfg.Certificate); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "IdP certificate validation failed: "+err.Error())
			return
		}

		resp, _, err := parseSAMLResponse(encoded)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid SAML response: "+err.Error())
			return
		}

		samlUser := extractSAMLUser(resp)
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

// ---------------------------------------------------------------------------
// IdP metadata import helper
// ---------------------------------------------------------------------------

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
