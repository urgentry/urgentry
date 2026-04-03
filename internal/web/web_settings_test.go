package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestSettingsAndReleaseDetailPages(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()
	profiles := sqlite.NewProfileStore(db, store.NewMemoryBlobStore())

	if _, err := sqlite.NewOwnershipStore(db).CreateRule(t.Context(), store.OwnershipRule{
		ProjectID: "test-proj",
		Name:      "Payments",
		Pattern:   "path:payments.go",
		Assignee:  "payments@team",
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if _, err := sqlite.NewReleaseStore(db).CreateRelease(t.Context(), "test-org", "backend@1.2.3"); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if _, err := sqlite.NewReleaseStore(db).CreateRelease(t.Context(), "test-org", "backend@1.2.2"); err != nil {
		t.Fatalf("CreateRelease previous: %v", err)
	}
	previousCreatedAt := time.Now().UTC().Add(-48 * time.Hour)
	currentCreatedAt := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := db.Exec(`UPDATE releases SET created_at = ? WHERE organization_id = 'test-org' AND version = 'backend@1.2.2'`, previousCreatedAt.Format(time.RFC3339)); err != nil {
		t.Fatalf("update previous release created_at: %v", err)
	}
	if _, err := db.Exec(`UPDATE releases SET created_at = ? WHERE organization_id = 'test-org' AND version = 'backend@1.2.3'`, currentCreatedAt.Format(time.RFC3339)); err != nil {
		t.Fatalf("update current release created_at: %v", err)
	}
	deployFinishedAt := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := sqlite.NewReleaseStore(db).AddDeploy(t.Context(), "test-org", "backend@1.2.3", store.ReleaseDeploy{
		Environment:  "production",
		Name:         "deploy-123",
		DateFinished: deployFinishedAt,
	}); err != nil {
		t.Fatalf("AddDeploy: %v", err)
	}
	if _, err := sqlite.NewReleaseStore(db).AddCommit(t.Context(), "test-org", "backend@1.2.3", store.ReleaseCommit{
		CommitSHA: "abc123def456",
		Message:   "Fix checkout crash",
		Files:     []string{"payments.go"},
	}); err != nil {
		t.Fatalf("AddCommit: %v", err)
	}
	insertGroup(t, db, "grp-release-web-1", "CheckoutError", "payments.go in charge", "error", "unresolved")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, processing_status, ingest_error)
		 VALUES
			('evt-release-web-0', 'test-proj', 'evt-release-web-0', 'grp-release-web-1', 'backend@1.2.2', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'payments.go in charge', ?, '{}', '{"event_id":"evt-release-web-0","release":"backend@1.2.2"}', 'completed', ''),
			('evt-release-web-1', 'test-proj', 'evt-release-web-1', 'grp-release-web-1', 'backend@1.2.3', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'payments.go in charge', ?, '{}', '{"event_id":"evt-release-web-1","release":"backend@1.2.3","exception":{"values":[{"stacktrace":{"frames":[{"instruction_addr":"0x1000"}]}}]}}', 'failed', 'missing debug symbols'),
			('evt-release-web-2', 'test-proj', 'evt-release-web-2', 'grp-release-web-1', 'backend@1.2.3', 'production', 'go', 'error', 'error', 'CheckoutError', 'boom', 'payments.go in charge', ?, '{}', '{"event_id":"evt-release-web-2","release":"backend@1.2.3"}', 'completed', ''),
			('evt-release-web-3', 'test-proj', 'evt-release-web-3', 'grp-release-web-1', 'backend@1.2.3', 'staging', 'go', 'error', 'error', 'CheckoutError', 'boom', 'payments.go in charge', ?, '{}', '{"event_id":"evt-release-web-3","release":"backend@1.2.3"}', 'completed', '')`,
		previousCreatedAt.Add(2*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(-2*time.Hour).Format(time.RFC3339),
		deployFinishedAt.Add(45*time.Minute).Format(time.RFC3339),
		deployFinishedAt.Add(90*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert release web event: %v", err)
	}
	health := sqlite.NewReleaseHealthStore(db)
	for _, session := range []*sqlite.ReleaseSession{
		{ProjectID: "test-proj", Release: "backend@1.2.2", Status: "ok", Quantity: 4, DistinctID: "user-1", DateCreated: previousCreatedAt.Add(30 * time.Minute)},
		{ProjectID: "test-proj", Release: "backend@1.2.2", Status: "errored", Quantity: 1, DistinctID: "user-2", DateCreated: previousCreatedAt.Add(45 * time.Minute)},
		{ProjectID: "test-proj", Release: "backend@1.2.3", Status: "ok", Quantity: 5, DistinctID: "user-1", DateCreated: currentCreatedAt.Add(30 * time.Minute)},
		{ProjectID: "test-proj", Release: "backend@1.2.3", Status: "errored", Quantity: 2, DistinctID: "user-3", DateCreated: currentCreatedAt.Add(45 * time.Minute)},
		{ProjectID: "test-proj", Release: "backend@1.2.3", Status: "crashed", Quantity: 1, DistinctID: "user-4", DateCreated: currentCreatedAt.Add(50 * time.Minute)},
	} {
		if err := health.SaveSession(t.Context(), session); err != nil {
			t.Fatalf("SaveSession(%s): %v", session.Release, err)
		}
	}
	traces := sqlite.NewTraceStore(db)
	for _, txn := range []*store.StoredTransaction{
		{ProjectID: "test-proj", EventID: "txn-release-prev-1", TraceID: "trace-release-prev-1", SpanID: "span-release-prev-1", Transaction: "checkout", Environment: "production", ReleaseID: "backend@1.2.2", StartTimestamp: previousCreatedAt.Add(4 * time.Hour), EndTimestamp: previousCreatedAt.Add(4*time.Hour + 210*time.Millisecond), DurationMS: 210},
		{ProjectID: "test-proj", EventID: "txn-release-prev-2", TraceID: "trace-release-prev-2", SpanID: "span-release-prev-2", Transaction: "checkout", Environment: "production", ReleaseID: "backend@1.2.2", StartTimestamp: previousCreatedAt.Add(5 * time.Hour), EndTimestamp: previousCreatedAt.Add(5*time.Hour + 240*time.Millisecond), DurationMS: 240},
		{ProjectID: "test-proj", EventID: "txn-release-cur-1", TraceID: "trace-release-cur-1", SpanID: "span-release-cur-1", Transaction: "checkout", Environment: "production", ReleaseID: "backend@1.2.3", StartTimestamp: deployFinishedAt.Add(-90 * time.Minute), EndTimestamp: deployFinishedAt.Add(-90*time.Minute + 170*time.Millisecond), DurationMS: 170},
		{ProjectID: "test-proj", EventID: "txn-release-cur-2", TraceID: "trace-release-cur-2", SpanID: "span-release-cur-2", Transaction: "checkout", Environment: "production", ReleaseID: "backend@1.2.3", StartTimestamp: deployFinishedAt.Add(45 * time.Minute), EndTimestamp: deployFinishedAt.Add(45*time.Minute + 390*time.Millisecond), DurationMS: 390},
		{ProjectID: "test-proj", EventID: "txn-release-cur-3", TraceID: "trace-release-cur-3", SpanID: "span-release-cur-3", Transaction: "checkout", Environment: "production", ReleaseID: "backend@1.2.3", StartTimestamp: deployFinishedAt.Add(90 * time.Minute), EndTimestamp: deployFinishedAt.Add(90*time.Minute + 430*time.Millisecond), DurationMS: 430},
		{ProjectID: "test-proj", EventID: "txn-release-cur-4", TraceID: "trace-release-cur-4", SpanID: "span-release-cur-4", Transaction: "search", Environment: "staging", ReleaseID: "backend@1.2.3", StartTimestamp: deployFinishedAt.Add(2 * time.Hour), EndTimestamp: deployFinishedAt.Add(2*time.Hour + 110*time.Millisecond), DurationMS: 110},
	} {
		if err := traces.SaveTransaction(t.Context(), txn); err != nil {
			t.Fatalf("SaveTransaction(%s): %v", txn.EventID, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, debug_file_id, cursor_rowid, total_items, processed_items, updated_items, failed_items, requested_via, last_error, created_at, updated_at)
		 VALUES
			('run-release-web-1', 'native_reprocess', 'failed', 'test-org', 'test-proj', 'backend@1.2.3', '', 0, 1, 1, 0, 1, 'test', 'missing debug symbols', ?, ?)`,
		now, now,
	); err != nil {
		t.Fatalf("insert native backfill run: %v", err)
	}
	profilefixtures.Save(t, profiles, "test-proj", profilefixtures.DBHeavy().Spec().
		WithIDs("evt-release-profile-1", "release-profile-1").
		WithTrace("trace-release-profile-1").
		WithDuration(42000000))

	resp, err := http.Get(srv.URL + "/settings/")
	if err != nil {
		t.Fatalf("GET /settings/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Ownership Rules") || !strings.Contains(body, "payments@team") {
		t.Fatalf("unexpected settings body: %s", body)
	}

	resp, err = http.Get(srv.URL + "/releases/backend@1.2.3/")
	if err != nil {
		t.Fatalf("GET /releases/backend@1.2.3/: %v", err)
	}
	body = getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release detail status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Suspect Issues") ||
		!strings.Contains(body, "Release Comparison") ||
		!strings.Contains(body, "Compared with backend@1.2.2") ||
		!strings.Contains(body, "Environment Error Movement") ||
		!strings.Contains(body, "Transaction Latency Movement") ||
		!strings.Contains(body, "Latest Deploy Impact") ||
		!strings.Contains(body, "Profile Highlights") ||
		!strings.Contains(body, "Native Processing") ||
		!strings.Contains(body, "Reprocess native events") ||
		!strings.Contains(body, "missing debug symbols") ||
		!strings.Contains(body, "release-profile-1") ||
		!strings.Contains(body, "dbQuery") ||
		!strings.Contains(body, "payments.go") ||
		!strings.Contains(body, "abc123def456") ||
		!strings.Contains(body, "production") ||
		!strings.Contains(body, "checkout") {
		t.Fatalf("unexpected release detail body: %s", body)
	}
}

func TestCreateReleaseNativeReprocessRequiresSessionCSRF(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServer(t)
	defer srv.Close()

	releaseStore := sqlite.NewReleaseStore(db)
	if _, err := releaseStore.CreateRelease(t.Context(), "test-org", "backend@2.0.0"); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	form := url.Values{"project_id": {"test-proj"}}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/releases/backend@2.0.0/native/reprocess", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest without csrf: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST native reprocess without csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status without csrf = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/releases/backend@2.0.0/native/reprocess", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest with csrf: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST native reprocess with csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status with csrf = %d, want 303", resp.StatusCode)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backfill_runs WHERE organization_id = 'test-org' AND project_id = 'test-proj' AND release_version = 'backend@2.0.0' AND kind = 'native_reprocess'`).Scan(&count); err != nil {
		t.Fatalf("count native reprocess runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("native reprocess run count = %d, want 1", count)
	}
}

func TestReleasesPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO releases (id, organization_id, version, created_at) VALUES ('rel-web-1', 'test-org', 'ios@1.2.3', ?)`, now); err != nil {
		t.Fatalf("seed release: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO release_sessions (id, project_id, release_version, status, created_at) VALUES ('sess-web-1', 'test-proj', 'ios@1.2.3', 'ok', ?)`, now); err != nil {
		t.Fatalf("seed release session: %v", err)
	}

	resp, err := http.Get(srv.URL + "/releases/")
	if err != nil {
		t.Fatalf("GET /releases/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Crash-Free") || !strings.Contains(body, "100.0%") {
		t.Fatalf("expected release health data in page body: %s", body)
	}
}

func TestSettingsPage(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	// Seed a project and key so the settings page has DSN to display.
	if _, err := db.Exec(`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('org-1', 'urgentry-org', 'Urgentry')`); err != nil {
		t.Fatalf("seed settings org: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'default', 'Default Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed settings project: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label) VALUES ('key-1', 'proj-1', 'abc123testkey', 'active', 'Default')`); err != nil {
		t.Fatalf("seed settings key: %v", err)
	}

	resp, err := http.Get(srv.URL + "/settings/")
	if err != nil {
		t.Fatalf("GET /settings/: %v", err)
	}
	body := getBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Settings") && !strings.Contains(body, "settings") {
		t.Error("expected body to contain 'Settings'")
	}
	if !strings.Contains(body, "abc123testkey") {
		t.Error("expected DSN key in settings page")
	}
}

func TestUpdateProjectSettings(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	form := url.Values{
		"name":                               {"Renamed Project"},
		"platform":                           {"python"},
		"status":                             {"disabled"},
		"event_retention_days":               {"14"},
		"attachment_retention_days":          {"7"},
		"debug_retention_days":               {"30"},
		"telemetry_errors_days":              {"14"},
		"telemetry_errors_tier":              {"delete"},
		"telemetry_logs_days":                {"14"},
		"telemetry_logs_tier":                {"delete"},
		"telemetry_traces_days":              {"14"},
		"telemetry_traces_tier":              {"delete"},
		"telemetry_replays_days":             {"7"},
		"telemetry_replays_tier":             {"archive"},
		"telemetry_replays_archive_days":     {"21"},
		"telemetry_profiles_days":            {"14"},
		"telemetry_profiles_tier":            {"delete"},
		"telemetry_outcomes_days":            {"14"},
		"telemetry_outcomes_tier":            {"delete"},
		"telemetry_attachments_days":         {"7"},
		"telemetry_attachments_tier":         {"delete"},
		"telemetry_debug_files_days":         {"30"},
		"telemetry_debug_files_tier":         {"archive"},
		"telemetry_debug_files_archive_days": {"60"},
		"replay_sample_rate":                 {"0.25"},
		"replay_max_bytes":                   {"4096"},
		"replay_scrub_fields":                {"email, token"},
		"replay_scrub_selectors":             {".secret"},
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+"/settings/project", form)
	if err != nil {
		t.Fatalf("POST /settings/project: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	var name, platform, status string
	var eventDays, attachmentDays, debugDays int
	if err := db.QueryRow(`SELECT name, platform, status, event_retention_days, attachment_retention_days, debug_file_retention_days FROM projects WHERE id = 'test-proj'`).Scan(&name, &platform, &status, &eventDays, &attachmentDays, &debugDays); err != nil {
		t.Fatalf("query updated project: %v", err)
	}
	if name != "Renamed Project" || platform != "python" || status != "disabled" || eventDays != 14 || attachmentDays != 7 || debugDays != 30 {
		t.Fatalf("unexpected project settings: name=%q platform=%q status=%q event=%d attachment=%d debug=%d", name, platform, status, eventDays, attachmentDays, debugDays)
	}
	var replayDays, replayArchiveDays int
	var replayTier string
	if err := db.QueryRow(`SELECT retention_days, storage_tier, archive_retention_days FROM telemetry_retention_policies WHERE project_id = 'test-proj' AND surface = 'replays'`).Scan(&replayDays, &replayTier, &replayArchiveDays); err != nil {
		t.Fatalf("query replay telemetry policy: %v", err)
	}
	if replayDays != 7 || replayTier != "archive" || replayArchiveDays != 21 {
		t.Fatalf("unexpected replay telemetry policy: days=%d tier=%q archive=%d", replayDays, replayTier, replayArchiveDays)
	}
	var sampleRate float64
	var maxBytes int64
	var scrubFieldsJSON, scrubSelectorsJSON string
	if err := db.QueryRow(`SELECT sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json FROM project_replay_configs WHERE project_id = 'test-proj'`).Scan(&sampleRate, &maxBytes, &scrubFieldsJSON, &scrubSelectorsJSON); err != nil {
		t.Fatalf("query replay ingest policy: %v", err)
	}
	if sampleRate != 0.25 || maxBytes != 4096 || !strings.Contains(scrubFieldsJSON, "email") || !strings.Contains(scrubSelectorsJSON, ".secret") {
		t.Fatalf("unexpected replay ingest config: rate=%v max=%d fields=%s selectors=%s", sampleRate, maxBytes, scrubFieldsJSON, scrubSelectorsJSON)
	}
}

func TestUpdateProjectSettingsRejectsInvalidReplayPolicy(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	form := url.Values{
		"name":               {"Renamed Project"},
		"platform":           {"python"},
		"status":             {"active"},
		"replay_sample_rate": {"2"},
	}
	resp, err := http.PostForm(srv.URL+"/settings/project", form)
	if err != nil {
		t.Fatalf("POST /settings/project: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "sampleRate must be between 0 and 1") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestUpdateProjectSettingsRequiresSessionCSRF(t *testing.T) {
	srv, db, sessionToken, csrf := setupAuthorizedTestServer(t)
	defer srv.Close()

	form := url.Values{
		"name":                               {"Renamed Project"},
		"platform":                           {"python"},
		"status":                             {"disabled"},
		"event_retention_days":               {"14"},
		"attachment_retention_days":          {"7"},
		"debug_retention_days":               {"30"},
		"telemetry_errors_days":              {"14"},
		"telemetry_errors_tier":              {"delete"},
		"telemetry_logs_days":                {"14"},
		"telemetry_logs_tier":                {"delete"},
		"telemetry_traces_days":              {"14"},
		"telemetry_traces_tier":              {"delete"},
		"telemetry_replays_days":             {"7"},
		"telemetry_replays_tier":             {"archive"},
		"telemetry_replays_archive_days":     {"21"},
		"telemetry_profiles_days":            {"14"},
		"telemetry_profiles_tier":            {"delete"},
		"telemetry_outcomes_days":            {"14"},
		"telemetry_outcomes_tier":            {"delete"},
		"telemetry_attachments_days":         {"7"},
		"telemetry_attachments_tier":         {"delete"},
		"telemetry_debug_files_days":         {"30"},
		"telemetry_debug_files_tier":         {"archive"},
		"telemetry_debug_files_archive_days": {"60"},
		"replay_sample_rate":                 {"0.25"},
		"replay_max_bytes":                   {"4096"},
		"replay_scrub_fields":                {"email, token"},
		"replay_scrub_selectors":             {".secret"},
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/settings/project", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest without csrf: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /settings/project without csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status without csrf = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/settings/project", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest with csrf: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /settings/project with csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status with csrf = %d, want 303", resp.StatusCode)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM projects WHERE id = 'test-proj'`).Scan(&name); err != nil {
		t.Fatalf("query updated project: %v", err)
	}
	if name != "Renamed Project" {
		t.Fatalf("updated project name = %q, want Renamed Project", name)
	}
}
