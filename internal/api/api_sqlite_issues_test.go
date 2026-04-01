package api

import (
	"net/http"
	"testing"
	"time"
)

func TestAPIListIssues_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-1", "ValueError: bad input", "main.go in handler", "error", "unresolved")
	insertSQLiteGroup(t, db, "grp-api-2", "TypeError: nil pointer", "util.go in parse", "error", "resolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/issues/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var issues []Issue
	decodeBody(t, resp, &issues)

	// The response should include at least the 2 SQLite groups.
	foundGrp1 := false
	foundGrp2 := false
	for _, iss := range issues {
		if iss.Title == "ValueError: bad input" {
			foundGrp1 = true
		}
		if iss.Title == "TypeError: nil pointer" {
			foundGrp2 = true
		}
	}
	if !foundGrp1 {
		t.Error("expected to find 'ValueError: bad input' in issue list")
	}
	if !foundGrp2 {
		t.Error("expected to find 'TypeError: nil pointer' in issue list")
	}
}

func TestAPIListIssues_SQLite_SearchSyntax(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-search-1", "ValueError: bad input", "main.go in handler", "error", "resolved")
	insertSQLiteGroup(t, db, "grp-api-search-2", "TypeError: nil pointer", "util.go in parse", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json)
		 VALUES
			('evt-search-1', 'test-proj-id', 'evt-search-1', 'grp-api-search-1', '1.2.3', 'production', 'go', 'error', 'error', 'ValueError: bad input', 'test message', 'main.go', ?, '{}', '{}'),
			('evt-search-2', 'test-proj-id', 'evt-search-2', 'grp-api-search-2', '2.0.0', 'production', 'go', 'error', 'log', 'TypeError: nil pointer', 'log message', 'util.go', ?, '{}', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert search events: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/issues/?query=is:resolved%20release:1.2.3%20ValueError")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var issues []Issue
	decodeBody(t, resp, &issues)
	if len(issues) != 1 || issues[0].ID != "grp-api-search-1" {
		t.Fatalf("unexpected issue search results: %+v", issues)
	}
}

func TestAPIGetIssue_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-get", "KeyError: missing key", "auth.go", "error", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/grp-api-get/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var iss Issue
	decodeBody(t, resp, &iss)
	if iss.Title != "KeyError: missing key" {
		t.Errorf("Title = %q, want 'KeyError: missing key'", iss.Title)
	}
}

func TestAPIGetIssue_SQLite_NotFound(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/issues/nonexistent-grp/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
