//go:build integration

package compat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// discoverResponse mirrors api.DiscoverResponse for JSON decoding in tests.
type discoverResponse struct {
	Query        string                `json:"query"`
	Scope        string                `json:"scope"`
	Issues       []discoverIssueRow    `json:"issues"`
	Logs         []discoverLogRow      `json:"logs"`
	Transactions []discoverTxnRow      `json:"transactions"`
}

type discoverIssueRow struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectSlug string `json:"projectSlug"`
	Title       string `json:"title"`
	Culprit     string `json:"culprit"`
	Level       string `json:"level"`
	Status      string `json:"status"`
	Count       int64  `json:"count"`
	ShortID     int    `json:"shortId"`
}

type discoverLogRow struct {
	EventID     string `json:"eventId"`
	ProjectID   string `json:"projectId"`
	ProjectSlug string `json:"projectSlug"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Level       string `json:"level"`
	Platform    string `json:"platform"`
}

type discoverTxnRow struct {
	EventID     string  `json:"eventId"`
	ProjectID   string  `json:"projectId"`
	ProjectSlug string  `json:"projectSlug"`
	Transaction string  `json:"transaction"`
	Op          string  `json:"op,omitempty"`
	Status      string  `json:"status,omitempty"`
	DurationMS  float64 `json:"durationMs"`
	TraceID     string  `json:"traceId"`
}

// seedDiscoverE2EData inserts groups, events, and transactions into the database
// for the discover e2e tests.
func seedDiscoverE2EData(t *testing.T, srv *compatServer) {
	t.Helper()

	now := time.Now().UTC()

	// Insert multiple groups with different levels.
	groups := []struct {
		id, title, culprit, level string
		shortID                   int
	}{
		{"grp-e2e-discover-1", "NullPointerException", "handler.go", "error", 301},
		{"grp-e2e-discover-2", "TimeoutError", "client.go", "warning", 302},
		{"grp-e2e-discover-3", "ValidationError", "form.go", "error", 303},
		{"grp-e2e-discover-4", "DeprecationWarning", "legacy.go", "info", 304},
		{"grp-e2e-discover-5", "ConnectionReset", "net.go", "fatal", 305},
	}
	for i, g := range groups {
		if _, err := srv.db.Exec(
			`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen, short_id)
			 VALUES (?, 'default-project', 'urgentry-v1', ?, ?, ?, ?, 'unresolved', ?, ?, 1, ?)`,
			g.id, g.id, g.title, g.culprit, g.level,
			now.Add(time.Duration(-60+i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(-30+i)*time.Minute).Format(time.RFC3339),
			g.shortID,
		); err != nil {
			t.Fatalf("insert group %s: %v", g.id, err)
		}
	}

	// Insert error events matching the groups.
	for i, g := range groups {
		if _, err := srv.db.Exec(
			`INSERT INTO events
				(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
			 VALUES (?, 'default-project', ?, ?, 'backend@1.0.0', 'production', 'go', ?, 'error', ?, ?, ?, ?, '{}', '{}')`,
			"evt-e2e-err-"+fmt.Sprint(i+1), "evt-e2e-err-"+fmt.Sprint(i+1),
			g.id, g.level, g.title, g.title, g.culprit,
			now.Add(time.Duration(-45+i)*time.Minute).Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert event for %s: %v", g.id, err)
		}
	}

	// Insert log events (not associated with groups).
	for i := 1; i <= 3; i++ {
		if _, err := srv.db.Exec(
			`INSERT INTO events
				(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
			 VALUES (?, 'default-project', ?, NULL, 'backend@1.0.0', 'production', 'go', 'info', 'log', ?, ?, 'worker.go', ?, '{}', '{}')`,
			fmt.Sprintf("evt-e2e-log-%d", i), fmt.Sprintf("evt-e2e-log-%d", i),
			fmt.Sprintf("worker log message %d", i), fmt.Sprintf("worker log message %d", i),
			now.Add(time.Duration(-20+i)*time.Minute).Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert log event %d: %v", i, err)
		}
	}

	// Insert transactions.
	txns := []struct {
		id, eventID, txnName, op, status, traceID, spanID string
		durationMS                                         float64
	}{
		{"txn-e2e-1", "evt-e2e-txn-1", "GET /api/users", "http.server", "ok", "aaaa000000000000aaaa000000000001", "aaaa00000001", 120},
		{"txn-e2e-2", "evt-e2e-txn-2", "POST /api/checkout", "http.server", "ok", "aaaa000000000000aaaa000000000002", "aaaa00000002", 340},
		{"txn-e2e-3", "evt-e2e-txn-3", "GET /api/products", "http.server", "internal_error", "aaaa000000000000aaaa000000000003", "aaaa00000003", 890},
	}
	for i, tx := range txns {
		ts := now.Add(time.Duration(-15+i) * time.Minute)
		if _, err := srv.db.Exec(
			`INSERT INTO transactions
				(id, project_id, event_id, trace_id, span_id, parent_span_id, transaction_name, op, status, platform, environment, release, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, created_at)
			 VALUES (?, 'default-project', ?, ?, ?, '', ?, ?, ?, 'go', 'production', 'backend@1.0.0', ?, ?, ?, '{}', '{}', '{}', ?)`,
			tx.id, tx.eventID, tx.traceID, tx.spanID,
			tx.txnName, tx.op, tx.status,
			ts.Format(time.RFC3339Nano),
			ts.Add(time.Duration(tx.durationMS)*time.Millisecond).Format(time.RFC3339Nano),
			tx.durationMS,
			ts.Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("insert transaction %s: %v", tx.id, err)
		}
		// Also insert the matching event row for transactions.
		if _, err := srv.db.Exec(
			`INSERT INTO events
				(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
			 VALUES (?, 'default-project', ?, NULL, 'backend@1.0.0', 'production', 'go', 'info', 'transaction', ?, ?, 'txn.go', ?, '{}', '{}')`,
			tx.eventID, tx.eventID, tx.txnName, tx.txnName,
			ts.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert txn event %s: %v", tx.eventID, err)
		}
	}
}

func discoverURL(srv *compatServer, params string) string {
	return srv.server.URL + "/api/0/organizations/urgentry-org/discover/" + params
}

func getDiscover(t *testing.T, srv *compatServer, params string) discoverResponse {
	t.Helper()
	resp := apiGet(t, discoverURL(srv, params), srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var dr discoverResponse
	readJSON(t, resp, &dr)
	return dr
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestDiscoverBasicQuery seeds events and queries the discover endpoint,
// verifying results are returned across all scopes.
func TestDiscoverBasicQuery(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// Query all scopes with no filter -- should return issues, logs, and transactions.
	dr := getDiscover(t, srv, "")
	if dr.Scope != "all" {
		t.Fatalf("scope = %q, want all", dr.Scope)
	}
	if len(dr.Issues) == 0 {
		t.Fatal("expected issues in discover response")
	}
	if len(dr.Logs) == 0 {
		t.Fatal("expected logs in discover response")
	}
	if len(dr.Transactions) == 0 {
		t.Fatal("expected transactions in discover response")
	}

	// Query with a text match.
	dr = getDiscover(t, srv, "?query=NullPointer")
	if dr.Query != "NullPointer" {
		t.Fatalf("query = %q, want NullPointer", dr.Query)
	}
	// Should find at least the NullPointerException issue.
	found := false
	for _, issue := range dr.Issues {
		if strings.Contains(issue.Title, "NullPointer") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected NullPointerException in issues, got %+v", dr.Issues)
	}
}

// TestDiscoverFieldsList verifies the response contains all expected fields
// in the returned structures across scopes.
func TestDiscoverFieldsList(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// Get all scopes to verify field presence.
	dr := getDiscover(t, srv, "")

	// Verify top-level fields.
	if dr.Scope == "" {
		t.Fatal("scope field missing from response")
	}

	// Check issue fields.
	if len(dr.Issues) == 0 {
		t.Fatal("no issues to verify fields against")
	}
	issue := dr.Issues[0]
	if issue.ID == "" {
		t.Error("issue.ID is empty")
	}
	if issue.ProjectID == "" {
		t.Error("issue.ProjectID is empty")
	}
	if issue.ProjectSlug == "" {
		t.Error("issue.ProjectSlug is empty")
	}
	if issue.Title == "" {
		t.Error("issue.Title is empty")
	}
	if issue.Level == "" {
		t.Error("issue.Level is empty")
	}
	if issue.Status == "" {
		t.Error("issue.Status is empty")
	}

	// Check log fields.
	if len(dr.Logs) == 0 {
		t.Fatal("no logs to verify fields against")
	}
	log := dr.Logs[0]
	if log.EventID == "" {
		t.Error("log.EventID is empty")
	}
	if log.ProjectID == "" {
		t.Error("log.ProjectID is empty")
	}
	if log.Title == "" {
		t.Error("log.Title is empty")
	}
	if log.Level == "" {
		t.Error("log.Level is empty")
	}

	// Check transaction fields.
	if len(dr.Transactions) == 0 {
		t.Fatal("no transactions to verify fields against")
	}
	txn := dr.Transactions[0]
	if txn.EventID == "" {
		t.Error("txn.EventID is empty")
	}
	if txn.ProjectID == "" {
		t.Error("txn.ProjectID is empty")
	}
	if txn.Transaction == "" {
		t.Error("txn.Transaction is empty")
	}
	if txn.TraceID == "" {
		t.Error("txn.TraceID is empty")
	}
}

// TestDiscoverAggregation verifies that scope=all returns aggregated results
// from issues, logs, and transactions simultaneously.
func TestDiscoverAggregation(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// scope=all should aggregate all three result types.
	dr := getDiscover(t, srv, "?scope=all")
	if dr.Scope != "all" {
		t.Fatalf("scope = %q, want all", dr.Scope)
	}
	if len(dr.Issues) == 0 {
		t.Fatal("scope=all should include issues")
	}
	if len(dr.Logs) == 0 {
		t.Fatal("scope=all should include logs")
	}
	if len(dr.Transactions) == 0 {
		t.Fatal("scope=all should include transactions")
	}

	// Verify counts match expectations from seed data.
	if len(dr.Issues) < 5 {
		t.Fatalf("expected at least 5 issues, got %d", len(dr.Issues))
	}
	if len(dr.Logs) < 3 {
		t.Fatalf("expected at least 3 logs, got %d", len(dr.Logs))
	}
	if len(dr.Transactions) < 3 {
		t.Fatalf("expected at least 3 transactions, got %d", len(dr.Transactions))
	}
}

// TestDiscoverFiltering tests the scope and query parameters for filtering.
func TestDiscoverFiltering(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// scope=issues should return only issues.
	dr := getDiscover(t, srv, "?scope=issues")
	if dr.Scope != "issues" {
		t.Fatalf("scope = %q, want issues", dr.Scope)
	}
	if len(dr.Issues) == 0 {
		t.Fatal("scope=issues returned no issues")
	}
	if len(dr.Logs) != 0 {
		t.Fatalf("scope=issues should not return logs, got %d", len(dr.Logs))
	}
	if len(dr.Transactions) != 0 {
		t.Fatalf("scope=issues should not return transactions, got %d", len(dr.Transactions))
	}

	// scope=logs should return only logs.
	dr = getDiscover(t, srv, "?scope=logs")
	if len(dr.Logs) == 0 {
		t.Fatal("scope=logs returned no logs")
	}
	if len(dr.Issues) != 0 {
		t.Fatalf("scope=logs should not return issues, got %d", len(dr.Issues))
	}
	if len(dr.Transactions) != 0 {
		t.Fatalf("scope=logs should not return transactions, got %d", len(dr.Transactions))
	}

	// scope=transactions should return only transactions.
	dr = getDiscover(t, srv, "?scope=transactions")
	if len(dr.Transactions) == 0 {
		t.Fatal("scope=transactions returned no transactions")
	}
	if len(dr.Issues) != 0 {
		t.Fatalf("scope=transactions should not return issues, got %d", len(dr.Issues))
	}
	if len(dr.Logs) != 0 {
		t.Fatalf("scope=transactions should not return logs, got %d", len(dr.Logs))
	}

	// Query filter should narrow results within a scope.
	dr = getDiscover(t, srv, "?scope=issues&query=Timeout")
	foundTimeout := false
	for _, issue := range dr.Issues {
		if strings.Contains(issue.Title, "Timeout") {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatalf("query=Timeout should find TimeoutError, got %+v", dr.Issues)
	}

	// Transaction query filter.
	dr = getDiscover(t, srv, "?scope=transactions&query=checkout")
	foundCheckout := false
	for _, txn := range dr.Transactions {
		if strings.Contains(txn.Transaction, "checkout") {
			foundCheckout = true
		}
	}
	if !foundCheckout {
		t.Fatalf("query=checkout should find checkout transaction, got %+v", dr.Transactions)
	}
}

// TestDiscoverSorting verifies that results respect the limit parameter,
// effectively controlling result set ordering/truncation.
func TestDiscoverSorting(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// With limit=2, each scope should have at most 2 results.
	dr := getDiscover(t, srv, "?scope=issues&limit=2")
	if len(dr.Issues) > 2 {
		t.Fatalf("expected at most 2 issues with limit=2, got %d", len(dr.Issues))
	}
	if len(dr.Issues) == 0 {
		t.Fatal("expected at least 1 issue with limit=2")
	}

	// With limit=1, only 1 transaction should come back.
	dr = getDiscover(t, srv, "?scope=transactions&limit=1")
	if len(dr.Transactions) > 1 {
		t.Fatalf("expected at most 1 transaction with limit=1, got %d", len(dr.Transactions))
	}
	if len(dr.Transactions) == 0 {
		t.Fatal("expected at least 1 transaction with limit=1")
	}
}

// TestDiscoverPagination tests the limit parameter as a pagination control.
func TestDiscoverPagination(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	seedDiscoverE2EData(t, srv)

	// First "page": limit=3.
	dr1 := getDiscover(t, srv, "?scope=issues&limit=3")
	if len(dr1.Issues) != 3 {
		t.Fatalf("expected 3 issues with limit=3, got %d", len(dr1.Issues))
	}

	// Second "page": limit=100 should return all remaining issues (5 total seeded).
	dr2 := getDiscover(t, srv, "?scope=issues&limit=100")
	if len(dr2.Issues) < 5 {
		t.Fatalf("expected at least 5 issues with limit=100, got %d", len(dr2.Issues))
	}

	// The first page results should be a subset of the full results.
	fullIDs := make(map[string]bool)
	for _, issue := range dr2.Issues {
		fullIDs[issue.ID] = true
	}
	for _, issue := range dr1.Issues {
		if !fullIDs[issue.ID] {
			t.Fatalf("issue %s from limited query not found in full query", issue.ID)
		}
	}
}

// TestDiscoverAuthRequired verifies that the discover endpoint requires
// authentication.
func TestDiscoverAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Request without any auth header.
	req, err := http.NewRequest(http.MethodGet, discoverURL(srv, ""), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET discover: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 without auth, got %d", resp.StatusCode)
	}

	// Request with a bogus PAT.
	resp2 := apiGet(t, discoverURL(srv, ""), "gpat_bogus_token_does_not_exist")
	defer resp2.Body.Close()
	io.ReadAll(resp2.Body)

	if resp2.StatusCode != http.StatusUnauthorized && resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 with bad PAT, got %d", resp2.StatusCode)
	}
}

// TestDiscoverInvalidQuery verifies that a malformed scope returns 400.
func TestDiscoverInvalidQuery(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	// Invalid scope value should return 400.
	resp := apiGet(t, discoverURL(srv, "?scope=invalid_scope"), srv.pat)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid scope, got %d; body: %s", resp.StatusCode, body)
	}

	// Verify error response is valid JSON.
	var errResp map[string]any
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("error response is not valid JSON: %v; body: %s", err, body)
	}
}
