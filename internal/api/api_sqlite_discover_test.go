package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestAPIOrganizationDiscover_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	insertSQLiteGroup(t, db, "grp-api-discover-1", "ImportError: bad input", "main.go in handler", "error", "unresolved")
	if _, err := db.Exec(`UPDATE groups SET assignee = 'owner@example.com', priority = 1 WHERE id = 'grp-api-discover-1'`); err != nil {
		t.Fatalf("update discover group parity fields: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-discover-log-1', 'test-proj-id', 'evt-discover-log-1', NULL, '1.2.4', 'production', 'otlp', 'info', 'log', 'api worker started', 'api worker started', 'log.go', ?, '{"logger":"api"}', '{"logger":"api"}'),
			('evt-discover-txn-1', 'test-proj-id', 'evt-discover-txn-1', 'grp-api-discover-1', '1.2.4', 'production', 'go', 'error', 'transaction', 'checkout', 'checkout', 'txn.go', ?, '{}', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert discover rows: %v", err)
	}
	if err := sqlite.NewTraceStore(db).SaveTransaction(t.Context(), &store.StoredTransaction{
		ProjectID:      "test-proj-id",
		EventID:        "evt-discover-txn-1",
		TraceID:        "0123456789abcdef0123456789abcdef",
		SpanID:         "0123456789abcdef",
		Transaction:    "checkout",
		Op:             "http.server",
		Status:         "ok",
		Platform:       "go",
		Environment:    "production",
		ReleaseID:      "1.2.4",
		StartTimestamp: time.Now().UTC().Add(-5 * time.Minute),
		EndTimestamp:   time.Now().UTC(),
		DurationMS:     123.4,
		NormalizedJSON: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("SaveTransaction: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/issues/?query=ImportError")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var issues []Issue
	decodeBody(t, resp, &issues)
	if len(issues) != 1 || issues[0].ProjectRef.Slug != "test-project" {
		t.Fatalf("unexpected org issue results: %+v", issues)
	}
	if issues[0].Priority != 1 {
		t.Fatalf("priority = %d, want 1", issues[0].Priority)
	}
	if issues[0].AssignedTo == nil || issues[0].AssignedTo.Email != "owner@example.com" {
		t.Fatalf("assignedTo = %+v, want owner@example.com", issues[0].AssignedTo)
	}
	if issues[0].Type != "error" {
		t.Fatalf("type = %q, want error", issues[0].Type)
	}
	if issues[0].Metadata["type"] != "ImportError" {
		t.Fatalf("metadata = %#v, want derived type", issues[0].Metadata)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/logs/?query=api")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var logs []DiscoverLog
	decodeBody(t, resp, &logs)
	if len(logs) != 1 || logs[0].ProjectSlug != "test-project" {
		t.Fatalf("unexpected log results: %+v", logs)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/discover/?scope=transactions&query=checkout")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var discover DiscoverResponse
	decodeBody(t, resp, &discover)
	if len(discover.Transactions) != 1 || discover.Transactions[0].ProjectSlug != "test-project" {
		t.Fatalf("unexpected discover transactions: %+v", discover.Transactions)
	}
}

func TestAPIOrganizationDiscoverRequiresOrgQueryScope(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	authStore := sqlite.NewAuthStore(db)
	if _, err := authStore.EnsureBootstrapAccess(t.Context(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org-id",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_test_admin_token",
	}); err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'owner@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("lookup bootstrap user: %v", err)
	}
	limitedPAT, err := authStore.CreatePersonalAccessToken(t.Context(), userID, "Project Reader", []string{auth.ScopeProjectRead}, nil, "gpat_project_read_only")
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	projectResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/issues/", limitedPAT, nil)
	if projectResp.StatusCode != http.StatusOK {
		t.Fatalf("project issues status = %d, want 200", projectResp.StatusCode)
	}
	projectResp.Body.Close()

	discoverResp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/discover/?scope=issues", limitedPAT, nil)
	if discoverResp.StatusCode != http.StatusForbidden {
		t.Fatalf("discover status = %d, want 403", discoverResp.StatusCode)
	}
	discoverResp.Body.Close()
}

func TestAPIOrganizationDiscoverQuotaExhaustion(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteGroup(t, db, "grp-api-discover-guard-1", "ImportError: bad input", "main.go", "error", "unresolved")

	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('test-org-id', ?, 300, 1, 300, 3600)`,
		string(sqlite.QueryWorkloadOrgIssues),
	); err != nil {
		t.Fatalf("insert query guard policy: %v", err)
	}

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	first := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/discover/?scope=issues&query=ImportError", pat, nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first discover status = %d, want 200", first.StatusCode)
	}
	first.Body.Close()

	second := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/discover/?scope=issues&query=ImportError", pat, nil)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second discover status = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on quota exhaustion")
	}
	secondBody := decodeAPIError(t, second)
	if secondBody.Code != "query_guard_blocked" {
		t.Fatalf("error body = %+v, want query_guard_blocked", secondBody)
	}
}
