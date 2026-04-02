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
