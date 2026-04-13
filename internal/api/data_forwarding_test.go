package api

import (
	"net/http"
	"testing"
)

func TestAPICreateDataForwarding_RejectsPrivateURL(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/data-forwarding/", map[string]any{
		"type": "webhook",
		"url":  "http://127.0.0.1/hook",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
