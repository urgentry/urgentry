package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"urgentry/internal/sqlite"
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

func TestAPIListIssues_SQLite_IncludesParityFields(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-parity-list", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/issues/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("expected at least one issue")
	}
	for _, key := range []string{"assignedTo", "hasSeen", "isBookmarked", "isPublic", "isSubscribed", "priority", "substatus", "metadata", "type", "numComments", "userCount", "stats"} {
		if _, ok := payload[0][key]; !ok {
			t.Fatalf("expected key %q in issue payload: %+v", key, payload[0])
		}
	}
	if _, ok := payload[0]["resolutionSubstatus"]; ok {
		t.Fatalf("unexpected legacy resolutionSubstatus field in issue payload: %+v", payload[0])
	}
}

func TestAPIGetIssue_SQLite_ParityFields(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-parity", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, user_identifier, tags_json)
		 VALUES
			('evt-parity-1', 'test-proj-id', 'evt-parity-1', 'grp-api-parity', 'error', 'ValueError: bad input', 'bad input', 'go', 'main.go in handler', ?, 'user-a', '{}'),
			('evt-parity-2', 'test-proj-id', 'evt-parity-2', 'grp-api-parity', 'error', 'ValueError: bad input', 'bad input', 'go', 'main.go in handler', ?, 'user-b', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := db.Exec(`UPDATE groups SET assignee = 'owner@example.com', priority = 1, resolution_substatus = 'next_release' WHERE id = 'grp-api-parity'`); err != nil {
		t.Fatalf("update group parity fields: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	authStore := sqlite.NewAuthStore(db)
	principal, err := authStore.AuthenticatePAT(context.Background(), "gpat_test_admin_token")
	if err != nil {
		t.Fatalf("AuthenticatePAT: %v", err)
	}
	groupStore := sqlite.NewGroupStore(db)
	if err := groupStore.ToggleIssueBookmark(context.Background(), "grp-api-parity", principal.User.ID, true); err != nil {
		t.Fatalf("ToggleIssueBookmark: %v", err)
	}
	if err := groupStore.ToggleIssueSubscription(context.Background(), "grp-api-parity", principal.User.ID, true); err != nil {
		t.Fatalf("ToggleIssueSubscription: %v", err)
	}
	if _, err := groupStore.AddIssueComment(context.Background(), "grp-api-parity", principal.User.ID, "first"); err != nil {
		t.Fatalf("AddIssueComment: %v", err)
	}

	resp := authGet(t, ts, "/api/0/issues/grp-api-parity/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %#v, want error", payload["type"])
	}
	if payload["hasSeen"] != true {
		t.Fatalf("hasSeen = %#v, want true", payload["hasSeen"])
	}
	if payload["isBookmarked"] != true {
		t.Fatalf("isBookmarked = %#v, want true", payload["isBookmarked"])
	}
	if payload["isSubscribed"] != true {
		t.Fatalf("isSubscribed = %#v, want true", payload["isSubscribed"])
	}
	if priority, ok := payload["priority"].(float64); !ok || int(priority) != 1 {
		t.Fatalf("priority = %#v, want 1", payload["priority"])
	}
	if payload["substatus"] != "next_release" {
		t.Fatalf("substatus = %#v, want next_release", payload["substatus"])
	}
	if _, ok := payload["resolutionSubstatus"]; ok {
		t.Fatalf("unexpected legacy resolutionSubstatus field in issue detail: %+v", payload)
	}
	if comments, ok := payload["numComments"].(float64); !ok || int(comments) != 1 {
		t.Fatalf("numComments = %#v, want 1", payload["numComments"])
	}
	if users, ok := payload["userCount"].(float64); !ok || int(users) != 2 {
		t.Fatalf("userCount = %#v, want 2", payload["userCount"])
	}

	assignedTo, ok := payload["assignedTo"].(map[string]any)
	if !ok {
		t.Fatalf("assignedTo = %#v, want object", payload["assignedTo"])
	}
	if assignedTo["email"] != "owner@example.com" {
		t.Fatalf("assignedTo.email = %#v, want owner@example.com", assignedTo["email"])
	}

	metadata, ok := payload["metadata"].(map[string]any)
	if !ok || metadata["type"] != "ValueError" {
		t.Fatalf("metadata = %#v, want derived object", payload["metadata"])
	}

	stats, ok := payload["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats = %#v, want object", payload["stats"])
	}
	points, ok := stats["24h"].([]any)
	if !ok || len(points) != 24 {
		t.Fatalf("stats[24h] = %#v, want 24 buckets", stats["24h"])
	}
}

func TestAPIIssueAssignee_Team(t *testing.T) {
	assignee := apiIssueAssignee("team:backend")
	if assignee == nil {
		t.Fatal("expected assignee")
	}
	if assignee.Type != "team" {
		t.Fatalf("type = %q, want team", assignee.Type)
	}
	if assignee.ID != "backend" {
		t.Fatalf("id = %q, want backend", assignee.ID)
	}
}
