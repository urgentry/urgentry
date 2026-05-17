package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	crewjamsaml "github.com/crewjam/saml"
)

type samlRouteStore struct {
	org *Organization
}

func (s samlRouteStore) AuthenticateUserPassword(context.Context, string, string) (*User, error) {
	return nil, ErrInvalidCredentials
}

func (s samlRouteStore) CreateSession(_ context.Context, userID, _, _ string, _ time.Duration) (string, *Principal, error) {
	return "raw-saml-session", &Principal{
		Kind:         CredentialSession,
		CredentialID: "session-saml-1",
		User:         &User{ID: userID, Email: "saml-user@example.com", DisplayName: "SAML User"},
		CSRFToken:    "csrf-saml-token",
	}, nil
}

func (s samlRouteStore) AuthenticateSession(context.Context, string) (*Principal, error) {
	return nil, ErrInvalidCredentials
}

func (s samlRouteStore) RevokeSession(context.Context, string) error { return nil }
func (s samlRouteStore) AuthenticatePAT(context.Context, string) (*Principal, error) {
	return nil, ErrInvalidCredentials
}
func (s samlRouteStore) AuthenticateAutomationToken(context.Context, string) (*Principal, error) {
	return nil, ErrInvalidCredentials
}
func (s samlRouteStore) ResolveOrganizationBySlug(_ context.Context, slug string) (*Organization, error) {
	if s.org != nil && s.org.Slug == slug {
		return s.org, nil
	}
	return nil, nil
}
func (s samlRouteStore) ResolveProjectByID(context.Context, string) (*Project, error) {
	return nil, nil
}
func (s samlRouteStore) ResolveProjectBySlug(context.Context, string, string) (*Project, error) {
	return nil, nil
}
func (s samlRouteStore) ResolveIssueProject(context.Context, string) (*Project, error) {
	return nil, nil
}
func (s samlRouteStore) ResolveEventProject(context.Context, string) (*Project, error) {
	return nil, nil
}
func (s samlRouteStore) LookupUserOrgRole(context.Context, string, string) (string, error) {
	return "", nil
}
func (s samlRouteStore) ListUserOrgRoles(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (s samlRouteStore) LookupUserProjectRole(context.Context, string, string) (string, error) {
	return "", nil
}

type samlRouteProvisioner struct{}

func (samlRouteProvisioner) FindOrCreateSAMLUser(_ context.Context, _ string, user SAMLUser) (*User, error) {
	displayName := strings.TrimSpace(user.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(user.FirstName + " " + user.LastName)
	}
	return &User{ID: "user-saml-1", Email: user.Email, DisplayName: displayName}, nil
}

func TestMemorySAMLConfigStore(t *testing.T) {
	store := NewMemorySAMLConfigStore()

	cfg, err := store.GetSAMLConfig(t.Context(), "org-1")
	if err != nil {
		t.Fatalf("GetSAMLConfig missing: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing org, got %+v", cfg)
	}

	if err := store.UpsertSAMLConfig(t.Context(), &SAMLConfig{
		OrganizationID: "org-1",
		SPEntityID:     "https://sp.example.com/metadata",
		ACSURL:         "https://sp.example.com/auth/saml/acme/acs",
	}); err != nil {
		t.Fatalf("UpsertSAMLConfig: %v", err)
	}

	cfg, err = store.GetSAMLConfig(t.Context(), "org-1")
	if err != nil {
		t.Fatalf("GetSAMLConfig existing: %v", err)
	}
	if cfg == nil || cfg.SPEntityID != "https://sp.example.com/metadata" {
		t.Fatalf("unexpected stored config: %+v", cfg)
	}
}

func TestRegisterOrgSAMLRoutes_MetadataAndACS(t *testing.T) {
	testCert := generateTestCertificate(t)
	cfg := &SAMLConfig{
		OrganizationID: "org-1",
		EntityID:       "https://idp.example.com/metadata",
		SSOURL:         "https://idp.example.com/sso",
		Certificate:    testCert.pem,
		SPEntityID:     "https://sp.example.com/metadata",
		ACSURL:         "https://sp.example.com/auth/saml/acme/acs",
	}
	cfgStore := NewMemorySAMLConfigStore()
	if err := cfgStore.UpsertSAMLConfig(t.Context(), cfg); err != nil {
		t.Fatalf("UpsertSAMLConfig: %v", err)
	}

	store := samlRouteStore{org: &Organization{ID: "org-1", Slug: "acme"}}
	provider := NewSAMLProvider(cfgStore, samlRouteProvisioner{}, store, time.Hour)
	mux := http.NewServeMux()
	RegisterOrgSAMLRoutes(mux, store, provider, "urgentry_session", "urgentry_csrf")

	metadataReq := httptest.NewRequest(http.MethodGet, "/auth/saml/acme/metadata", nil)
	metadataRec := httptest.NewRecorder()
	mux.ServeHTTP(metadataRec, metadataReq)
	if metadataRec.Code != http.StatusOK {
		t.Fatalf("metadata status = %d, want 200", metadataRec.Code)
	}
	metadataBody := metadataRec.Body.String()
	if !strings.Contains(metadataBody, "https://sp.example.com/metadata") || !strings.Contains(metadataBody, "https://sp.example.com/auth/saml/acme/acs") {
		t.Fatalf("unexpected metadata body: %s", metadataBody)
	}

	form := url.Values{
		"SAMLResponse": {encodeSignedTestSAMLResponse(t, testCert, cfg, "assertion-route-1", time.Now().Add(-time.Minute), time.Now().Add(5*time.Minute), "saml-user@example.com", "SAML User")},
		"RelayState":   {"/issues/"},
	}
	acsReq := httptest.NewRequest(http.MethodPost, "/auth/saml/acme/acs", strings.NewReader(form.Encode()))
	acsReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	acsRec := httptest.NewRecorder()
	mux.ServeHTTP(acsRec, acsReq)
	if acsRec.Code != http.StatusSeeOther {
		body, _ := io.ReadAll(acsRec.Body)
		t.Fatalf("acs status = %d, want 303, body=%s", acsRec.Code, body)
	}
	if location := acsRec.Header().Get("Location"); location != "/issues/" {
		t.Fatalf("redirect location = %q, want /issues/", location)
	}
	cookies := acsRec.Result().Cookies()
	foundSession := false
	foundCSRF := false
	for _, cookie := range cookies {
		switch cookie.Name {
		case "urgentry_session":
			foundSession = cookie.Value == "raw-saml-session"
		case "urgentry_csrf":
			foundCSRF = cookie.Value == "csrf-saml-token"
		}
	}
	if !foundSession || !foundCSRF {
		t.Fatalf("missing SAML cookies: %+v", cookies)
	}
}

func TestRegisterOrgSAMLRoutes_RejectsInvalidSAMLResponses(t *testing.T) {
	testCert := generateTestCertificate(t)
	cfg := &SAMLConfig{
		OrganizationID: "org-1",
		EntityID:       "https://idp.example.com/metadata",
		SSOURL:         "https://idp.example.com/sso",
		Certificate:    testCert.pem,
		SPEntityID:     "https://sp.example.com/metadata",
		ACSURL:         "https://sp.example.com/auth/saml/acme/acs",
	}
	cfgStore := NewMemorySAMLConfigStore()
	if err := cfgStore.UpsertSAMLConfig(t.Context(), cfg); err != nil {
		t.Fatalf("UpsertSAMLConfig: %v", err)
	}

	store := samlRouteStore{org: &Organization{ID: "org-1", Slug: "acme"}}
	provider := NewSAMLProvider(cfgStore, samlRouteProvisioner{}, store, time.Hour)
	mux := http.NewServeMux()
	RegisterOrgSAMLRoutes(mux, store, provider, "urgentry_session", "urgentry_csrf")

	valid := encodeSignedTestSAMLResponse(t, testCert, cfg, "assertion-valid-1", time.Now().Add(-time.Minute), time.Now().Add(5*time.Minute), "saml-user@example.com", "SAML User")

	tests := []struct {
		name     string
		response string
	}{
		{
			name:     "unsigned",
			response: encodeUnsignedTestSAMLResponse(t, cfg, time.Now().Add(-time.Minute), time.Now().Add(5*time.Minute), "saml-user@example.com", "SAML User"),
		},
		{
			name:     "tampered",
			response: tamperSAMLResponse(t, valid),
		},
		{
			name:     "expired",
			response: encodeSignedTestSAMLResponse(t, testCert, cfg, "assertion-expired-1", time.Now().Add(-20*time.Minute), time.Now().Add(-10*time.Minute), "saml-user@example.com", "SAML User"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postSAMLResponse(t, mux, tt.response)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); !strings.Contains(got, "Invalid SAML response.") {
				t.Fatalf("body = %q, want generic SAML error", got)
			}
			for _, cookie := range rec.Result().Cookies() {
				if cookie.Name == "urgentry_session" || cookie.Name == "urgentry_csrf" {
					t.Fatalf("unexpected auth cookie on rejected response: %+v", cookie)
				}
			}
		})
	}
}

func TestRegisterOrgSAMLRoutes_RejectsReplay(t *testing.T) {
	testCert := generateTestCertificate(t)
	cfg := &SAMLConfig{
		OrganizationID: "org-1",
		EntityID:       "https://idp.example.com/metadata",
		SSOURL:         "https://idp.example.com/sso",
		Certificate:    testCert.pem,
		SPEntityID:     "https://sp.example.com/metadata",
		ACSURL:         "https://sp.example.com/auth/saml/acme/acs",
	}
	cfgStore := NewMemorySAMLConfigStore()
	if err := cfgStore.UpsertSAMLConfig(t.Context(), cfg); err != nil {
		t.Fatalf("UpsertSAMLConfig: %v", err)
	}

	store := samlRouteStore{org: &Organization{ID: "org-1", Slug: "acme"}}
	provider := NewSAMLProvider(cfgStore, samlRouteProvisioner{}, store, time.Hour)
	mux := http.NewServeMux()
	RegisterOrgSAMLRoutes(mux, store, provider, "urgentry_session", "urgentry_csrf")

	response := encodeSignedTestSAMLResponse(t, testCert, cfg, "assertion-replay-1", time.Now().Add(-time.Minute), time.Now().Add(5*time.Minute), "saml-user@example.com", "SAML User")
	if rec := postSAMLResponse(t, mux, response); rec.Code != http.StatusSeeOther {
		t.Fatalf("first status = %d, want 303, body=%s", rec.Code, rec.Body.String())
	}
	if rec := postSAMLResponse(t, mux, response); rec.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func postSAMLResponse(t *testing.T, mux *http.ServeMux, response string) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{"SAMLResponse": {response}, "RelayState": {"/issues/"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/saml/acme/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type samlTestCertificate struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
	pem  string
}

func generateTestCertificate(t *testing.T) samlTestCertificate {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "saml-test-idp",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return samlTestCertificate{
		key:  privateKey,
		cert: cert,
		pem:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}

func encodeSignedTestSAMLResponse(t *testing.T, testCert samlTestCertificate, cfg *SAMLConfig, assertionID string, notBefore, notOnOrAfter time.Time, email, displayName string) string {
	t.Helper()

	now := time.Now().UTC()
	req := &crewjamsaml.IdpAuthnRequest{
		IDP: &crewjamsaml.IdentityProvider{
			Key:         testCert.key,
			Certificate: testCert.cert,
			MetadataURL: mustParseTestURL(t, cfg.EntityID),
			SSOURL:      mustParseTestURL(t, cfg.SSOURL),
		},
		HTTPRequest:             httptest.NewRequest(http.MethodPost, cfg.SSOURL, nil),
		Request:                 crewjamsaml.AuthnRequest{},
		ServiceProviderMetadata: &crewjamsaml.EntityDescriptor{EntityID: cfg.SPEntityID},
		SPSSODescriptor:         &crewjamsaml.SPSSODescriptor{},
		ACSEndpoint: &crewjamsaml.IndexedEndpoint{
			Binding:  crewjamsaml.HTTPPostBinding,
			Location: cfg.ACSURL,
			Index:    0,
		},
		Assertion: &crewjamsaml.Assertion{
			ID:           assertionID,
			IssueInstant: now,
			Version:      "2.0",
			Issuer: crewjamsaml.Issuer{
				Format: "urn:oasis:names:tc:SAML:2.0:nameid-format:entity",
				Value:  cfg.EntityID,
			},
			Subject: &crewjamsaml.Subject{
				NameID: &crewjamsaml.NameID{
					Format: string(crewjamsaml.EmailAddressNameIDFormat),
					Value:  email,
				},
				SubjectConfirmations: []crewjamsaml.SubjectConfirmation{{
					Method: "urn:oasis:names:tc:SAML:2.0:cm:bearer",
					SubjectConfirmationData: &crewjamsaml.SubjectConfirmationData{
						NotOnOrAfter: notOnOrAfter.UTC(),
						Recipient:    cfg.ACSURL,
					},
				}},
			},
			Conditions: &crewjamsaml.Conditions{
				NotBefore:    notBefore.UTC(),
				NotOnOrAfter: notOnOrAfter.UTC(),
				AudienceRestrictions: []crewjamsaml.AudienceRestriction{{
					Audience: crewjamsaml.Audience{Value: cfg.SPEntityID},
				}},
			},
			AttributeStatements: []crewjamsaml.AttributeStatement{{
				Attributes: []crewjamsaml.Attribute{
					{
						Name:   "email",
						Values: []crewjamsaml.AttributeValue{{Type: "xs:string", Value: email}},
					},
					{
						Name:   "displayName",
						Values: []crewjamsaml.AttributeValue{{Type: "xs:string", Value: displayName}},
					},
				},
			}},
		},
		Now: now,
	}
	form, err := req.PostBinding()
	if err != nil {
		t.Fatalf("PostBinding: %v", err)
	}
	return form.SAMLResponse
}

func encodeUnsignedTestSAMLResponse(t *testing.T, cfg *SAMLConfig, notBefore, notOnOrAfter time.Time, email, displayName string) string {
	t.Helper()

	xmlPayload := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" Destination="` + cfg.ACSURL + `" ID="response-unsigned-1" IssueInstant="` + time.Now().UTC().Format(time.RFC3339) + `" Version="2.0"><saml:Issuer>` + cfg.EntityID + `</saml:Issuer><samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"></samlp:StatusCode></samlp:Status><saml:Assertion ID="assertion-unsigned-1" IssueInstant="` + time.Now().UTC().Format(time.RFC3339) + `" Version="2.0"><saml:Issuer>` + cfg.EntityID + `</saml:Issuer><saml:Subject><saml:NameID>` + email + `</saml:NameID><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData NotOnOrAfter="` + notOnOrAfter.UTC().Format(time.RFC3339) + `" Recipient="` + cfg.ACSURL + `"></saml:SubjectConfirmationData></saml:SubjectConfirmation></saml:Subject><saml:Conditions NotBefore="` + notBefore.UTC().Format(time.RFC3339) + `" NotOnOrAfter="` + notOnOrAfter.UTC().Format(time.RFC3339) + `"><saml:AudienceRestriction><saml:Audience>` + cfg.SPEntityID + `</saml:Audience></saml:AudienceRestriction></saml:Conditions><saml:AttributeStatement><saml:Attribute Name="email"><saml:AttributeValue>` + email + `</saml:AttributeValue></saml:Attribute><saml:Attribute Name="displayName"><saml:AttributeValue>` + displayName + `</saml:AttributeValue></saml:Attribute></saml:AttributeStatement></saml:Assertion></samlp:Response>`
	return base64.StdEncoding.EncodeToString([]byte(xmlPayload))
}

func tamperSAMLResponse(t *testing.T, encoded string) string {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	tampered := bytes.Replace(raw, []byte("saml-user@example.com"), []byte("evil-user@example.com"), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("test fixture did not contain value to tamper")
	}
	return base64.StdEncoding.EncodeToString(tampered)
}

func mustParseTestURL(t *testing.T, raw string) url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse URL %q: %v", raw, err)
	}
	return *u
}
