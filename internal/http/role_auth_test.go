package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"urgentry/internal/api"
	"urgentry/internal/config"
	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type roleTestServer struct {
	server     *httptest.Server
	db         *sql.DB
	pat        string
	projectKey string
	email      string
	pass       string
}

func newRoleTestServer(t *testing.T, role string) roleTestServer {
	t.Helper()

	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	publicKey, err := sqlite.EnsureDefaultKey(context.Background(), db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}

	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 "admin@example.com",
		DisplayName:           "Admin",
		Password:              "test-password",
		PersonalAccessToken:   "gpat_test_bootstrap_token",
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}

	blobStore := store.NewMemoryBlobStore()
	jobStore := sqlite.NewJobStore(db)
	lifecycle := sqlite.NewLifecycleStore(db)
	proc := &issue.Processor{
		Events:   sqlite.NewEventStore(db),
		Groups:   sqlite.NewGroupStore(db),
		Blobs:    blobStore,
		Releases: sqlite.NewReleaseStore(db),
	}
	pipe := pipeline.NewDurable(proc, jobStore, 1, 1)
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobStore, jobStore, 1)
	pipe.SetNativeJobProcessor(pipeline.NativeJobProcessorFunc(func(ctx context.Context, projectID string, payload []byte) error {
		return nativeCrashes.ProcessStackwalkJob(ctx, proc, projectID, payload)
	}))

	cfg := config.Config{
		Env:               "test",
		HTTPAddr:          ":0",
		SessionCookieName: "urgentry_session",
		CSRFCookieName:    "urgentry_csrf",
	}
	deps := sqliteServerDeps(t, db, dataDir, keyStore, authStore, pipe, blobStore, nativeCrashes)
	deps.API.SourceMapStore = sourcemap.NewMemoryStore()
	deps.Lifecycle = lifecycle
	handler := NewServer(role, cfg, deps)

	return roleTestServer{
		server:     httptest.NewServer(handler),
		db:         db,
		pat:        bootstrap.PAT,
		projectKey: publicKey,
		email:      bootstrap.Email,
		pass:       bootstrap.Password,
	}
}

func loginSessionClient(t *testing.T, srv roleTestServer) (*http.Client, string) {
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
		"password": {srv.pass},
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

	serverURL, err := url.Parse(srv.server.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	for _, cookie := range jar.Cookies(serverURL) {
		if cookie.Name == "urgentry_csrf" {
			return client, cookie.Value
		}
	}
	t.Fatal("missing urgentry_csrf cookie")
	return nil, ""
}

func doJSONRequest(t *testing.T, client *http.Client, method, url, csrf string, body any, target any) int {
	t.Helper()

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}

	req, err := http.NewRequest(method, url, &payload)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp.StatusCode
}

func uploadSourceMap(t *testing.T, token, baseURL string) int {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "app.js.map")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(`{"version":3,"file":"app.js","sources":["app.ts"],"names":[],"mappings":"AAAA"}`)); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.WriteField("name", "~/static/app.js.map"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/0/projects/urgentry-org/default/releases/1.2.3/files/", &body)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("source map upload: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRoleMountingIngest(t *testing.T) {
	srv := newRoleTestServer(t, "ingest")
	defer srv.server.Close()

	resp, err := http.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /issues/ status = %d, want 404", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST store: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /api/default-project/store/ status = %d, want 401", resp.StatusCode)
	}
}

func TestRoleMountingAPI(t *testing.T) {
	srv := newRoleTestServer(t, "api")
	defer srv.server.Close()

	req, err := http.NewRequest(http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST store: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /api/default-project/store/ status = %d, want 404", resp.StatusCode)
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err = client.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /issues/ status = %d, want 303", resp.StatusCode)
	}
}

func TestRoleMountingWorker(t *testing.T) {
	srv := newRoleTestServer(t, "worker")
	defer srv.server.Close()

	resp, err := http.Get(srv.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /issues/ status = %d, want 404", resp.StatusCode)
	}
}

func TestManagementAPIRequiresPATNotProjectKey(t *testing.T) {
	srv := newRoleTestServer(t, "api")
	defer srv.server.Close()

	projectKeyReq, err := http.NewRequest(http.MethodGet, srv.server.URL+"/api/0/organizations/", nil)
	if err != nil {
		t.Fatalf("new project-key request: %v", err)
	}
	projectKeyReq.Header.Set("Authorization", "Bearer "+srv.projectKey)
	projectKeyResp, err := http.DefaultClient.Do(projectKeyReq)
	if err != nil {
		t.Fatalf("GET orgs with project key: %v", err)
	}
	projectKeyResp.Body.Close()
	if projectKeyResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("project key auth status = %d, want 401", projectKeyResp.StatusCode)
	}

	patReq, err := http.NewRequest(http.MethodGet, srv.server.URL+"/api/0/organizations/", nil)
	if err != nil {
		t.Fatalf("new pat request: %v", err)
	}
	patReq.Header.Set("Authorization", "Bearer "+srv.pat)
	patResp, err := http.DefaultClient.Do(patReq)
	if err != nil {
		t.Fatalf("GET orgs with pat: %v", err)
	}
	patResp.Body.Close()
	if patResp.StatusCode != http.StatusOK {
		t.Fatalf("pat auth status = %d, want 200", patResp.StatusCode)
	}
}

func TestWebLoginCreatesSession(t *testing.T) {
	srv := newRoleTestServer(t, "api")
	defer srv.server.Close()

	client, _ := loginSessionClient(t, srv)

	resp, err := client.Get(srv.server.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/ after login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /issues/ after login status = %d, want 200", resp.StatusCode)
	}
}

func TestSessionManagesPATsAndAutomationTokens(t *testing.T) {
	srv := newRoleTestServer(t, "api")
	defer srv.server.Close()

	client, csrf := loginSessionClient(t, srv)

	var pat api.CreatedPersonalAccessToken
	status := doJSONRequest(t, client, http.MethodPost, srv.server.URL+"/api/0/users/me/personal-access-tokens/", csrf, map[string]any{
		"label":  "CLI Access",
		"scopes": []string{"org:read", "project:read"},
	}, &pat)
	if status != http.StatusCreated {
		t.Fatalf("create PAT status = %d, want 201", status)
	}
	if pat.Token == "" {
		t.Fatal("expected raw PAT token in create response")
	}

	var pats []api.PersonalAccessToken
	status = doJSONRequest(t, client, http.MethodGet, srv.server.URL+"/api/0/users/me/personal-access-tokens/", "", nil, &pats)
	if status != http.StatusOK {
		t.Fatalf("list PATs status = %d, want 200", status)
	}
	foundPAT := false
	for _, token := range pats {
		if token.ID == pat.ID && token.Label == "CLI Access" {
			foundPAT = true
			break
		}
	}
	if !foundPAT {
		t.Fatalf("expected created PAT in list, got %+v", pats)
	}

	req, err := http.NewRequest(http.MethodGet, srv.server.URL+"/api/0/organizations/", nil)
	if err != nil {
		t.Fatalf("new pat auth request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pat.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET orgs with created PAT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("created PAT auth status = %d, want 200", resp.StatusCode)
	}

	status = doJSONRequest(t, client, http.MethodDelete, srv.server.URL+"/api/0/users/me/personal-access-tokens/"+pat.ID+"/", csrf, nil, nil)
	if status != http.StatusNoContent {
		t.Fatalf("revoke PAT status = %d, want 204", status)
	}

	req, err = http.NewRequest(http.MethodGet, srv.server.URL+"/api/0/organizations/", nil)
	if err != nil {
		t.Fatalf("new revoked PAT request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pat.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET orgs with revoked PAT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked PAT auth status = %d, want 401", resp.StatusCode)
	}

	var automation api.CreatedAutomationToken
	status = doJSONRequest(t, client, http.MethodPost, srv.server.URL+"/api/0/projects/urgentry-org/default/automation-tokens/", csrf, map[string]any{
		"label":  "CI Upload",
		"scopes": []string{"project:artifacts:write"},
	}, &automation)
	if status != http.StatusCreated {
		t.Fatalf("create automation token status = %d, want 201", status)
	}
	if automation.Token == "" {
		t.Fatal("expected raw automation token in create response")
	}

	status = uploadSourceMap(t, automation.Token, srv.server.URL)
	if status != http.StatusCreated {
		t.Fatalf("automation token upload status = %d, want 201", status)
	}

	status = doJSONRequest(t, client, http.MethodDelete, srv.server.URL+"/api/0/projects/urgentry-org/default/automation-tokens/"+automation.ID+"/", csrf, nil, nil)
	if status != http.StatusNoContent {
		t.Fatalf("revoke automation token status = %d, want 204", status)
	}

	status = uploadSourceMap(t, automation.Token, srv.server.URL)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked automation token upload status = %d, want 401", status)
	}
}
