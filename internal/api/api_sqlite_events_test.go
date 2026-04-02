package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func assertHasEventTag(t *testing.T, tags []EventTag, key, value string) {
	t.Helper()
	for _, tag := range tags {
		if tag.Key == key && tag.Value == value {
			return
		}
	}
	t.Fatalf("tags = %+v, want {%q %q}", tags, key, value)
}

const normalizedEventInterfacesPayload = `{
	"message":"bad input",
	"dist":"12",
	"release":"backend@1.2.3",
	"request":{"method":"GET","url":"https://app.example.com/checkout"},
	"contexts":{
		"trace":{"trace_id":"trace-1","span_id":"span-1","type":"trace"},
		"device":{"name":"iPhone","family":"iPhone","type":"device"}
	},
	"sdk":{"name":"sentry.go","version":"1.2.3"},
	"user":{"id":"user-1","email":"dev@example.com"},
	"fingerprint":["{{ default }}","checkout"],
	"errors":[{"type":"js_no_source"}],
	"packages":{"pkg/errors":"v0.9.1"},
	"measurements":{"lcp":{"value":1234.5,"unit":"millisecond"}},
	"exception":{"values":[{"type":"ValueError","value":"bad input"}]},
	"breadcrumbs":{"values":[{"type":"default","category":"auth","message":"signed in","level":"info"}]}
}`

func TestAPIListEvents_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-evt", "EventListErr", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-1", "grp-api-evt", "EventListErr", "error")
	insertSQLiteEvent(t, db, "evt-api-2", "grp-api-evt", "EventListErr", "error")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/events/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var events []Event
	decodeBody(t, resp, &events)

	// Should contain at least the 2 SQLite events.
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}

	// Verify at least one event has the expected title.
	found := false
	for _, evt := range events {
		if evt.Title == "EventListErr" {
			assertHasEventTag(t, evt.Tags, "environment", "production")
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find event with title 'EventListErr'")
	}
}

func TestAPIGetProjectEvent_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-gevt", "SingleEvent", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-single", "grp-api-gevt", "SingleEvent", "error")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/events/evt-api-single/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var evt Event
	decodeBody(t, resp, &evt)
	if evt.EventID != "evt-api-single" {
		t.Errorf("EventID = %q, want 'evt-api-single'", evt.EventID)
	}
	assertHasEventTag(t, evt.Tags, "environment", "production")
}

func TestAPIListEvents_SQLite_IncludesNormalizedInterfaces(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-evt-interfaces", "EventInterfaces", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-interfaces", "grp-api-evt-interfaces", "EventInterfaces", "error")
	if _, err := db.Exec(`UPDATE events SET payload_json = ? WHERE event_id = 'evt-api-interfaces'`, normalizedEventInterfacesPayload); err != nil {
		t.Fatalf("update payload_json: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/events/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var events []Event
	decodeBody(t, resp, &events)

	var target *Event
	for i := range events {
		if events[i].EventID == "evt-api-interfaces" {
			target = &events[i]
			break
		}
	}
	if target == nil {
		t.Fatal("expected interface-rich event in list response")
	}
	if len(target.Entries) < 4 {
		t.Fatalf("entries = %+v, want synthesized interfaces", target.Entries)
	}
	if target.Contexts["trace"] == nil {
		t.Fatalf("contexts = %#v, want trace context", target.Contexts)
	}
	if target.SDK["name"] != "sentry.go" {
		t.Fatalf("sdk = %#v, want sentry.go", target.SDK)
	}
	if target.User["email"] != "dev@example.com" {
		t.Fatalf("user = %#v, want dev@example.com", target.User)
	}
	if len(target.Fingerprints) != 2 {
		t.Fatalf("fingerprints = %#v, want two items", target.Fingerprints)
	}
	if target.Measurements["lcp"] == nil {
		t.Fatalf("measurements = %#v, want lcp", target.Measurements)
	}
}

func TestAPIListOrgEvents_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-org-evt", "OrgEventListErr", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-org", "grp-api-org-evt", "OrgEventListErr", "error")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/events/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Data []OrgEventRow `json:"data"`
	}
	decodeBody(t, resp, &body)
	if len(body.Data) == 0 {
		t.Fatal("expected at least one org event")
	}
	found := false
	for _, evt := range body.Data {
		if evt.ID == "evt-api-org" {
			assertHasEventTag(t, evt.Tags, "environment", "production")
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find org event %q", "evt-api-org")
	}
}

func TestAPIListOrgEvents_SQLite_FieldSelection(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-org-fs-1", "FieldSelectErr", "main.go", "error", "unresolved")
	insertSQLiteGroup(t, db, "grp-api-org-fs-2", "AnotherErr", "worker.go", "warning", "unresolved")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	// Test 1: field selection with plain fields.
	resp := authGet(t, ts, "/api/0/organizations/test-org/events/?field=title&field=level")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("field selection: expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Fields map[string]string `json:"fields"`
		} `json:"meta"`
	}
	decodeBody(t, resp, &body)
	if len(body.Data) == 0 {
		t.Fatal("field selection: expected at least one row")
	}
	// Verify only requested columns are returned.
	for _, row := range body.Data {
		if _, ok := row["title"]; !ok {
			t.Fatalf("field selection: row missing title: %+v", row)
		}
		if _, ok := row["level"]; !ok {
			t.Fatalf("field selection: row missing level: %+v", row)
		}
	}
	if body.Meta.Fields["title"] != "string" {
		t.Fatalf("meta.fields.title = %q, want string", body.Meta.Fields["title"])
	}
	if body.Meta.Fields["level"] != "string" {
		t.Fatalf("meta.fields.level = %q, want string", body.Meta.Fields["level"])
	}

	// Test 2: aggregation with count().
	resp = authGet(t, ts, "/api/0/organizations/test-org/events/?field=count()")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("count aggregation: expected 200, got %d", resp.StatusCode)
	}
	var aggBody struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Fields map[string]string `json:"fields"`
		} `json:"meta"`
	}
	decodeBody(t, resp, &aggBody)
	if len(aggBody.Data) == 0 {
		t.Fatal("count aggregation: expected at least one row")
	}
	countVal, ok := aggBody.Data[0]["count()"]
	if !ok {
		t.Fatalf("count aggregation: row missing count(): %+v", aggBody.Data[0])
	}
	countNum, ok := countVal.(float64)
	if !ok || countNum < 2 {
		t.Fatalf("count aggregation: count() = %v, want >= 2", countVal)
	}
	if aggBody.Meta.Fields["count()"] != "number" {
		t.Fatalf("meta.fields[count()] = %q, want number", aggBody.Meta.Fields["count()"])
	}

	// Test 3: mixed fields + aggregation (grouped).
	resp = authGet(t, ts, "/api/0/organizations/test-org/events/?field=title&field=count()")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grouped aggregation: expected 200, got %d", resp.StatusCode)
	}
	var groupBody struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Fields map[string]string `json:"fields"`
		} `json:"meta"`
	}
	decodeBody(t, resp, &groupBody)
	if len(groupBody.Data) < 2 {
		t.Fatalf("grouped aggregation: expected at least 2 rows, got %d", len(groupBody.Data))
	}
	for _, row := range groupBody.Data {
		if _, ok := row["title"]; !ok {
			t.Fatalf("grouped aggregation: row missing title: %+v", row)
		}
		if _, ok := row["count()"]; !ok {
			t.Fatalf("grouped aggregation: row missing count(): %+v", row)
		}
	}

	// Test 4: explicit dataset parameter.
	resp = authGet(t, ts, "/api/0/organizations/test-org/events/?field=title&field=level&dataset=issues")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("explicit dataset: expected 200, got %d", resp.StatusCode)
	}
	var dsBody struct {
		Data []map[string]any `json:"data"`
	}
	decodeBody(t, resp, &dsBody)
	if len(dsBody.Data) == 0 {
		t.Fatal("explicit dataset: expected at least one row")
	}
}

func TestAPIResolveEventID_SQLite(t *testing.T) {
	db := openTestSQLite(t)

	insertSQLiteGroup(t, db, "grp-api-resolve-evt", "ResolveEvent", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-resolve", "grp-api-resolve-evt", "ResolveEvent", "error")

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/eventids/evt-api-resolve/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		EventID string `json:"eventId"`
		Event   Event  `json:"event"`
	}
	decodeBody(t, resp, &body)
	if body.EventID != "evt-api-resolve" {
		t.Fatalf("eventId = %q, want %q", body.EventID, "evt-api-resolve")
	}
	assertHasEventTag(t, body.Event.Tags, "environment", "production")
}

func TestAPIGetIssueEvent_SQLite_IncludesNormalizedInterfaces(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-issue-event", "IssueEventInterfaces", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-issue-event", "grp-api-issue-event", "IssueEventInterfaces", "error")
	if _, err := db.Exec(`UPDATE events SET payload_json = ? WHERE event_id = 'evt-api-issue-event'`, normalizedEventInterfacesPayload); err != nil {
		t.Fatalf("update payload_json: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/issues/grp-api-issue-event/events/evt-api-issue-event/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var evt Event
	decodeBody(t, resp, &evt)
	if len(evt.Entries) < 4 {
		t.Fatalf("entries = %+v, want synthesized interfaces", evt.Entries)
	}
	if evt.Contexts["device"] == nil {
		t.Fatalf("contexts = %#v, want device context", evt.Contexts)
	}
	if evt.Packages["pkg/errors"] != "v0.9.1" {
		t.Fatalf("packages = %#v, want pkg/errors", evt.Packages)
	}
	if len(evt.Errors) != 1 {
		t.Fatalf("errors = %#v, want one error", evt.Errors)
	}
}

func TestAPIResolveShortID_SQLite_ParityFields(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-resolve-short", "ValueError: bad input", "main.go in handler", "error", "unresolved")

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, user_identifier, tags_json)
		 VALUES
			('evt-short-1', 'test-proj-id', 'evt-short-1', 'grp-api-resolve-short', 'error', 'ValueError: bad input', 'bad input', 'go', 'main.go in handler', ?, 'user-a', '{}'),
			('evt-short-2', 'test-proj-id', 'evt-short-2', 'grp-api-resolve-short', 'error', 'ValueError: bad input', 'bad input', 'go', 'main.go in handler', ?, 'user-b', '{}')`,
		now, now,
	); err != nil {
		t.Fatalf("insert short id events: %v", err)
	}
	if _, err := db.Exec(`UPDATE groups SET short_id = 42, assignee = 'owner@example.com', priority = 1 WHERE id = 'grp-api-resolve-short'`); err != nil {
		t.Fatalf("update short id group parity fields: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	authStore := sqlite.NewAuthStore(db)
	principal, err := authStore.AuthenticatePAT(context.Background(), "gpat_test_admin_token")
	if err != nil {
		t.Fatalf("AuthenticatePAT: %v", err)
	}
	groupStore := sqlite.NewGroupStore(db)
	if err := groupStore.ToggleIssueBookmark(context.Background(), "grp-api-resolve-short", principal.User.ID, true); err != nil {
		t.Fatalf("ToggleIssueBookmark: %v", err)
	}
	if _, err := groupStore.AddIssueComment(context.Background(), "grp-api-resolve-short", principal.User.ID, "first"); err != nil {
		t.Fatalf("AddIssueComment: %v", err)
	}

	resp := authGet(t, ts, "/api/0/organizations/test-org/shortids/GENTRY-42/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Group Issue `json:"group"`
	}
	decodeBody(t, resp, &body)
	if body.Group.Priority != 1 {
		t.Fatalf("priority = %d, want 1", body.Group.Priority)
	}
	if body.Group.AssignedTo == nil || body.Group.AssignedTo.Email != "owner@example.com" {
		t.Fatalf("assignedTo = %+v, want owner@example.com", body.Group.AssignedTo)
	}
	if !body.Group.IsBookmarked {
		t.Fatalf("isBookmarked = %v, want true", body.Group.IsBookmarked)
	}
	if body.Group.NumComments != 1 {
		t.Fatalf("numComments = %d, want 1", body.Group.NumComments)
	}
	if body.Group.UserCount != 2 {
		t.Fatalf("userCount = %d, want 2", body.Group.UserCount)
	}
	if body.Group.ProjectRef.Name != "Test Project" || body.Group.ProjectRef.Platform != "go" {
		t.Fatalf("project ref = %+v, want Test Project/go", body.Group.ProjectRef)
	}
	if body.Group.Count != "1" {
		t.Fatalf("count = %#v, want string 1", body.Group.Count)
	}
}

func TestAPIEventDetail_SQLite_ParityFields(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteGroup(t, db, "grp-api-event-parity", "EventDetailParity", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-older", "grp-api-event-parity", "EventDetailParity", "error")
	insertSQLiteEvent(t, db, "evt-current", "grp-api-event-parity", "EventDetailParity", "error")
	insertSQLiteEvent(t, db, "evt-newer", "grp-api-event-parity", "EventDetailParity", "error")
	insertSQLiteReleaseWithOrg(t, db, "rel-event-parity", "backend@1.2.3")

	for eventID, timestamp := range map[string]string{
		"evt-older":   "2026-04-01T09:00:00Z",
		"evt-current": "2026-04-01T10:00:00Z",
		"evt-newer":   "2026-04-01T11:00:00Z",
	} {
		if _, err := db.Exec(
			`UPDATE events SET payload_json = ?, release = 'backend@1.2.3', ingested_at = ?, occurred_at = ? WHERE event_id = ?`,
			normalizedEventInterfacesPayload, timestamp, timestamp, eventID,
		); err != nil {
			t.Fatalf("update event %s: %v", eventID, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO user_feedback (id, project_id, event_id, group_id, name, email, comments, created_at)
		 VALUES ('feedback-event-parity', 'test-proj-id', 'evt-current', 'grp-api-event-parity', 'Jane', 'jane@example.com', 'Something broke', '2026-04-01T12:00:00Z')`,
	); err != nil {
		t.Fatalf("insert feedback: %v", err)
	}

	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	assertEventDetail := func(t *testing.T, evt Event) {
		t.Helper()
		if evt.Type != "error" {
			t.Fatalf("type = %q, want error", evt.Type)
		}
		if evt.PreviousEventID != "evt-newer" {
			t.Fatalf("previousEventID = %q, want evt-newer", evt.PreviousEventID)
		}
		if evt.NextEventID != "evt-older" {
			t.Fatalf("nextEventID = %q, want evt-older", evt.NextEventID)
		}
		if evt.Size <= 0 {
			t.Fatalf("size = %d, want > 0", evt.Size)
		}
		if evt.DateReceived == nil || evt.DateReceived.Format(time.RFC3339) != "2026-04-01T10:00:00Z" {
			t.Fatalf("dateReceived = %#v, want 2026-04-01T10:00:00Z", evt.DateReceived)
		}
		if evt.Dist != "12" {
			t.Fatalf("dist = %q, want 12", evt.Dist)
		}
		if evt.Release == nil || evt.Release.Version != "backend@1.2.3" {
			t.Fatalf("release = %+v, want backend@1.2.3", evt.Release)
		}
		if evt.UserReport == nil || evt.UserReport.EventID != "evt-current" || evt.UserReport.Email != "jane@example.com" {
			t.Fatalf("userReport = %+v, want linked feedback", evt.UserReport)
		}
	}

	projectResp := authGet(t, ts, "/api/0/projects/test-org/test-project/events/evt-current/")
	if projectResp.StatusCode != http.StatusOK {
		t.Fatalf("project event detail status = %d, want 200", projectResp.StatusCode)
	}
	var projectEvent Event
	decodeBody(t, projectResp, &projectEvent)
	assertEventDetail(t, projectEvent)

	issueResp := authGet(t, ts, "/api/0/organizations/test-org/issues/grp-api-event-parity/events/evt-current/")
	if issueResp.StatusCode != http.StatusOK {
		t.Fatalf("issue event detail status = %d, want 200", issueResp.StatusCode)
	}
	var issueEvent Event
	decodeBody(t, issueResp, &issueEvent)
	assertEventDetail(t, issueEvent)

	resolveResp := authGet(t, ts, "/api/0/organizations/test-org/eventids/evt-current/")
	if resolveResp.StatusCode != http.StatusOK {
		t.Fatalf("resolve event detail status = %d, want 200", resolveResp.StatusCode)
	}
	var resolved struct {
		Event Event `json:"event"`
	}
	decodeBody(t, resolveResp, &resolved)
	assertEventDetail(t, resolved.Event)
}

func TestAPIEventAttachments_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteGroup(t, db, "grp-api-att", "AttachmentError", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-att", "grp-api-att", "AttachmentError", "error")

	blobStore, err := store.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	attachments := sqlite.NewAttachmentStore(db, blobStore)
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-1",
		EventID:     "evt-api-att",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("crash payload")); err != nil {
		t.Fatalf("save attachment: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:          db,
		Attachments: attachments,
	})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/events/evt-api-att/attachments/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list attachments status = %d, want 200", resp.StatusCode)
	}
	defer resp.Body.Close()

	var items []Attachment
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode attachments: %v", err)
	}
	if len(items) != 1 || items[0].Name != "crash.txt" {
		t.Fatalf("unexpected attachments: %+v", items)
	}

	download := authGet(t, ts, "/api/0/events/evt-api-att/attachments/att-1/")
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download attachment status = %d, want 200", download.StatusCode)
	}
	if got := download.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("content type = %q, want text/plain", got)
	}
	body, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatalf("read attachment body: %v", err)
	}
	_ = download.Body.Close()
	if string(body) != "crash payload" {
		t.Fatalf("attachment payload = %q, want %q", string(body), "crash payload")
	}
}

func TestAPIUploadEventAttachment_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteGroup(t, db, "grp-api-up-att", "AttachmentUpload", "main.go", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-up-att", "grp-api-up-att", "AttachmentUpload", "error")

	blobStore, err := store.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	attachments := sqlite.NewAttachmentStore(db, blobStore)

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:          db,
		Attachments: attachments,
		BlobStore:   blobStore,
	})))
	defer ts.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "upload.log")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("upload payload")); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	if err := writer.WriteField("event_id", "evt-api-up-att"); err != nil {
		t.Fatalf("WriteField event_id: %v", err)
	}
	if err := writer.WriteField("content_type", "text/plain"); err != nil {
		t.Fatalf("WriteField content_type: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/attachments/", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload attachment: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload attachment status = %d, want 201", resp.StatusCode)
	}
	var created Attachment
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created attachment: %v", err)
	}
	_ = resp.Body.Close()
	if created.EventID != "evt-api-up-att" || created.Name != "upload.log" {
		t.Fatalf("unexpected created attachment: %+v", created)
	}

	list := authGet(t, ts, "/api/0/events/evt-api-up-att/attachments/")
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list attachments status = %d, want 200", list.StatusCode)
	}
	var items []Attachment
	decodeBody(t, list, &items)
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("unexpected listed attachments: %+v", items)
	}

	download := authGet(t, ts, "/api/0/events/evt-api-up-att/attachments/"+created.ID+"/")
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download attachment status = %d, want 200", download.StatusCode)
	}
	payload, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatalf("read uploaded attachment body: %v", err)
	}
	_ = download.Body.Close()
	if string(payload) != "upload payload" {
		t.Fatalf("attachment payload = %q, want %q", string(payload), "upload payload")
	}
}
