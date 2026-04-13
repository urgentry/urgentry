package api

import (
	"net/http"
	"testing"

	"urgentry/internal/integration"
	"urgentry/internal/sqlite"
)

func TestAPIInstallationExternalIssues_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "1", "CheckoutError", "checkout.go", "error", "unresolved")

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		IntegrationRegistry: integration.NewDefaultRegistry(),
		IntegrationStore:    sqlite.NewIntegrationConfigStore(db),
	})
	defer ts.Close()

	install := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/integrations/webhook/install", pat, map[string]any{
		"config": map[string]any{"url": "https://example.com/hook"},
	})
	if install.StatusCode != http.StatusCreated {
		t.Fatalf("install status = %d, want 201", install.StatusCode)
	}
	var installation integration.IntegrationConfig
	decodeBody(t, install, &installation)

	create := authzJSONRequest(t, ts, http.MethodPost, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/", pat, map[string]any{
		"issueId":    1,
		"webUrl":     "https://example.com/ExternalProj/issue-1",
		"project":    "ExternalProj",
		"identifier": "issue-1",
	})
	if create.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", create.StatusCode)
	}
	var created externalIssueLinkResponse
	decodeBody(t, create, &created)
	if created.IssueID != "1" || created.ServiceType != "webhook" || created.DisplayName != "ExternalProj#issue-1" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	update := authzJSONRequest(t, ts, http.MethodPost, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/", pat, map[string]any{
		"issueId":    1,
		"webUrl":     "https://example.com/ExternalProj/issue-1-updated",
		"project":    "ExternalProj",
		"identifier": "issue-1",
	})
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", update.StatusCode)
	}
	var updated externalIssueLinkResponse
	decodeBody(t, update, &updated)
	if updated.ID != created.ID || updated.WebURL != "https://example.com/ExternalProj/issue-1-updated" {
		t.Fatalf("unexpected update response: %+v", updated)
	}

	list := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/issues/1/external-issues/", pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var linked []externalIssueLinkResponse
	decodeBody(t, list, &linked)
	if len(linked) != 1 || linked[0].ID != created.ID || linked[0].WebURL != updated.WebURL {
		t.Fatalf("unexpected list response: %+v", linked)
	}

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/"+created.ID+"/", pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()

	empty := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/issues/1/external-issues/", pat, nil)
	if empty.StatusCode != http.StatusOK {
		t.Fatalf("empty list status = %d, want 200", empty.StatusCode)
	}
	decodeBody(t, empty, &linked)
	if len(linked) != 0 {
		t.Fatalf("expected empty external issue list, got %+v", linked)
	}
}
