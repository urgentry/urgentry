package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestProjectSettingsSubRoutes(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed a project key so the keys page has something to render.
	if _, err := db.Exec(`INSERT INTO project_keys (id, project_id, public_key, status) VALUES ('key-1', 'test-proj', 'abc123def456', 'active')`); err != nil {
		// Key table insert failures are non-fatal — the page still renders.
		_ = err
	}

	tabs := []struct {
		path     string
		contains string
	}{
		{"/settings/project/test-project/general/", "General"},
		{"/settings/project/test-project/keys/", "Keys"},
		{"/settings/project/test-project/ownership/", "Ownership"},
		{"/settings/project/test-project/environments/", "Environments"},
		{"/settings/project/test-project/retention/", "Retention"},
		{"/settings/project/test-project/filters/", "Filters"},
	}

	for _, tc := range tabs {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			body := getBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200; body: %s", tc.path, resp.StatusCode, body)
			}
			if !strings.Contains(body, tc.contains) {
				t.Fatalf("GET %s: expected body to contain %q; got: %s", tc.path, tc.contains, body[:minInt(500, len(body))])
			}
		})
	}

	// Unknown slug should 404.
	resp, err := http.Get(srv.URL + "/settings/project/no-such-project/general/")
	if err != nil {
		t.Fatalf("GET unknown slug: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project slug: status = %d, want 404", resp.StatusCode)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
