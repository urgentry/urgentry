package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestReplayIssueLookupUsesBatchStoreLookup(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('test-org', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj', 'test-org', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	insertGroup(t, db, "grp-batch-1", "ValueError: bad input", "main.go", "error", "unresolved")
	insertGroup(t, db, "grp-batch-2", "TypeError: nil pointer", "handler.go", "error", "unresolved")

	spy := &webStoreSpy{WebStore: sqlite.NewWebStore(db)}
	deps := testHandlerDeps(db, store.NewMemoryBlobStore(), dataDir, nil)
	deps.WebStore = spy
	handler := NewHandlerWithDeps(deps)
	ctx := context.WithValue(context.Background(), pageRequestStateKey{}, &pageRequestState{
		replayScopes: make(map[string]pageScopeResult),
		metrics:      make(map[string]int),
	})

	refs, err := handler.replayIssueLookup(ctx, []string{"grp-batch-1", "grp-batch-2", "grp-batch-1", ""})
	if err != nil {
		t.Fatalf("replayIssueLookup: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("replayIssueLookup len = %d, want 2", len(refs))
	}
	if spy.getIssuesCalls != 1 {
		t.Fatalf("GetIssues calls = %d, want 1", spy.getIssuesCalls)
	}
	if spy.getIssueCalls != 0 {
		t.Fatalf("GetIssue calls = %d, want 0", spy.getIssueCalls)
	}
	state := pageRequestStateFromContext(ctx)
	if state == nil || state.metrics["replay_issue_lookup.batch"] != 1 {
		t.Fatalf("replay_issue_lookup.batch = %d, want 1", state.metrics["replay_issue_lookup.batch"])
	}
}

func TestReplayAndProfilePages(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()
	blobStore := store.NewMemoryBlobStore()
	profiles := sqlite.NewProfileStore(db, blobStore)
	replays := sqlite.NewReplayStore(db, blobStore)
	attachments := sqlite.NewAttachmentStore(db, blobStore)

	now := time.Now().UTC().Format(time.RFC3339)
	profilefixtures.Save(t, profiles, "test-proj", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-web-profile-1", "profile-1"))
	profilefixtures.Save(t, profiles, "test-proj", profilefixtures.CPUHeavy().Spec().
		WithIDs("evt-web-profile-2", "profile-2").
		WithTrace("fedcba9876543210fedcba9876543210").
		WithRelease("backend@1.2.4").
		WithTimestamp(time.Date(2026, time.March, 29, 10, 2, 0, 0, time.UTC)).
		WithDuration(32000000))
	if _, err := db.Exec(`UPDATE events SET payload_json = '' WHERE project_id = 'test-proj' AND event_id = 'evt-web-profile-1'`); err != nil {
		t.Fatalf("clear profile payload_json: %v", err)
	}
	insertGroup(t, db, "grp-web-profile-1", "CheckoutFailure", "checkout.go in checkout", "error", "unresolved")
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES
			('evt-web-issue-1', 'test-proj', 'evt-web-issue-1', 'grp-web-profile-1', 'backend@1.2.3', 'production', 'go', 'error', 'error', 'CheckoutFailure', 'boom', 'checkout.go in checkout', ?, '{"trace_id":"0123456789abcdef0123456789abcdef"}', '{"contexts":{"trace":{"trace_id":"0123456789abcdef0123456789abcdef"}}}', 'dev@example.com')`,
		now,
	); err != nil {
		t.Fatalf("insert linked issue event: %v", err)
	}

	replayPayload := []byte(`{
		"event_id":"evt-web-replay-1",
		"replay_id":"replay-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"},
		"user":{"email":"dev@example.com"},
		"contexts":{"trace":{"trace_id":"0123456789abcdef0123456789abcdef"}}
	}`)
	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj", "evt-web-replay-1", replayPayload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	recording := []byte(`{
		"events":[
			{"type":"snapshot","offset_ms":0,"data":{"snapshot_id":"snap-web-1"}},
			{"type":"navigation","offset_ms":100,"data":{"url":"https://app.example.com/checkout?step=1","title":"Checkout"}},
			{"type":"console","offset_ms":200,"data":{"level":"error","message":"boom"}},
			{"type":"network","offset_ms":300,"data":{"method":"POST","url":"https://api.example.com/pay","status_code":500,"duration_ms":182}},
			{"type":"click","offset_ms":420,"data":{"selector":"button.pay","text":"Pay now"}},
			{"type":"error","offset_ms":430,"data":{"event_id":"evt-web-issue-1","trace_id":"0123456789abcdef0123456789abcdef","message":"Payment failed"}}
		]
	}`)
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-web-replay-1",
		EventID:     "evt-web-replay-1",
		ProjectID:   "test-proj",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, recording); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj", "replay-1"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}
	replayRecord, err := replays.GetReplay(t.Context(), "test-proj", "replay-1")
	if err != nil {
		t.Fatalf("GetReplay: %v", err)
	}
	errorAnchor := ""
	for _, item := range replayRecord.Timeline {
		if item.Kind == "error" {
			errorAnchor = item.ID
			break
		}
	}
	if errorAnchor == "" {
		t.Fatal("expected replay error anchor")
	}

	resp, err := http.Get(srv.URL + "/replays/")
	if err != nil {
		t.Fatalf("GET /replays/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replays status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Replays") || !strings.Contains(body, "https://app.example.com/checkout") {
		t.Fatalf("unexpected replay list body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/replays/replay-1/?pane=errors&anchor=" + url.QueryEscape(errorAnchor) + "&ts=430")
	if err != nil {
		t.Fatalf("GET /replays/replay-1/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Replay Player") ||
		!strings.Contains(body, "replayScrubber") ||
		!strings.Contains(body, "Payment failed") ||
		!strings.Contains(body, "/issues/grp-web-profile-1/") ||
		!strings.Contains(body, "/traces/0123456789abcdef0123456789abcdef/") ||
		!strings.Contains(body, "pane=errors") {
		t.Fatalf("unexpected replay detail body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/profiles/")
	if err != nil {
		t.Fatalf("GET /profiles/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profiles status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Profiles") || !strings.Contains(body, "checkout") {
		t.Fatalf("unexpected profile list body: %s", body)
	}
	resp, err = http.Get(srv.URL + "/profiles/?transaction=checkout&release=backend@1.2.3&environment=production&start=2026-03-29T09:00:00Z&end=2026-03-29T11:00:00Z")
	if err != nil {
		t.Fatalf("GET filtered /profiles/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filtered profiles status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "name=\"transaction\"") || !strings.Contains(body, "backend@1.2.3") {
		t.Fatalf("unexpected filtered profile list body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/profiles/profile-1/?frame=dbQuery&compare=profile-2&max_depth=2&max_nodes=128")
	if err != nil {
		t.Fatalf("GET /profiles/profile-1/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Query Controls") ||
		!strings.Contains(body, "Top-Down Call Tree") ||
		!strings.Contains(body, "Bottom-Up Hotspots") ||
		!strings.Contains(body, "Flamegraph") ||
		!strings.Contains(body, "Hot Path") ||
		!strings.Contains(body, "Top Regressions") ||
		!strings.Contains(body, "/releases/backend@1.2.3/") ||
		!strings.Contains(body, "/issues/grp-web-profile-1/") ||
		!strings.Contains(body, "/traces/0123456789abcdef0123456789abcdef/") ||
		!strings.Contains(body, "dbQuery @ db.go:12") {
		t.Fatalf("unexpected profile detail body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/traces/0123456789abcdef0123456789abcdef/")
	if err != nil {
		t.Fatalf("GET /traces/0123456789abcdef0123456789abcdef/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trace detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Related Profiles") ||
		!strings.Contains(body, "/profiles/profile-1/") ||
		!strings.Contains(body, "profile-1") {
		t.Fatalf("unexpected trace detail body: %s", body)
	}
}

func TestReplayDetailPageUsesLoadedReplayTimeline(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('test-org', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj', 'test-org', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	blobs := store.NewMemoryBlobStore()
	replays := sqlite.NewReplayStore(db, blobs)
	attachments := sqlite.NewAttachmentStore(db, blobs)
	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj", "evt-replay-spy-1", []byte(`{
		"event_id":"evt-replay-spy-1",
		"replay_id":"replay-spy-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"}
	}`)); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-replay-spy-1",
		EventID:     "evt-replay-spy-1",
		ProjectID:   "test-proj",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, []byte(`{"events":[{"type":"console","offset_ms":120,"data":{"level":"error","message":"boom"}}]}`)); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj", "replay-spy-1"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	spy := &replayReadStoreSpy{
		base:               replays,
		failOnTimelineList: true,
	}
	deps := testHandlerDeps(db, blobs, dataDir, nil)
	deps.Replays = spy
	handler := NewHandlerWithDeps(deps)
	req := httptest.NewRequest(http.MethodGet, "/replays/replay-spy-1/", nil)
	req.SetPathValue("id", "replay-spy-1")
	rr := httptest.NewRecorder()

	handler.replayDetailPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	if spy.listTimelineCalls != 0 {
		t.Fatalf("ListReplayTimeline calls = %d, want 0", spy.listTimelineCalls)
	}
	if !strings.Contains(rr.Body.String(), "Replay Player") {
		t.Fatalf("expected replay detail body, got %s", rr.Body.String())
	}
}

func TestReplayDetailPageShowsPartialManifest(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	replays := sqlite.NewReplayStore(db, store.NewMemoryBlobStore())
	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj", "evt-web-replay-partial", []byte(`{
		"event_id":"evt-web-replay-partial",
		"replay_id":"replay-partial",
		"timestamp":"2026-03-29T12:00:00Z",
		"request":{"url":"https://app.example.com/settings"}
	}`)); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj", "replay-partial"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	resp, err := http.Get(srv.URL + "/replays/replay-partial/")
	if err != nil {
		t.Fatalf("GET /replays/replay-partial/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "partial") || !strings.Contains(body, "recording not uploaded") {
		t.Fatalf("unexpected partial replay body: %s", body)
	}
}
