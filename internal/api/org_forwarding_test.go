package api

import (
	"net/http"
	"testing"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestAPICreateOrgForwarding_RejectsPrivateURL(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPost(t, ts, "/api/0/organizations/test-org/forwarding/", map[string]any{
		"type": "webhook",
		"name": "private",
		"url":  "http://127.0.0.1/hook",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIUpdateOrgForwarding_RejectsPrivateURL(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	forwarders := sqlite.NewOrgForwarderStore(db)
	if err := forwarders.CreateOrgForwarder(t.Context(), &store.OrgDataForwarder{
		OrgID:           "test-org-id",
		Type:            "webhook",
		Name:            "public",
		URL:             "https://hooks.example.test/incoming",
		CredentialsJSON: "{}",
		Enabled:         true,
	}); err != nil {
		t.Fatalf("CreateOrgForwarder: %v", err)
	}

	items, err := forwarders.ListOrgForwarders(t.Context(), "test-org-id")
	if err != nil || len(items) != 1 {
		t.Fatalf("ListOrgForwarders: %v len=%d", err, len(items))
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/organizations/test-org/forwarding/"+items[0].ID+"/", map[string]any{
		"type":        "webhook",
		"name":        "private",
		"url":         "http://127.0.0.1/hook",
		"enabled":     true,
		"credentials": map[string]any{},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
