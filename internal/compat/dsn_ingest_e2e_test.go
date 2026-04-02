//go:build integration

package compat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/pkg/dsn"
)

// dsnKeyResponse mirrors api.ProjectKey for JSON decoding.
type dsnKeyResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Label     string `json:"label"`
	Public    string `json:"public"`
	Secret    string `json:"secret"`
	IsActive  bool   `json:"isActive"`
	DSN       struct {
		Public string `json:"public"`
		Secret string `json:"secret"`
	} `json:"dsn"`
}

// fetchDSNKeys calls GET /api/0/projects/{org}/{proj}/keys/ with PAT auth and
// returns the decoded key list.
func fetchDSNKeys(t *testing.T, srv *compatServer) []dsnKeyResponse {
	t.Helper()

	resp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/projects/urgentry-org/default/keys/",
		srv.pat, nil, "")
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET keys status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var keys []dsnKeyResponse
	if err := json.Unmarshal(body, &keys); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one project key")
	}
	return keys
}

// waitForGroupCount polls the groups table until at least `want` groups exist
// for the given project.
func waitForGroupCount(t *testing.T, db *sql.DB, projectID string, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM groups WHERE project_id = ?`, projectID).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("project %s did not reach %d groups", projectID, want)
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestE2EDSNRetrieval authenticates with the bootstrap PAT, GETs the project
// keys API, and verifies the DSN is returned in the correct format.
func TestE2EDSNRetrieval(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	keys := fetchDSNKeys(t, srv)

	key := keys[0]
	if !key.IsActive {
		t.Fatal("expected key to be active")
	}
	if key.Public == "" {
		t.Fatal("public key is empty")
	}
	if key.DSN.Public == "" {
		t.Fatal("public DSN is empty")
	}

	// Parse the returned DSN to verify its structure.
	parsed, err := dsn.Parse(key.DSN.Public)
	if err != nil {
		t.Fatalf("dsn.Parse(%q): %v", key.DSN.Public, err)
	}
	if parsed.PublicKey != key.Public {
		t.Fatalf("DSN public key = %q, want %q", parsed.PublicKey, key.Public)
	}
	if parsed.ProjectID == "" {
		t.Fatal("DSN project ID is empty")
	}
	if parsed.Scheme != "http" {
		t.Fatalf("DSN scheme = %q, want http (test server)", parsed.Scheme)
	}
}

// TestE2EDSNIngestFlow retrieves a DSN via the keys API, parses it, and uses
// the public key to POST an event to /api/{project_id}/store/. It then verifies
// the event was stored.
func TestE2EDSNIngestFlow(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	keys := fetchDSNKeys(t, srv)
	parsed, err := dsn.Parse(keys[0].DSN.Public)
	if err != nil {
		t.Fatalf("dsn.Parse: %v", err)
	}

	eventID := "e2e0000000000000000000000000aa01"
	payload := fmt.Sprintf(`{
		"event_id": "%s",
		"message": "dsn ingest flow test",
		"level": "error",
		"platform": "go"
	}`, eventID)

	storeURL := fmt.Sprintf("%s/api/%s/store/", srv.server.URL, parsed.ProjectID)
	authHeader := fmt.Sprintf("Sentry sentry_key=%s,sentry_version=7,sentry_client=e2e-test/1.0", parsed.PublicKey)

	resp := doRequest(t, http.MethodPost, storeURL, strings.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": authHeader,
	})
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST store status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode store response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("store response missing id")
	}

	waitForEvent(t, srv.db, eventID)
}

// TestE2EDSNEnvelopeFlow retrieves a DSN, uses it to POST a Sentry envelope to
// /api/{project_id}/envelope/, and verifies the event is stored.
func TestE2EDSNEnvelopeFlow(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	keys := fetchDSNKeys(t, srv)
	parsed, err := dsn.Parse(keys[0].DSN.Public)
	if err != nil {
		t.Fatalf("dsn.Parse: %v", err)
	}

	eventID := "e2e0000000000000000000000000bb01"

	// Build a Sentry envelope:
	//   line 1: envelope header JSON
	//   line 2: item header JSON
	//   line 3: item payload JSON
	eventPayload := fmt.Sprintf(`{"event_id":"%s","message":"envelope flow test","level":"error","platform":"go"}`, eventID)
	itemHeader := fmt.Sprintf(`{"type":"event","length":%d}`, len(eventPayload))
	envelopeBody := fmt.Sprintf(`{"event_id":"%s","dsn":"%s","sent_at":"%s"}`,
		eventID, keys[0].DSN.Public, time.Now().UTC().Format(time.RFC3339))
	envelope := envelopeBody + "\n" + itemHeader + "\n" + eventPayload

	envelopeURL := fmt.Sprintf("%s/api/%s/envelope/", srv.server.URL, parsed.ProjectID)
	authHeader := fmt.Sprintf("Sentry sentry_key=%s,sentry_version=7,sentry_client=e2e-test/1.0", parsed.PublicKey)

	resp := doRequest(t, http.MethodPost, envelopeURL, strings.NewReader(envelope), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": authHeader,
	})
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST envelope status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	waitForEvent(t, srv.db, eventID)
}

// TestE2EMultipleEventsFlow sends 5 distinct events and verifies all 5 are
// persisted and retrievable via the API.
func TestE2EMultipleEventsFlow(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	const count = 5
	eventIDs := make([]string, count)
	for i := 0; i < count; i++ {
		eventIDs[i] = fmt.Sprintf("e2e00000000000000000000000cc%04d", i+1)
	}

	for _, eid := range eventIDs {
		payload := fmt.Sprintf(`{"event_id":"%s","message":"multi event %s","level":"info","platform":"go"}`, eid, eid)
		resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(payload), map[string]string{
			"Content-Type":  "application/json",
			"X-Sentry-Auth": srv.sentryAuthHeader(),
		})
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST store event %s status=%d, want 200; body=%s", eid, resp.StatusCode, body)
		}
	}

	// Wait for all events to be persisted.
	waitForProjectEventCount(t, srv.db, "default-project", count)

	// Verify via API that all events are listed.
	resp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/projects/urgentry-org/default/events/",
		srv.pat, nil, "")
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET events status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var events []map[string]interface{}
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) < count {
		t.Fatalf("got %d events, want >= %d", len(events), count)
	}
}

// TestE2EEventRetrievalAPI sends an event, then GETs it by ID via the project
// events API and verifies the response fields match.
func TestE2EEventRetrievalAPI(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e2e0000000000000000000000000dd01"
	payload := fmt.Sprintf(`{
		"event_id": "%s",
		"message": "event retrieval test",
		"level": "warning",
		"platform": "python",
		"request": {"method": "GET", "url": "https://app.example.com/checkout"},
		"contexts": {"trace": {"trace_id": "trace-e2e-1", "span_id": "span-e2e-1", "type": "trace"}},
		"sdk": {"name": "sentry.python", "version": "2.0.0"},
		"user": {"id": "user-e2e", "email": "compat@example.com"},
		"fingerprint": ["{{ default }}", "checkout"],
		"modules": {"pkg/errors": "v0.9.1"},
		"measurements": {"lcp": {"value": 1234.5, "unit": "millisecond"}},
		"breadcrumbs": {"values": [{"type": "default", "category": "auth", "message": "signed in", "level": "info"}]},
		"exception": {"values": [{"type": "ValueError", "value": "bad input"}]}
	}`, eventID)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", strings.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST store status=%d, want 200", resp.StatusCode)
	}

	waitForEvent(t, srv.db, eventID)

	// GET the event by ID.
	getResp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/projects/urgentry-org/default/events/"+eventID+"/",
		srv.pat, nil, "")
	defer getResp.Body.Close()

	body, _ := io.ReadAll(getResp.Body)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET event status=%d, want 200; body=%s", getResp.StatusCode, body)
	}

	var evt map[string]interface{}
	if err := json.Unmarshal(body, &evt); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	// Verify key fields.
	if got, ok := evt["eventID"].(string); !ok || got != eventID {
		t.Fatalf("eventID = %v, want %s", evt["eventID"], eventID)
	}
	if got, ok := evt["message"].(string); !ok || got == "" {
		t.Fatalf("message is empty or missing: %v", evt["message"])
	}
	if got, ok := evt["level"].(string); !ok || got != "warning" {
		t.Fatalf("level = %v, want warning", evt["level"])
	}
	if got, ok := evt["platform"].(string); !ok || got != "python" {
		t.Fatalf("platform = %v, want python", evt["platform"])
	}
	if entries, ok := evt["entries"].([]interface{}); !ok || len(entries) < 4 {
		t.Fatalf("entries = %v, want synthesized interfaces", evt["entries"])
	}
	contexts, ok := evt["contexts"].(map[string]interface{})
	if !ok || contexts["trace"] == nil {
		t.Fatalf("contexts = %v, want trace context", evt["contexts"])
	}
	sdk, ok := evt["sdk"].(map[string]interface{})
	if !ok || sdk["name"] != "sentry.python" {
		t.Fatalf("sdk = %v, want sentry.python", evt["sdk"])
	}
	user, ok := evt["user"].(map[string]interface{})
	if !ok || user["email"] != "compat@example.com" {
		t.Fatalf("user = %v, want compat@example.com", evt["user"])
	}
	if got, ok := evt["fingerprints"].([]interface{}); !ok || len(got) != 2 {
		t.Fatalf("fingerprints = %v, want 2 items", evt["fingerprints"])
	}
	packages, ok := evt["packages"].(map[string]interface{})
	if !ok || packages["pkg/errors"] != "v0.9.1" {
		t.Fatalf("packages = %v, want pkg/errors", evt["packages"])
	}
	measurements, ok := evt["measurements"].(map[string]interface{})
	if !ok || measurements["lcp"] == nil {
		t.Fatalf("measurements = %v, want lcp", evt["measurements"])
	}
}

// TestE2EIssueCreation sends an error event, then verifies that an issue
// (group) is created and visible via the project issues API.
func TestE2EIssueCreation(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "e2e0000000000000000000000000ee01"
	payload := fmt.Sprintf(`{
		"event_id": "%s",
		"message": "issue creation test error",
		"level": "error",
		"platform": "go",
		"exception": {
			"values": [{
				"type": "RuntimeError",
				"value": "something went wrong in e2e test",
				"stacktrace": {
					"frames": [{
						"filename": "main.go",
						"function": "main",
						"lineno": 42
					}]
				}
			}]
		}
	}`, eventID)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/",
		bytes.NewReader([]byte(payload)), map[string]string{
			"Content-Type":  "application/json",
			"X-Sentry-Auth": srv.sentryAuthHeader(),
		})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST store status=%d, want 200", resp.StatusCode)
	}

	// Wait for the event to be persisted and grouped.
	waitForEvent(t, srv.db, eventID)
	waitForGroupCount(t, srv.db, "default-project", 1)

	// Verify via the issues API.
	issuesResp := apiRequest(t, http.MethodGet,
		srv.server.URL+"/api/0/projects/urgentry-org/default/issues/",
		srv.pat, nil, "")
	defer issuesResp.Body.Close()

	body, _ := io.ReadAll(issuesResp.Body)
	if issuesResp.StatusCode != http.StatusOK {
		t.Fatalf("GET issues status=%d, want 200; body=%s", issuesResp.StatusCode, body)
	}

	var issues []map[string]interface{}
	if err := json.Unmarshal(body, &issues); err != nil {
		t.Fatalf("decode issues: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue after sending an error event")
	}

	// Verify the issue has meaningful fields.
	issue := issues[0]
	if id, ok := issue["id"].(string); !ok || id == "" {
		t.Fatal("issue id is empty")
	}
	if title, ok := issue["title"].(string); !ok || title == "" {
		t.Fatal("issue title is empty")
	}
	if status, ok := issue["status"].(string); !ok || status == "" {
		t.Fatal("issue status is empty")
	}
}
