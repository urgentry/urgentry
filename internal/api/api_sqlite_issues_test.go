package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
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
	if iss.Permalink != "/organizations/test-org/issues/grp-api-get/" {
		t.Fatalf("Permalink = %q, want org-scoped issue URL", iss.Permalink)
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
	project, ok := payload[0]["project"].(map[string]any)
	if !ok {
		t.Fatalf("project = %#v, want object", payload[0]["project"])
	}
	if project["name"] != "Test Project" || project["platform"] != "go" {
		t.Fatalf("project = %#v, want Test Project/go", project)
	}
	if payload[0]["count"] != "1" {
		t.Fatalf("count = %#v, want string 1", payload[0]["count"])
	}
	if _, ok := payload[0]["resolutionSubstatus"]; ok {
		t.Fatalf("unexpected legacy resolutionSubstatus field in issue payload: %+v", payload[0])
	}
}

func TestAPIListIssues_SQLite_NumComments(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-comment-list", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	authStore := sqlite.NewAuthStore(db)
	principal, err := authStore.AuthenticatePAT(context.Background(), "gpat_test_admin_token")
	if err != nil {
		t.Fatalf("AuthenticatePAT: %v", err)
	}
	groupStore := sqlite.NewGroupStore(db)
	if _, err := groupStore.AddIssueComment(context.Background(), "grp-api-comment-list", principal.User.ID, "first"); err != nil {
		t.Fatalf("AddIssueComment first: %v", err)
	}
	if _, err := groupStore.AddIssueComment(context.Background(), "grp-api-comment-list", principal.User.ID, "second"); err != nil {
		t.Fatalf("AddIssueComment second: %v", err)
	}

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/issues/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(payload))
	}
	if comments, ok := payload[0]["numComments"].(float64); !ok || int(comments) != 2 {
		t.Fatalf("numComments = %#v, want 2", payload[0]["numComments"])
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
	project, ok := payload["project"].(map[string]any)
	if !ok {
		t.Fatalf("project = %#v, want object", payload["project"])
	}
	if project["name"] != "Test Project" || project["platform"] != "go" {
		t.Fatalf("project = %#v, want Test Project/go", project)
	}
	if payload["count"] != "1" {
		t.Fatalf("count = %#v, want string 1", payload["count"])
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
	points24, ok := stats["24h"].([]any)
	if !ok || len(points24) != 24 {
		t.Fatalf("stats[24h] = %#v, want 24 buckets", stats["24h"])
	}
	for i, point := range points24 {
		bucket, ok := point.([]any)
		if !ok || len(bucket) != 2 {
			t.Fatalf("stats[24h][%d] = %#v, want [timestamp, count]", i, point)
		}
	}
	points30, ok := stats["30d"].([]any)
	if !ok || len(points30) != 30 {
		t.Fatalf("stats[30d] = %#v, want 30 buckets", stats["30d"])
	}
	for i, point := range points30 {
		bucket, ok := point.([]any)
		if !ok || len(bucket) != 2 {
			t.Fatalf("stats[30d][%d] = %#v, want [timestamp, count]", i, point)
		}
	}
	if payload["permalink"] != "/organizations/test-org/issues/grp-api-parity/" {
		t.Fatalf("permalink = %#v, want org-scoped issue URL", payload["permalink"])
	}
	if _, ok := payload["logger"]; !ok {
		t.Fatalf("logger missing from issue detail: %+v", payload)
	}
	for _, key := range []string{"annotations", "pluginActions", "pluginContexts", "pluginIssues", "tags", "seenBy", "participants"} {
		items, ok := payload[key].([]any)
		if !ok {
			t.Fatalf("%s = %#v, want array", key, payload[key])
		}
		if len(items) != 0 {
			t.Fatalf("%s = %#v, want empty array for seeded fixture", key, payload[key])
		}
	}
	activity, ok := payload["activity"].([]any)
	if !ok {
		t.Fatalf("activity = %#v, want array", payload["activity"])
	}
	if len(activity) != 1 {
		t.Fatalf("activity = %#v, want 1 comment activity entry", activity)
	}
	statusDetails, ok := payload["statusDetails"].(map[string]any)
	if !ok {
		t.Fatalf("statusDetails = %#v, want object", payload["statusDetails"])
	}
	if statusDetails["inNextRelease"] != true {
		t.Fatalf("statusDetails = %#v, want inNextRelease=true", statusDetails)
	}
	subscriptionDetails, ok := payload["subscriptionDetails"].(map[string]any)
	if !ok {
		t.Fatalf("subscriptionDetails = %#v, want object", payload["subscriptionDetails"])
	}
	if len(subscriptionDetails) != 0 {
		t.Fatalf("subscriptionDetails = %#v, want empty object", subscriptionDetails)
	}
	if payload["shareId"] != nil {
		t.Fatalf("shareId = %#v, want null", payload["shareId"])
	}
	resp = authGet(t, ts, "/api/0/organizations/test-org/issues/grp-api-parity/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("org-scoped issue status = %d, want 200", resp.StatusCode)
	}
	var orgPayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&orgPayload); err != nil {
		t.Fatalf("decode org-scoped payload: %v", err)
	}
	if orgPayload["id"] != "grp-api-parity" || orgPayload["permalink"] != "/organizations/test-org/issues/grp-api-parity/" {
		t.Fatalf("unexpected org-scoped issue payload: %+v", orgPayload)
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

func TestAPIBulkIssueOperationsCapIDs_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	params := url.Values{}
	for i := 0; i < maxBulkIssueIDs+1; i++ {
		params.Add("id", "grp-bulk-cap")
	}
	path := "/api/0/organizations/test-org/issues/?" + params.Encode()

	resp := authPut(t, ts, path, bulkMutateRequest{Status: "ignored"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bulk mutate status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	resp = authDelete(t, ts, path)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bulk delete status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIProjectBulkIssueOperationsCapIDs_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	params := url.Values{}
	for i := 0; i < maxBulkIssueIDs+1; i++ {
		params.Add("id", "grp-project-bulk-cap")
	}
	path := "/api/0/projects/test-org/test-project/issues/?" + params.Encode()

	resp := authPut(t, ts, path, bulkMutateRequest{Status: "ignored"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("project bulk mutate status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	resp = authDelete(t, ts, path)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("project bulk delete status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIBulkIssueMutateRoutesShareBehavior_SQLite(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{name: "organization", path: "/api/0/organizations/test-org/issues/"},
		{name: "project", path: "/api/0/projects/test-org/test-project/issues/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestSQLite(t)
			issueID := "grp-bulk-shared-" + tc.name
			insertSQLiteGroup(t, db, issueID, "ValueError: bad input", "main.go in handler", "error", "unresolved")

			ts := newSQLiteTestServer(t, db)
			defer ts.Close()

			resp := authPut(t, ts, tc.path+"?id="+issueID, map[string]any{
				"status":     "resolved",
				"assignedTo": "backend",
				"statusDetails": map[string]any{
					"inNextRelease": true,
				},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			var payload map[string]any
			decodeBody(t, resp, &payload)
			if payload["status"] != "resolved" {
				t.Fatalf("response status = %#v, want resolved", payload["status"])
			}
			statusDetails, ok := payload["statusDetails"].(map[string]any)
			if !ok {
				t.Fatalf("statusDetails = %#v, want object", payload["statusDetails"])
			}
			if statusDetails["inNextRelease"] != true {
				t.Fatalf("statusDetails = %#v, want inNextRelease=true", statusDetails)
			}

			var status, assignee, substatus, release string
			if err := db.QueryRow(
				`SELECT status, COALESCE(assignee, ''), COALESCE(resolution_substatus, ''), COALESCE(resolved_in_release, '') FROM groups WHERE id = ?`,
				issueID,
			).Scan(&status, &assignee, &substatus, &release); err != nil {
				t.Fatalf("load mutated issue: %v", err)
			}
			if status != "resolved" {
				t.Fatalf("stored status = %q, want resolved", status)
			}
			if assignee != "backend" {
				t.Fatalf("stored assignee = %q, want backend", assignee)
			}
			if substatus != "next_release" {
				t.Fatalf("stored substatus = %q, want next_release", substatus)
			}
			if release != "" {
				t.Fatalf("stored release = %q, want empty", release)
			}
		})
	}
}

func TestAPIUpdateIssue_SQLite_RejectsHasSeenMutation(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-update", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/issues/grp-api-update/", map[string]any{
		"hasSeen": true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update issue status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIUpdateIssue_SQLite_MutatesBookmarkAndPriority(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-update", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authPut(t, ts, "/api/0/issues/grp-api-update/", map[string]any{
		"isBookmarked": true,
		"priority":     2,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update issue status = %d, want 200", resp.StatusCode)
	}

	var updated Issue
	decodeBody(t, resp, &updated)
	if !updated.IsBookmarked {
		t.Fatal("updated issue isBookmarked = false, want true")
	}
	if updated.Priority != 2 {
		t.Fatalf("updated issue priority = %d, want 2", updated.Priority)
	}

	authStore := sqlite.NewAuthStore(db)
	principal, err := authStore.AuthenticatePAT(context.Background(), "gpat_test_admin_token")
	if err != nil {
		t.Fatalf("AuthenticatePAT: %v", err)
	}

	var bookmarkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issue_bookmarks WHERE group_id = 'grp-api-update' AND user_id = ?`, principal.User.ID).Scan(&bookmarkCount); err != nil {
		t.Fatalf("query bookmarks: %v", err)
	}
	if bookmarkCount != 1 {
		t.Fatalf("bookmark count = %d, want 1", bookmarkCount)
	}

	var priority int
	if err := db.QueryRow(`SELECT COALESCE(priority, 0) FROM groups WHERE id = 'grp-api-update'`).Scan(&priority); err != nil {
		t.Fatalf("query priority: %v", err)
	}
	if priority != 2 {
		t.Fatalf("stored priority = %d, want 2", priority)
	}

	getResp := authGet(t, ts, "/api/0/issues/grp-api-update/")
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get updated issue status = %d, want 200", getResp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode get payload: %v", err)
	}
	if payload["hasSeen"] != true {
		t.Fatalf("get hasSeen = %#v, want true", payload["hasSeen"])
	}
	if payload["isBookmarked"] != true {
		t.Fatalf("get isBookmarked = %#v, want true", payload["isBookmarked"])
	}
	if got, ok := payload["priority"].(float64); !ok || int(got) != 2 {
		t.Fatalf("get priority = %#v, want 2", payload["priority"])
	}
}

func TestHandleUpdateIssue_NoPrincipalSkipsBookmarkToggle(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-update-no-principal", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	body := bytes.NewBufferString(`{"isBookmarked":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/0/issues/grp-api-update-no-principal/", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("issue_id", "grp-api-update-no-principal")
	rec := httptest.NewRecorder()

	services := controlplane.SQLiteServices(db)
	handler := handleUpdateIssue(db, services.IssueReads, services.Issues, sqlite.NewHookStore(db), func(http.ResponseWriter, *http.Request) bool {
		return true
	})
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var bookmarkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issue_bookmarks WHERE group_id = 'grp-api-update-no-principal'`).Scan(&bookmarkCount); err != nil {
		t.Fatalf("query bookmarks: %v", err)
	}
	if bookmarkCount != 0 {
		t.Fatalf("bookmark count = %d, want 0", bookmarkCount)
	}
}

func TestHandleUpdateIssue_NilUserPrincipalSkipsSubscriptionToggle(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-update-nil-user", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	body := bytes.NewBufferString(`{"isSubscribed":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/0/issues/grp-api-update-nil-user/", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("issue_id", "grp-api-update-nil-user")
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{}))
	rec := httptest.NewRecorder()

	services := controlplane.SQLiteServices(db)
	handler := handleUpdateIssue(db, services.IssueReads, services.Issues, sqlite.NewHookStore(db), func(http.ResponseWriter, *http.Request) bool {
		return true
	})
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var subscriptionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM issue_subscriptions WHERE group_id = 'grp-api-update-nil-user'`).Scan(&subscriptionCount); err != nil {
		t.Fatalf("query subscriptions: %v", err)
	}
	if subscriptionCount != 0 {
		t.Fatalf("subscription count = %d, want 0", subscriptionCount)
	}
}
