package sqlite

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestHookStore_FireHooksFiltersAndWildcard(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'org', 'Org')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'proj', 'Project', 'go', 'active')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	payloads := &capturedHookBodies{}
	client := &http.Client{Transport: hookRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode hook payload: %v", err)
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader("bad request")),
				Header:     make(http.Header),
			}, nil
		}
		payloads.add(payload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}

	hooks := NewHookStore(db)
	hooks.HTTPClient = client
	for _, hook := range []*ServiceHook{
		{ProjectID: "proj-1", URL: "https://hooks.example.test/event-created", Events: []string{"event.created"}},
		{ProjectID: "proj-1", URL: "https://hooks.example.test/all", Events: nil},
		{ProjectID: "proj-1", URL: "https://hooks.example.test/event-alert", Events: []string{"event.alert"}},
		{ProjectID: "proj-1", URL: "https://hooks.example.test/disabled", Events: []string{"event.created"}, Status: "disabled"},
	} {
		if err := hooks.Create(ctx, hook); err != nil {
			t.Fatalf("Create hook: %v", err)
		}
	}

	payload := map[string]any{"action": "event.created", "data": map[string]any{"event": map[string]any{"id": "evt-1"}}}
	if err := hooks.FireHooks(ctx, "proj-1", "event.created", payload); err != nil {
		t.Fatalf("FireHooks: %v", err)
	}

	items := payloads.snapshot()
	if len(items) != 2 {
		t.Fatalf("captured %d hook payloads, want 2", len(items))
	}
	for _, item := range items {
		if got := item["action"]; got != "event.created" {
			t.Fatalf("action = %v, want event.created", got)
		}
	}
}

type capturedHookBodies struct {
	mu    sync.Mutex
	items []map[string]any
}

func (c *capturedHookBodies) add(item map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, item)
}

func (c *capturedHookBodies) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.items))
	copy(out, c.items)
	return out
}

type hookRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn hookRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
