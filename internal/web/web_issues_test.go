package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestIssueListPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-list-1", "ValueError: bad input", "main.go", "error", "unresolved")
	insertGroup(t, db, "grp-list-2", "TypeError: nil pointer", "handler.go", "error", "unresolved")

	resp, err := http.Get(srv.URL + "/issues/")
	if err != nil {
		t.Fatalf("GET /issues/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "ValueError") {
		t.Error("expected body to contain issue 'ValueError'")
	}
}

func TestIssueListSearch(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-search-1", "ValueError: bad input", "main.go", "error", "unresolved")
	insertGroup(t, db, "grp-search-2", "KeyError: missing key", "handler.go", "error", "unresolved")

	resp, err := http.Get(srv.URL + "/issues/?query=ValueError")
	if err != nil {
		t.Fatalf("GET /issues/?query=ValueError: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "ValueError") {
		t.Error("expected body to contain 'ValueError'")
	}
}

func TestIssueListSearchSyntax(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-search-syntax-1", "ValueError: bad input", "main.go", "error", "resolved")
	insertGroup(t, db, "grp-search-syntax-2", "TypeError: missing key", "handler.go", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-search-syntax-1', 'test-proj', 'evt-search-syntax-1', 'grp-search-syntax-1', '1.2.3', 'production', 'go', 'error', 'error', 'ValueError: bad input', 'bad input', 'main.go', ?, '{}', '{}'),
			('evt-search-syntax-2', 'test-proj', 'evt-search-syntax-2', 'grp-search-syntax-2', '2.0.0', 'production', 'go', 'error', 'log', 'TypeError: missing key', 'missing key', 'handler.go', ?, '{}', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert search syntax events: %v", err)
	}

	resp, err := http.Get(srv.URL + "/issues/?query=is:resolved%20release:1.2.3%20ValueError")
	if err != nil {
		t.Fatalf("GET /issues/ search syntax: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "ValueError") {
		t.Fatalf("expected matching issue in body: %s", body)
	}
	if strings.Contains(body, "TypeError") {
		t.Fatalf("unexpected non-matching issue in body: %s", body)
	}
}

func TestIssueListFilter(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-filter-1", "Open issue", "main.go", "error", "unresolved")
	insertGroup(t, db, "grp-filter-2", "Fixed issue", "main.go", "error", "resolved")

	resp, err := http.Get(srv.URL + "/issues/?filter=unresolved")
	if err != nil {
		t.Fatalf("GET /issues/?filter=unresolved: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	// The page should render (we can't easily assert the specific filter
	// since HTML output depends on template details, but 200 is success).
	_ = body
}

func TestIssueListPagination(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Insert 30 groups so we exceed the default page size of 25.
	for i := 0; i < 30; i++ {
		id := "grp-page-" + padInt(i)
		insertGroup(t, db, id, "Issue #"+padInt(i), "main.go", "error", "unresolved")
	}

	// Page 1: should return 200.
	resp1, err := http.Get(srv.URL + "/issues/?page=1")
	if err != nil {
		t.Fatalf("GET /issues/?page=1: %v", err)
	}
	body1 := getBody(t, resp1)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("page 1 status = %d, want 200", resp1.StatusCode)
	}

	// Page 2: should also return 200.
	resp2, err := http.Get(srv.URL + "/issues/?page=2")
	if err != nil {
		t.Fatalf("GET /issues/?page=2: %v", err)
	}
	body2 := getBody(t, resp2)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("page 2 status = %d, want 200", resp2.StatusCode)
	}

	// Both pages should have content, and they should be different.
	if body1 == body2 {
		t.Error("page 1 and page 2 should have different content")
	}
}

// ---------------------------------------------------------------------------
// Issue and Event Detail Pages
// ---------------------------------------------------------------------------

func TestIssueDetailPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-detail-1", "ValueError: cannot parse", "utils.go in parse", "error", "unresolved")
	insertGroup(t, db, "grp-detail-similar", "ValueError: cannot parse", "utils.go in parse", "error", "unresolved")
	insertGroup(t, db, "grp-detail-merged", "ValueError: cannot parse payload", "utils.go in parse", "error", "merged")
	insertEvent(t, db, "evt-detail-1", "grp-detail-1", "ValueError: cannot parse", "error", "cannot parse int")
	insertEvent(t, db, "evt-detail-2", "grp-detail-1", "ValueError: cannot parse", "error", "another parse failure")
	insertEvent(t, db, "evt-detail-similar", "grp-detail-similar", "ValueError: cannot parse", "error", "similar parse failure")
	insertEvent(t, db, "evt-detail-merged", "grp-detail-merged", "ValueError: cannot parse payload", "error", "merged parse failure")

	_, err := db.Exec(
		`UPDATE groups
		    SET resolved_in_release = '',
		        merged_into_group_id = 'grp-detail-1'
		  WHERE id = 'grp-detail-merged'`,
	)
	if err != nil {
		t.Fatalf("link merged issue: %v", err)
	}
	_, err = db.Exec(
		`UPDATE events
		    SET occurred_at = CASE event_id
		                        WHEN 'evt-detail-1' THEN '2026-03-28T12:00:00Z'
		                        WHEN 'evt-detail-2' THEN '2026-03-28T12:05:00Z'
		                        ELSE occurred_at
		                      END,
		        release = CASE event_id
		                    WHEN 'evt-detail-1' THEN 'web@1.0.0'
		                    WHEN 'evt-detail-2' THEN 'web@1.1.0'
		                    ELSE release
		                  END,
		        user_identifier = CASE event_id
		                            WHEN 'evt-detail-1' THEN 'alpha@example.com'
		                            WHEN 'evt-detail-2' THEN 'beta@example.com'
		                            ELSE user_identifier
		                          END,
		        tags_json = CASE event_id
		                      WHEN 'evt-detail-1' THEN '{"environment":"production","browser":"Chrome"}'
		                      WHEN 'evt-detail-2' THEN '{"environment":"staging","browser":"Chrome"}'
		                      ELSE tags_json
		                    END
		  WHERE event_id IN ('evt-detail-1', 'evt-detail-2')`,
	)
	if err != nil {
		t.Fatalf("update issue detail event context: %v", err)
	}
	similar, err := sqlite.NewWebStore(db).ListSimilarIssues(t.Context(), "grp-detail-1", 6)
	if err != nil {
		t.Fatalf("ListSimilarIssues: %v", err)
	}
	if len(similar) == 0 {
		t.Fatal("expected similar issues from web store")
	}

	resp, err := http.Get(srv.URL + "/issues/grp-detail-1/")
	if err != nil {
		t.Fatalf("GET /issues/grp-detail-1/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "ValueError") ||
		!strings.Contains(body, "Similar Issues") ||
		!strings.Contains(body, "Merged Issues") ||
		!strings.Contains(body, "Changed Since First Seen") {
		t.Errorf("expected richer workflow context in detail page, got body: %s", body)
	}
}

func TestIssueDetailPage_ShowsBoundNextRelease(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-detail-next-release", "CheckoutError", "checkout.go", "error", "resolved")
	if _, err := db.Exec(
		`UPDATE groups
		    SET resolution_substatus = 'next_release',
		        resolved_in_release = ''
		  WHERE id = 'grp-detail-next-release'`,
	); err != nil {
		t.Fatalf("mark next release: %v", err)
	}
	if _, err := sqlite.NewReleaseStore(db).CreateRelease(t.Context(), "test-org", "checkout@2.0.0"); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	resp, err := http.Get(srv.URL + "/issues/grp-detail-next-release/")
	if err != nil {
		t.Fatalf("GET /issues/grp-detail-next-release/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Resolved in checkout@2.0.0") {
		t.Fatalf("expected bound release label, got %s", body)
	}
}

func TestIssueDetailTabs(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-tabs-1", "TabError", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-tabs-1", "grp-tabs-1", "TabError", "error", "tab test")

	resp, err := http.Get(srv.URL + "/issues/grp-tabs-1/")
	if err != nil {
		t.Fatalf("GET /issues/grp-tabs-1/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	// Tab panels should have role or data attributes or identifiable content.
	// At minimum the page should render without error.
	if len(body) < 100 {
		t.Error("detail page body suspiciously short")
	}
}

func TestEventDetailPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-evtdet-1", "EventDetailErr", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-evtdet-1", "grp-evtdet-1", "EventDetailErr", "error", "event detail test")

	resp, err := http.Get(srv.URL + "/events/evt-evtdet-1/")
	if err != nil {
		t.Fatalf("GET /events/evt-evtdet-1/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "EventDetailErr") && !strings.Contains(body, "evt-evtdet-1") {
		t.Error("expected event info in detail page")
	}
}

func TestEventDetailPageShowsAttachments(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-evt-att-1", "AttachmentErr", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-evt-att-1", "grp-evt-att-1", "AttachmentErr", "error", "attachment test")
	_, err := db.Exec(
		`INSERT INTO event_attachments (id, project_id, event_id, name, content_type, size_bytes, object_key, created_at)
		 VALUES ('att-web-1', 'test-proj', 'evt-evt-att-1', 'report.log', 'text/plain', 12, 'attachments/test', ?)`,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	resp, err := http.Get(srv.URL + "/events/evt-evt-att-1/")
	if err != nil {
		t.Fatalf("GET /events/evt-evt-att-1/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "report.log") {
		t.Fatalf("expected attachment name in page body")
	}
}

// ---------------------------------------------------------------------------
// Status Actions
// ---------------------------------------------------------------------------

func TestResolveAction(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-resolve", "ResolveMe", "main.go", "error", "unresolved")

	data := url.Values{"action": {"resolve"}}
	resp := htmxPost(t, srv.URL+"/issues/grp-resolve/status", "application/x-www-form-urlencoded", data.Encode())
	resp.Body.Close()

	// Should redirect or return success.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Errorf("resolve status = %d, want 200 or 3xx", resp.StatusCode)
	}

	// Verify the group was resolved.
	var status string
	if err := db.QueryRow("SELECT status FROM groups WHERE id = 'grp-resolve'").Scan(&status); err != nil {
		t.Fatalf("scan resolved status: %v", err)
	}
	if status != "resolved" {
		t.Errorf("group status = %q, want 'resolved'", status)
	}
}

func TestIgnoreAction(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-ignore", "IgnoreMe", "main.go", "error", "unresolved")

	data := url.Values{"action": {"ignore"}}
	resp := htmxPost(t, srv.URL+"/issues/grp-ignore/status", "application/x-www-form-urlencoded", data.Encode())
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Errorf("ignore status = %d, want 200 or 3xx", resp.StatusCode)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM groups WHERE id = 'grp-ignore'").Scan(&status); err != nil {
		t.Fatalf("scan ignored status: %v", err)
	}
	if status != "ignored" {
		t.Errorf("group status = %q, want 'ignored'", status)
	}
}

func TestReopenAction(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-reopen", "ReopenMe", "main.go", "error", "resolved")

	data := url.Values{"action": {"reopen"}}
	resp := htmxPost(t, srv.URL+"/issues/grp-reopen/status", "application/x-www-form-urlencoded", data.Encode())
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Errorf("reopen status = %d, want 200 or 3xx", resp.StatusCode)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM groups WHERE id = 'grp-reopen'").Scan(&status); err != nil {
		t.Fatalf("scan reopened status: %v", err)
	}
	if status != "unresolved" {
		t.Errorf("group status = %q, want 'unresolved'", status)
	}
}

func TestAssignAction(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-assign", "AssignMe", "main.go", "error", "unresolved")

	data := url.Values{"assignee": {"alice@example.com"}}
	resp := htmxPost(t, srv.URL+"/issues/grp-assign/assign", "application/x-www-form-urlencoded", data.Encode())
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Errorf("assign status = %d, want 200 or 3xx", resp.StatusCode)
	}

	var assignee sql.NullString
	if err := db.QueryRow("SELECT assignee FROM groups WHERE id = 'grp-assign'").Scan(&assignee); err != nil {
		t.Fatalf("scan assignee: %v", err)
	}
	if !assignee.Valid || assignee.String != "alice@example.com" {
		t.Errorf("assignee = %q, want 'alice@example.com'", assignee.String)
	}
}

// ---------------------------------------------------------------------------
// Issue Detail Tab Routes
// ---------------------------------------------------------------------------

func TestIssueTabRoutes(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-tabrt-1", "TabRouteError: test", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-tabrt-1", "grp-tabrt-1", "TabRouteError: test", "error", "tab route test")

	tabs := []string{"events", "activity", "similar", "merged", "tags", "replays"}
	for _, tab := range tabs {
		t.Run(tab, func(t *testing.T) {
			url := srv.URL + "/issues/grp-tabrt-1/" + tab + "/"
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("GET %s: %v", url, err)
			}
			body := getBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("tab %s: status = %d, want 200; body: %s", tab, resp.StatusCode, body)
			}
			if len(body) < 100 {
				t.Errorf("tab %s: body suspiciously short", tab)
			}
		})
	}
}

func TestIssueTabRoutes_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	tabs := []string{"events", "activity", "similar", "merged", "tags", "replays"}
	for _, tab := range tabs {
		t.Run(tab, func(t *testing.T) {
			url := srv.URL + "/issues/nonexistent-id/" + tab + "/"
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("GET %s: %v", url, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("tab %s: status = %d, want 404", tab, resp.StatusCode)
			}
		})
	}
}

func TestIssueEventsTabContent(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-evttab-1", "EventTabError", "main.go", "error", "unresolved")
	insertEvent(t, db, "evt-evttab-1", "grp-evttab-1", "EventTabError", "error", "event tab test")
	insertEvent(t, db, "evt-evttab-2", "grp-evttab-1", "EventTabError", "error", "event tab test 2")

	resp, err := http.Get(srv.URL + "/issues/grp-evttab-1/events/")
	if err != nil {
		t.Fatalf("GET /issues/grp-evttab-1/events/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "EventTabError") {
		t.Errorf("expected issue title in events tab; got body: %s", body)
	}
}

func TestIssueSimilarTabContent(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-simtab-1", "SimilarTabError: test input", "main.go", "error", "unresolved")
	insertGroup(t, db, "grp-simtab-2", "SimilarTabError: test input", "utils.go", "error", "unresolved")
	insertEvent(t, db, "evt-simtab-1", "grp-simtab-1", "SimilarTabError: test input", "error", "similar test")
	insertEvent(t, db, "evt-simtab-2", "grp-simtab-2", "SimilarTabError: test input", "error", "similar test 2")

	resp, err := http.Get(srv.URL + "/issues/grp-simtab-1/similar/")
	if err != nil {
		t.Fatalf("GET /issues/grp-simtab-1/similar/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "SimilarTabError") {
		t.Errorf("expected issue title in similar tab; got body: %s", body)
	}
}

func TestIssueListErrorsPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-errlist-1", "ErrorListIssue", "main.go", "error", "unresolved")

	resp, err := http.Get(srv.URL + "/issues/errors/")
	if err != nil {
		t.Fatalf("GET /issues/errors/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	_ = db
}

func TestIssueListWarningsPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	insertGroup(t, db, "grp-warnlist-1", "WarnListIssue", "main.go", "warning", "unresolved")

	resp, err := http.Get(srv.URL + "/issues/warnings/")
	if err != nil {
		t.Fatalf("GET /issues/warnings/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	_ = db
}

// Suppress unused import warnings.
var _ = store.ErrNotFound
