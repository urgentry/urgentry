//go:build integration

package compat

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// csrfFromCookies extracts the urgentry_csrf cookie value from the jar for
// the given server URL.
func csrfFromCookies(t *testing.T, jar http.CookieJar, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "urgentry_csrf" {
			return c.Value
		}
	}
	return ""
}

// e2eLoginClient creates a cookie-jar-backed HTTP client, performs
// the full login flow (GET /login/ then POST /login/), and returns
// the client plus the CSRF token. It stops following redirects so
// callers can inspect status codes.
func e2eLoginClient(t *testing.T, srv *compatServer) (*http.Client, string) {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}

	form := url.Values{
		"email":    {srv.email},
		"password": {srv.password},
		"next":     {"/issues/"},
	}
	resp, err := client.PostForm(srv.server.URL+"/login/", form)
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login/ status = %d, want 303", resp.StatusCode)
	}

	csrf := csrfFromCookies(t, jar, srv.server.URL)
	if csrf == "" {
		t.Fatal("missing urgentry_csrf cookie after login")
	}
	return client, csrf
}

// TestE2ELoginBootstrap boots a full Urgentry test instance, GETs the
// login page, extracts the CSRF token (from the form), POSTs valid
// credentials, and verifies the redirect to /issues/.
func TestE2ELoginBootstrap(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}

	// Step 1: GET /login/ and verify the page renders.
	getResp, err := client.Get(srv.server.URL + "/login/")
	if err != nil {
		t.Fatalf("GET /login/: %v", err)
	}
	body, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login/ status = %d, want 200", getResp.StatusCode)
	}
	if !strings.Contains(string(body), "Sign in") {
		t.Fatal("login page does not contain 'Sign in'")
	}

	// Step 2: POST /login/ with valid credentials.
	form := url.Values{
		"email":    {srv.email},
		"password": {srv.password},
		"next":     {"/issues/"},
	}
	postResp, err := client.PostForm(srv.server.URL+"/login/", form)
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login/ status = %d, want 303", postResp.StatusCode)
	}

	loc := postResp.Header.Get("Location")
	if loc != "/issues/" {
		t.Fatalf("POST /login/ Location = %q, want /issues/", loc)
	}

	// Verify session and CSRF cookies are set.
	u, _ := url.Parse(srv.server.URL)
	var hasSession, hasCSRF bool
	for _, c := range jar.Cookies(u) {
		switch c.Name {
		case "urgentry_session":
			hasSession = true
		case "urgentry_csrf":
			hasCSRF = true
		}
	}
	if !hasSession {
		t.Fatal("urgentry_session cookie not set after login")
	}
	if !hasCSRF {
		t.Fatal("urgentry_csrf cookie not set after login")
	}
}

// TestE2ELoginInvalidPassword posts an invalid password and verifies
// the login page re-renders with an error message.
func TestE2ELoginInvalidPassword(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}

	form := url.Values{
		"email":    {srv.email},
		"password": {"wrong-password"},
		"next":     {"/issues/"},
	}
	resp, err := client.PostForm(srv.server.URL+"/login/", form)
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// The login handler re-renders the login template (200) with an error
	// message rather than redirecting.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /login/ with bad password status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Invalid email or password") {
		t.Fatal("expected error message in login page body")
	}
}

// TestE2ELoginCSRFProtection verifies that the /logout endpoint rejects
// a POST without a valid CSRF token. The login POST itself does NOT require
// CSRF (it is the session-establishing action), but logout does.
func TestE2ELoginCSRFProtection(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	client, _ := e2eLoginClient(t, srv)

	// POST /logout without CSRF header should be rejected.
	req, err := http.NewRequest(http.MethodPost, srv.server.URL+"/logout", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /logout without CSRF status = %d, want 403", resp.StatusCode)
	}
}

// TestE2ESessionCookie verifies that after a successful login the session
// cookie can be used to access authenticated pages without re-authenticating.
func TestE2ESessionCookie(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	client, _ := e2eLoginClient(t, srv)

	// Access /issues/ using the session cookie. The server should NOT
	// redirect to /login/ (status 200, not 303).
	resp, err := client.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /issues/ with session cookie status = %d, want 200", resp.StatusCode)
	}
}

// TestE2ELogout logs in, then POSTs /logout with the CSRF token, and
// verifies the session is invalidated (subsequent requests redirect to login).
func TestE2ELogout(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	client, csrf := e2eLoginClient(t, srv)

	// POST /logout with valid CSRF.
	req, err := http.NewRequest(http.MethodPost, srv.server.URL+"/logout", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-CSRF-Token", csrf)
	logoutResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	logoutResp.Body.Close()

	if logoutResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", logoutResp.StatusCode)
	}
	loc := logoutResp.Header.Get("Location")
	if loc != "/login/" {
		t.Fatalf("POST /logout Location = %q, want /login/", loc)
	}

	// Now verify the session is invalidated: GET /issues/ should redirect
	// back to /login/.
	resp, err := client.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/ after logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /issues/ after logout status = %d, want 303 redirect to login", resp.StatusCode)
	}
}

// TestE2EBootstrapPAT verifies that the bootstrap personal access token
// can authenticate API requests via the Authorization: Bearer header.
func TestE2EBootstrapPAT(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	req, err := http.NewRequest(http.MethodGet, srv.server.URL+"/api/0/projects/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+srv.pat)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/0/projects/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/0/projects/ with PAT status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	// The response should be valid JSON containing project data.
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		t.Fatalf("expected JSON array, got: %s", body)
	}
}

// TestE2EAPIAuthRequired verifies that unauthenticated API requests are
// rejected with 401.
func TestE2EAPIAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	resp, err := http.Get(srv.server.URL + "/api/0/projects/")
	if err != nil {
		t.Fatalf("GET /api/0/projects/: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/0/projects/ without auth status = %d, want 401", resp.StatusCode)
	}
}

// TestE2EAPIWithPAT hits /api/0/projects/ with the bootstrap PAT and
// verifies a 200 response containing project data (the default project).
func TestE2EAPIWithPAT(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	resp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/", srv.pat, nil, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/0/projects/ with PAT status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	// Verify there is at least one project (the bootstrap "default" project).
	bodyStr := strings.TrimSpace(string(body))
	if bodyStr == "[]" || bodyStr == "" {
		t.Fatal("expected at least one project in the response")
	}

	// Verify it looks like a JSON array with project slugs.
	re := regexp.MustCompile(`"slug"\s*:\s*"default"`)
	if !re.Match(body) {
		t.Fatalf("expected project slug 'default' in response: %s", bodyStr)
	}
}
