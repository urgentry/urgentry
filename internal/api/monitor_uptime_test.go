package api

import (
	"net/http"
	"testing"
)

func TestAPICreateUptimeMonitor_RejectsPrivateURL(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/uptime-monitors/", map[string]any{
		"name": "private target",
		"url":  "http://127.0.0.1/health",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
