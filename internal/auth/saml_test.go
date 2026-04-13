package auth

import (
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
	certPEM := generateTestCertificatePEM(t)
	cfgStore := NewMemorySAMLConfigStore()
	if err := cfgStore.UpsertSAMLConfig(t.Context(), &SAMLConfig{
		OrganizationID: "org-1",
		EntityID:       "https://idp.example.com/metadata",
		SSOURL:         "https://idp.example.com/sso",
		Certificate:    certPEM,
		SPEntityID:     "https://sp.example.com/metadata",
		ACSURL:         "https://sp.example.com/auth/saml/acme/acs",
	}); err != nil {
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
		"SAMLResponse": {encodeTestSAMLResponse(t, time.Now().Add(-time.Minute), time.Now().Add(5*time.Minute), "saml-user@example.com", "SAML User")},
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

func generateTestCertificatePEM(t *testing.T) string {
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
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func encodeTestSAMLResponse(t *testing.T, notBefore, notOnOrAfter time.Time, email, displayName string) string {
	t.Helper()

	xmlPayload := `<Response><Assertion><Issuer>https://idp.example.com/metadata</Issuer><Subject><NameID>` + email + `</NameID></Subject><Conditions NotBefore="` + notBefore.UTC().Format(time.RFC3339) + `" NotOnOrAfter="` + notOnOrAfter.UTC().Format(time.RFC3339) + `"></Conditions><AttributeStatement><Attribute Name="email"><AttributeValue>` + email + `</AttributeValue></Attribute><Attribute Name="displayName"><AttributeValue>` + displayName + `</AttributeValue></Attribute></AttributeStatement></Assertion></Response>`
	return base64.StdEncoding.EncodeToString([]byte(xmlPayload))
}
