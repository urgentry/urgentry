package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/integration"
	"urgentry/internal/sqlite"
)

func TestIntegrationWebhook_GitHubConfigIDRouteVerifiesSignature(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		IntegrationRegistry: integration.NewDefaultRegistry(),
		IntegrationStore:    sqlite.NewIntegrationConfigStore(db),
	})
	defer ts.Close()

	install := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/integrations/github/install", pat, map[string]any{
		"config": map[string]any{
			"github_token":          "ghp_test",
			"github_owner":          "acme",
			"github_repo":           "api",
			"github_webhook_secret": "secret",
		},
	})
	if install.StatusCode != http.StatusCreated {
		t.Fatalf("install status = %d, want 201", install.StatusCode)
	}
	var cfg integration.IntegrationConfig
	decodeBody(t, install, &cfg)

	payload, err := json.Marshal(map[string]any{
		"ref": "refs/heads/main",
		"repository": map[string]any{
			"full_name": "acme/api",
		},
		"commits": []map[string]any{{"id": "abc123", "message": "ship it"}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	invalid := integrationWebhookRequest(t, ts, "/api/0/organizations/test-org/integrations/"+cfg.ID+"/webhook", http.Header{
		"X-Hub-Signature-256": []string{"sha256=deadbeef"},
	}, payload)
	if invalid.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d, want 401", invalid.StatusCode)
	}
	invalid.Body.Close()

	valid := integrationWebhookRequest(t, ts, "/api/0/organizations/test-org/integrations/"+cfg.ID+"/webhook", http.Header{
		"X-Hub-Signature-256": []string{githubWebhookSignature256("secret", payload)},
	}, payload)
	if valid.StatusCode != http.StatusOK {
		t.Fatalf("valid signature status = %d, want 200", valid.StatusCode)
	}
	var resp map[string]any
	decodeBody(t, valid, &resp)
	if resp["ok"] != true || resp["ref"] != "refs/heads/main" {
		t.Fatalf("unexpected webhook response: %+v", resp)
	}
}

func TestIntegrationWebhook_GitHubFailsClosedWithoutSecret(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		IntegrationRegistry: integration.NewDefaultRegistry(),
		IntegrationStore:    sqlite.NewIntegrationConfigStore(db),
	})
	defer ts.Close()

	install := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/integrations/github/install", pat, map[string]any{
		"config": map[string]any{
			"github_token": "ghp_test",
			"github_owner": "acme",
			"github_repo":  "api",
		},
	})
	if install.StatusCode != http.StatusCreated {
		t.Fatalf("install status = %d, want 201", install.StatusCode)
	}
	var cfg integration.IntegrationConfig
	decodeBody(t, install, &cfg)

	payload := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/api"}}`)
	resp := integrationWebhookRequest(t, ts, "/api/0/organizations/test-org/integrations/"+cfg.ID+"/webhook", nil, payload)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing secret status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestIntegrationWebhook_GitHubSlugRouteRejectsAmbiguousInstall(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		IntegrationRegistry: integration.NewDefaultRegistry(),
		IntegrationStore:    sqlite.NewIntegrationConfigStore(db),
	})
	defer ts.Close()

	for _, repo := range []string{"api", "worker"} {
		install := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/integrations/github/install", pat, map[string]any{
			"config": map[string]any{
				"github_token":          "ghp_test",
				"github_owner":          "acme",
				"github_repo":           repo,
				"github_webhook_secret": "secret",
			},
		})
		if install.StatusCode != http.StatusCreated {
			t.Fatalf("install %s status = %d, want 201", repo, install.StatusCode)
		}
		install.Body.Close()
	}

	payload := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/api"}}`)
	resp := integrationWebhookRequest(t, ts, "/api/0/organizations/test-org/integrations/github/webhook", nil, payload)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ambiguous slug status = %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

func integrationWebhookRequest(t *testing.T, ts *httptest.Server, path string, headers http.Header, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func githubWebhookSignature256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
