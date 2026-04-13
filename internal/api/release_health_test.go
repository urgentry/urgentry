package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func TestProjectReleaseHealthAndSessions_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@1.2.3",
		DistinctID:  "user-1",
		Status:      "crashed",
		Errors:      1,
		StartedAt:   time.Now().UTC(),
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession single: %v", err)
	}
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@1.2.3",
		Status:      "errored",
		Errors:      1,
		Quantity:    2,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession aggregate: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/health/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	var health ReleaseHealth
	decodeBody(t, resp, &health)
	if health.Version != "ios@1.2.3" {
		t.Fatalf("Version = %q, want ios@1.2.3", health.Version)
	}
	if health.SessionCount != 3 || health.CrashedSessions != 1 || health.ErroredSessions != 2 {
		t.Fatalf("health = %+v", health)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/sessions/?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", resp.StatusCode)
	}
	var sessions []ReleaseSession
	decodeBody(t, resp, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(sessions))
	}
	if sessions[0].Quantity == 0 {
		t.Fatalf("expected quantity on release session response: %+v", sessions[0])
	}
}

func TestListReleases_SQLiteIncludesHealth(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@9.9.9",
		Status:      "abnormal",
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/releases/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var releases []Release
	decodeBody(t, resp, &releases)
	for _, release := range releases {
		if release.Version != "ios@9.9.9" {
			continue
		}
		if release.SessionCount != 1 || release.AbnormalSessions != 1 {
			t.Fatalf("release = %+v", release)
		}
		return
	}

	t.Fatal("expected release with health metrics")
}

func TestListReleases_SQLiteIncludesNativeSummaries(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	insertSQLiteReleaseWithOrg(t, db, "rel-native-ios", "ios@1.2.3")
	insertSQLiteReleaseWithOrg(t, db, "rel-native-android", "android@2.0.0")

	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, ingested_at, tags_json, payload_json, processing_status, ingest_error)
		 VALUES
			('evt-rel-native-ios', 'test-proj-id', 'evt-rel-native-ios', 'grp-1', 'ios@1.2.3', 'production', 'cocoa', 'fatal', 'error', 'Native crash', 'boom', 'App', ?, ?, '{}',
			 '{"event_id":"evt-rel-native-ios","release":"ios@1.2.3","exception":{"values":[{"stacktrace":{"frames":[{"instruction_addr":"0x1010","debug_id":"debug-1","package":"code-1","filename":"src/AppDelegate.swift","function":"main"}]}}]}}',
			 'completed', ''),
			('evt-rel-native-android', 'test-proj-id', 'evt-rel-native-android', 'grp-2', 'android@2.0.0', 'production', 'android', 'fatal', 'error', 'Native crash', 'boom', 'App', ?, ?, '{}',
			 '{"event_id":"evt-rel-native-android","release":"android@2.0.0","exception":{"values":[{"stacktrace":{"frames":[{"instruction_addr":"0x2020","package":"libapp.so","function":"main"}]}}]}}',
			 'failed', 'stackwalk failed')`,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
		now.Add(time.Minute).Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert native release events: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, debug_file_id, cursor_rowid, total_items, processed_items, updated_items, failed_items, requested_via, last_error, created_at, updated_at)
		 VALUES
			('run-rel-native-ios', 'native_reprocess', 'completed', 'test-org-id', 'test-proj-id', 'ios@1.2.3', '', 0, 1, 1, 1, 0, 'test', '', ?, ?),
			('run-rel-native-android', 'native_reprocess', 'failed', 'test-org-id', NULL, 'android@2.0.0', '', 0, 1, 1, 0, 1, 'test', 'stackwalk failed', ?, ?)`,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
		now.Add(time.Minute).Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert native release runs: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/releases/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var releases []Release
	decodeBody(t, resp, &releases)
	releaseByVersion := make(map[string]Release, len(releases))
	for _, release := range releases {
		releaseByVersion[release.Version] = release
	}

	ios, ok := releaseByVersion["ios@1.2.3"]
	if !ok {
		t.Fatalf("missing ios release in %+v", releases)
	}
	if ios.NativeEventCount != 1 || ios.NativeResolvedFrames != 1 || ios.NativeReprocessStatus != "completed" {
		t.Fatalf("unexpected ios release summary: %+v", ios)
	}

	android, ok := releaseByVersion["android@2.0.0"]
	if !ok {
		t.Fatalf("missing android release in %+v", releases)
	}
	if android.NativeEventCount != 1 || android.NativeFailedEvents != 1 || android.NativeLastError != "stackwalk failed" || android.NativeReprocessStatus != "failed" {
		t.Fatalf("unexpected android release summary: %+v", android)
	}
}

func TestOrgSessions_SQLiteBucketsStatuses(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	base := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	for _, session := range []*sqlite.ReleaseSession{
		{
			ProjectID:   "test-proj-id",
			Release:     "ios@1.2.3",
			Status:      "ok",
			Quantity:    2,
			DateCreated: base,
		},
		{
			ProjectID:   "test-proj-id",
			Release:     "ios@1.2.3",
			Status:      "crashed",
			Quantity:    1,
			DateCreated: base.Add(time.Hour),
		},
	} {
		if err := releaseHealth.SaveSession(t.Context(), session); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/sessions/?start=2026-04-12T09:00:00Z&end=2026-04-12T12:00:00Z")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("org sessions status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Intervals []string `json:"intervals"`
		Groups    []struct {
			By     map[string]string `json:"by"`
			Totals map[string]int    `json:"totals"`
			Series map[string][]int  `json:"series"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode org sessions: %v", err)
	}
	resp.Body.Close()

	if len(payload.Intervals) != 2 {
		t.Fatalf("intervals len = %d, want 2 (%+v)", len(payload.Intervals), payload.Intervals)
	}
	groupByStatus := make(map[string]struct {
		total  int
		series []int
	}, len(payload.Groups))
	for _, group := range payload.Groups {
		groupByStatus[group.By["session.status"]] = struct {
			total  int
			series []int
		}{
			total:  group.Totals["sum(session)"],
			series: group.Series["sum(session)"],
		}
	}
	if got := groupByStatus["ok"]; got.total != 2 || len(got.series) != 2 || got.series[0] != 2 || got.series[1] != 0 {
		t.Fatalf("unexpected ok group: %+v", got)
	}
	if got := groupByStatus["crashed"]; got.total != 1 || len(got.series) != 2 || got.series[0] != 0 || got.series[1] != 1 {
		t.Fatalf("unexpected crashed group: %+v", got)
	}
}
