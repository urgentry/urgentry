package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"urgentry/internal/sqlite"
)

func TestAPICreateHook_RejectsInvalidEvents(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/hooks/", createHookRequest{
		URL:    "https://example.com/hook",
		Events: []string{"not-real"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPICreateHook_RejectsPrivateURL(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/hooks/", createHookRequest{
		URL:    "http://127.0.0.1/hook",
		Events: []string{"event.created"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIGetHook_RejectsCrossProjectHook(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('other-proj-id', 'test-org-id', 'other-project', 'Other Project', 'go', 'active')`); err != nil {
		t.Fatalf("insert other project: %v", err)
	}

	hooks := sqlite.NewHookStore(db)
	hook := &sqlite.ServiceHook{
		ProjectID: "other-proj-id",
		URL:       "https://example.com/hook",
		Events:    []string{"event.created"},
	}
	if err := hooks.Create(t.Context(), hook); err != nil {
		t.Fatalf("Create hook: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{Hooks: hooks})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/hooks/"+hook.ID+"/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIUpdateIssue_FiresResolvedHook(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteGroup(t, db, "grp-hook-issue", "Hooked Issue", "main.go", "error", "unresolved")

	client, payloads := newAPIHookCaptureClient(t)

	hooks := sqlite.NewHookStore(db)
	hooks.HTTPClient = client
	if err := hooks.Create(t.Context(), &sqlite.ServiceHook{
		ProjectID: "test-proj-id",
		URL:       "https://hooks.example.test/issues",
		Events:    []string{"issue.resolved"},
	}); err != nil {
		t.Fatalf("Create hook: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{Hooks: hooks})))
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/issues/grp-hook-issue/", map[string]string{"status": "resolved"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	payload := payloads.action("issue.resolved")
	if payload == nil {
		t.Fatalf("expected issue.resolved hook, got %#v", payloads.snapshot())
	}
	issue := payload["data"].(map[string]any)["issue"].(map[string]any)
	if got := issue["id"]; got != "grp-hook-issue" {
		t.Fatalf("issue.id = %v, want grp-hook-issue", got)
	}
	if got := issue["status"]; got != "resolved" {
		t.Fatalf("issue.status = %v, want resolved", got)
	}
}

type apiHookPayloads struct {
	mu    sync.Mutex
	items []map[string]any
}

func (h *apiHookPayloads) add(item map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items = append(h.items, item)
}

func (h *apiHookPayloads) snapshot() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]map[string]any, len(h.items))
	copy(out, h.items)
	return out
}

func (h *apiHookPayloads) action(name string) map[string]any {
	for _, item := range h.snapshot() {
		if action, _ := item["action"].(string); action == name {
			return item
		}
	}
	return nil
}

type apiHookRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn apiHookRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func newAPIHookCaptureClient(t *testing.T) (*http.Client, *apiHookPayloads) {
	t.Helper()
	payloads := &apiHookPayloads{}
	client := &http.Client{Transport: apiHookRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode hook payload: %v", err)
		}
		payloads.add(payload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}
	return client, payloads
}
