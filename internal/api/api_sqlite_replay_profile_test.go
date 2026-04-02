package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestAPIReplaysAndProfiles_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobStore := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobStore)
	profileStore := sqlite.NewProfileStore(db, blobStore)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES
			('evt-api-replay-1', 'test-proj-id', 'evt-api-replay-1', NULL, 'web@1.2.3', 'production', 'javascript', 'info', 'replay', 'Replay of https://app.example.com/checkout', 'Session replay', 'https://app.example.com/checkout', ?, '{"replay_id":"replay-1"}', '{"replay_id":"replay-1","request":{"url":"https://app.example.com/checkout"},"user":{"email":"dev@example.com"}}', 'dev@example.com')`,
		now,
	); err != nil {
		t.Fatalf("insert replay/profile events: %v", err)
	}
	profilefixtures.Save(t, profileStore, "test-proj-id", profilefixtures.SaveRead().Spec().WithIDs("evt-api-profile-1", "profile-1"))
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-api-replay-1",
		EventID:     "evt-api-replay-1",
		ProjectID:   "test-proj-id",
		Name:        "replay-recording.json",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, []byte(`{"segments":[1,2,3]}`)); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := sqlite.NewReplayStore(db, blobStore).IndexReplay(t.Context(), "test-proj-id", "replay-1"); err != nil {
		t.Fatalf("IndexReplay(detail): %v", err)
	}
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-api-replay-extra",
		EventID:     "evt-api-replay-1",
		ProjectID:   "test-proj-id",
		Name:        "notes.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("not a replay asset")); err != nil {
		t.Fatalf("SaveAttachment(extra): %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET payload_json = '' WHERE project_id = 'test-proj-id' AND event_id = 'evt-api-profile-1'`); err != nil {
		t.Fatalf("clear profile payload_json: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		Attachments: attachments,
		BlobStore:   blobStore,
	})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/replays/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replays status = %d, want 200", resp.StatusCode)
	}
	var replays []Replay
	decodeBody(t, resp, &replays)
	if len(replays) != 1 || replays[0].ID != "replay-1" || replays[0].User == nil || replays[0].User.Email != "dev@example.com" || len(replays[0].URLs) == 0 || replays[0].URLs[0] != "https://app.example.com/checkout" {
		t.Fatalf("unexpected replays: %+v", replays)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-1/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay detail status = %d, want 200", resp.StatusCode)
	}
	var replay Replay
	decodeBody(t, resp, &replay)
	if replay.ID != "replay-1" || len(replay.Payload) == 0 || replay.CountSegments != 1 || len(replay.Attachments) != 1 {
		t.Fatalf("unexpected replay detail: %+v", replay)
	}
	if replay.Attachments[0].ID != "att-api-replay-1" {
		t.Fatalf("unexpected replay attachment inventory: %+v", replay.Attachments)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profiles status = %d, want 200", resp.StatusCode)
	}
	var profiles []Profile
	decodeBody(t, resp, &profiles)
	if len(profiles) != 1 || profiles[0].ProfileID != "profile-1" || profiles[0].TraceID != "0123456789abcdef0123456789abcdef" || profiles[0].Summary.SampleCount != 6 {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/profile-1/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile detail status = %d, want 200", resp.StatusCode)
	}
	var profile Profile
	decodeBody(t, resp, &profile)
	if profile.ProfileID != "profile-1" || profile.DurationNS != "25000000" || len(profile.Payload) == 0 || len(profile.Summary.TopFrames) == 0 || len(profile.Summary.TopFunctions) == 0 {
		t.Fatalf("unexpected profile detail: %+v", profile)
	}
}

func TestAPIReplayPlaybackEndpoints_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobStore := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobStore)
	replays := sqlite.NewReplayStore(db, blobStore)
	events := sqlite.NewEventStore(db)

	payload := []byte(`{
		"event_id":"evt-api-replay-playback",
		"replay_id":"replay-playback",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"},
		"user":{"email":"dev@example.com"},
		"contexts":{"trace":{"trace_id":"trace-api-1"}}
	}`)
	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj-id", "evt-api-replay-playback", payload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := events.SaveEvent(t.Context(), &store.StoredEvent{
		ID:             "evt-api-linked-row",
		ProjectID:      "test-proj-id",
		EventID:        "evt-api-linked",
		GroupID:        "grp-api-linked",
		EventType:      "error",
		Platform:       "javascript",
		Level:          "error",
		OccurredAt:     time.Now().UTC(),
		IngestedAt:     time.Now().UTC(),
		NormalizedJSON: json.RawMessage(`{"event_id":"evt-api-linked"}`),
	}); err != nil {
		t.Fatalf("SaveEvent linked: %v", err)
	}
	recording := []byte(`{
		"events":[
			{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout?step=1"}},
			{"type":"network","offset_ms":60,"data":{"method":"POST","url":"https://api.example.com/pay","status_code":500,"duration_ms":182}},
			{"type":"error","offset_ms":120,"data":{"event_id":"evt-api-linked","trace_id":"trace-api-1","message":"Payment failed"}}
		]
	}`)
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-api-replay-playback",
		EventID:     "evt-api-replay-playback",
		ProjectID:   "test-proj-id",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, recording); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj-id", "replay-playback"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		Attachments: attachments,
		BlobStore:   blobStore,
	})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/manifest/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", resp.StatusCode)
	}
	var manifest ReplayPlaybackManifest
	decodeBody(t, resp, &manifest)
	if manifest.ReplayID != "replay-playback" || manifest.ProcessingStatus != "ready" || manifest.Counts.Navigation != 1 || manifest.Counts.Network != 1 || manifest.Counts.ErrorMarks != 1 || len(manifest.Assets) != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.Assets[0].DownloadURL == "" || !strings.Contains(manifest.Assets[0].DownloadURL, "/replays/replay-playback/assets/att-api-replay-playback/") {
		t.Fatalf("unexpected download url: %+v", manifest.Assets[0])
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/timeline/?limit=2&start_ms=0&end_ms=120")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d, want 200", resp.StatusCode)
	}
	var timeline ReplayTimelinePage
	decodeBody(t, resp, &timeline)
	if len(timeline.Items) != 2 || !timeline.HasMore || timeline.NextStartMS == 0 {
		t.Fatalf("unexpected timeline page: %+v", timeline)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/panes/errors/?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("errors pane status = %d, want 200", resp.StatusCode)
	}
	decodeBody(t, resp, &timeline)
	if len(timeline.Items) != 1 || timeline.Items[0].Anchor == "" || timeline.Items[0].Anchor != timeline.Items[0].ID || timeline.Items[0].LinkedIssueID != "grp-api-linked" {
		t.Fatalf("unexpected errors pane: %+v", timeline)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/recording-segments/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording segments status = %d, want 200", resp.StatusCode)
	}
	var segments []ReplayRecordingSegment
	decodeBody(t, resp, &segments)
	if len(segments) != 1 || segments[0].ReplayID != "replay-playback" {
		t.Fatalf("unexpected recording segments: %+v", segments)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/recording-segments/"+segments[0].ID+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording segment detail status = %d, want 200", resp.StatusCode)
	}
	var segment ReplayRecordingSegment
	decodeBody(t, resp, &segment)
	if segment.ID != segments[0].ID || segment.ReplayID != "replay-playback" {
		t.Fatalf("unexpected recording segment detail: %+v", segment)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/viewed-by/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewed-by status = %d, want 200", resp.StatusCode)
	}
	var viewers []ReplayViewer
	decodeBody(t, resp, &viewers)
	if len(viewers) != 0 {
		t.Fatalf("unexpected replay viewers: %+v", viewers)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/assets/att-api-replay-playback/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(recording) {
		t.Fatalf("unexpected replay asset body: %s", body)
	}

	if _, err := db.Exec(`DELETE FROM event_attachments WHERE id = 'att-api-replay-playback'`); err != nil {
		t.Fatalf("delete attachment row: %v", err)
	}
	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-playback/assets/att-api-replay-playback/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", resp.StatusCode)
	}

	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj-id", "evt-api-replay-expired", []byte(`{"event_id":"evt-api-replay-expired","replay_id":"replay-expired","timestamp":"2026-03-29T12:00:00Z"}`)); err != nil {
		t.Fatalf("SaveEnvelopeReplay(expired): %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj-id", "replay-expired"); err != nil {
		t.Fatalf("IndexReplay(expired): %v", err)
	}
	if _, err := db.Exec(`DELETE FROM events WHERE event_id = 'evt-api-replay-expired'`); err != nil {
		t.Fatalf("delete expired replay: %v", err)
	}
	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-expired/manifest/")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired manifest status = %d, want 404", resp.StatusCode)
	}
}

func TestAPIReplayDeletion_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobStore := store.NewMemoryBlobStore()
	replays := sqlite.NewReplayStore(db, blobStore)

	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj-id", "evt-api-replay-delete", []byte(`{
		"event_id":"evt-api-replay-delete",
		"replay_id":"replay-delete",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production"
	}`)); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj-id", "replay-delete"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	del := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/replays/replay-delete/", pat, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete replay status = %d, want 204", del.StatusCode)
	}
	del.Body.Close()

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/replays/replay-delete/", pat, nil)
	if get.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted replay status = %d, want 404", get.StatusCode)
	}
	get.Body.Close()
}

func TestAPIProfileQueryViews_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobStore := store.NewMemoryBlobStore()
	profileStore := sqlite.NewProfileStore(db, blobStore)

	saveProfileQueryFixture(t, profileStore, "test-proj-id", profilefixtures.IOHeavy().Spec().WithIDs("evt-api-profile-query-a", "profile-query-a"))
	saveProfileQueryFixture(t, profileStore, "test-proj-id", profilefixtures.CPUHeavy().Spec().WithIDs("evt-api-profile-query-b", "profile-query-b"))
	saveProfileQueryFixture(t, profileStore, "test-proj-id", profilefixtures.MalformedEmpty().Spec().WithIDs("evt-api-profile-query-invalid", "profile-query-invalid").WithTransaction("broken"))

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		BlobStore: blobStore,
	})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/top-down/?profile_id=profile-query-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("top-down status = %d, want 200", resp.StatusCode)
	}
	var topDownJSON map[string]any
	decodeBody(t, resp, &topDownJSON)
	if _, ok := topDownJSON["profile_id"]; !ok {
		t.Fatalf("top-down response missing snake_case profile_id: %+v", topDownJSON)
	}
	if _, ok := topDownJSON["total_weight"]; !ok {
		t.Fatalf("top-down response missing snake_case total_weight: %+v", topDownJSON)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/top-down/?profile_id=profile-query-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("top-down status = %d, want 200", resp.StatusCode)
	}
	var topDown ProfileTree
	decodeBody(t, resp, &topDown)
	if topDown.TotalWeight != 8 || len(topDown.Root.Children) != 1 || topDown.Root.Children[0].Name != "rootHandler @ app.go:1" {
		t.Fatalf("unexpected top-down response: %+v", topDown)
	}

	filtered := authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/top-down/?transaction=checkout&release=backend@1.2.3&environment=production&start=2026-03-29T09:00:00Z&end=2026-03-29T11:00:00Z")
	if filtered.StatusCode != http.StatusOK {
		t.Fatalf("filtered top-down status = %d, want 200", filtered.StatusCode)
	}
	filtered.Body.Close()

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/bottom-up/?profile_id=profile-query-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bottom-up status = %d, want 200", resp.StatusCode)
	}
	var bottomUp ProfileTree
	decodeBody(t, resp, &bottomUp)
	if bottomUp.TotalWeight != 8 || len(bottomUp.Root.Children) != 2 || bottomUp.Root.Children[0].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected bottom-up response: %+v", bottomUp)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/flamegraph/?profile_id=profile-query-a&max_depth=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flamegraph status = %d, want 200", resp.StatusCode)
	}
	var flamegraph ProfileTree
	decodeBody(t, resp, &flamegraph)
	if !flamegraph.Truncated {
		t.Fatalf("expected truncated flamegraph: %+v", flamegraph)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/hot-path/?profile_id=profile-query-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hot-path status = %d, want 200", resp.StatusCode)
	}
	var hotPath ProfileHotPath
	decodeBody(t, resp, &hotPath)
	if len(hotPath.Frames) != 3 || hotPath.Frames[2].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected hot path response: %+v", hotPath)
	}

	guarded := authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/top-down/?profile_id=profile-query-a&max_nodes=3000")
	if guarded.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("guarded top-down status = %d, want 429", guarded.StatusCode)
	}
	guardedBody := decodeAPIError(t, guarded)
	if guardedBody.Code != "query_guard_blocked" {
		t.Fatalf("guarded error body = %+v, want query_guard_blocked", guardedBody)
	}

	notReady := authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/top-down/?profile_id=profile-query-invalid")
	if notReady.StatusCode != http.StatusConflict {
		t.Fatalf("not-ready top-down status = %d, want 409", notReady.StatusCode)
	}
	notReadyBody := decodeAPIError(t, notReady)
	if notReadyBody.Code != "profile_not_query_ready" {
		t.Fatalf("not-ready error body = %+v, want profile_not_query_ready", notReadyBody)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/compare/?baseline=profile-query-a&candidate=profile-query-b")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compare status = %d, want 200", resp.StatusCode)
	}
	var comparisonJSON map[string]any
	decodeBody(t, resp, &comparisonJSON)
	if _, ok := comparisonJSON["baseline_profile_id"]; !ok {
		t.Fatalf("comparison response missing snake_case baseline_profile_id: %+v", comparisonJSON)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/profiles/compare/?baseline=profile-query-a&candidate=profile-query-b")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compare status = %d, want 200", resp.StatusCode)
	}
	var comparison ProfileComparison
	decodeBody(t, resp, &comparison)
	if comparison.DurationDeltaNS != 3000000 || len(comparison.TopRegressions) == 0 || comparison.TopRegressions[0].Name != "scoreRules" {
		t.Fatalf("unexpected comparison response: %+v", comparison)
	}
}

func TestAPIReplayQueryCostGuard(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES
			('evt-api-replay-guard-1', 'test-proj-id', 'evt-api-replay-guard-1', 'web@1.2.3', 'production', 'javascript', 'info', 'replay', 'Replay of https://app.example.com/checkout', 'Session replay', 'https://app.example.com/checkout', ?, '{"replay_id":"replay-guard-1"}', '{"replay_id":"replay-guard-1"}', 'dev@example.com')`,
		now,
	); err != nil {
		t.Fatalf("insert replay event: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('test-org-id', ?, 50, 10, 500, 3600)`,
		string(sqlite.QueryWorkloadReplays),
	); err != nil {
		t.Fatalf("insert replay query guard policy: %v", err)
	}

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/replays/", pat, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("replays status = %d, want 429", resp.StatusCode)
	}
	body := decodeAPIError(t, resp)
	if body.Code != "query_guard_blocked" {
		t.Fatalf("error body = %+v, want query_guard_blocked", body)
	}
}
