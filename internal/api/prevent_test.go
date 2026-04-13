package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func newSQLitePreventServer(t *testing.T) (*httptest.Server, *sql.DB, time.Time) {
	t.Helper()

	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	base := time.Now().UTC().Truncate(time.Second)
	syncedAt := base.Add(-2 * time.Hour)
	startedAt := base.Add(-10 * time.Minute)
	currentResultAt := base.Add(-1 * time.Hour)
	previousResultAt := base.Add(-8 * 24 * time.Hour)

	if _, err := db.Exec(`
INSERT INTO repositories (
	id, organization_id, owner_slug, name, provider, url, external_slug, status,
	default_branch, test_analytics_enabled, sync_status, last_synced_at,
	last_sync_started_at, last_sync_error, created_at
) VALUES (
	'repo-prevent-1', 'test-org-id', 'sentry', 'platform', 'github',
	'https://github.com/sentry/platform', 'sentry/platform', 'active',
	'main', 1, 'idle', ?, ?, '', ?
)`,
		syncedAt.Format(time.RFC3339),
		startedAt.Format(time.RFC3339),
		base.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed prevent repository: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO prevent_repository_branches (
	id, repository_id, name, is_default, status, last_synced_at, created_at
) VALUES
	('branch-prevent-main', 'repo-prevent-1', 'main', 1, 'active', ?, ?),
	('branch-prevent-release', 'repo-prevent-1', 'release/1.0', 0, 'active', ?, ?)
`,
		syncedAt.Format(time.RFC3339),
		base.Format(time.RFC3339),
		previousResultAt.Format(time.RFC3339),
		previousResultAt.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed prevent branches: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO prevent_repository_tokens (
	id, repository_id, label, token_value, token_prefix, token_hash, status,
	rotated_at, last_used_at, revoked_at, created_at
) VALUES (
	'token-prevent-1', 'repo-prevent-1', 'Primary token', 'gprevent_primary_full',
	'gprevent_primary', 'hash-primary', 'active', NULL, NULL, NULL, ?
)`,
		base.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed prevent token: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO prevent_repository_test_suites (
	id, repository_id, external_suite_id, name, status, last_run_at, created_at
) VALUES
	('suite-prevent-api', 'repo-prevent-1', 'suite-ext-api', 'api', 'active', ?, ?),
	('suite-prevent-ui', 'repo-prevent-1', 'suite-ext-ui', 'ui', 'active', ?, ?)
`,
		currentResultAt.Format(time.RFC3339),
		base.Format(time.RFC3339),
		previousResultAt.Format(time.RFC3339),
		previousResultAt.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed prevent test suites: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO prevent_repository_test_results (
	id, repository_id, suite_id, suite_name, branch_name, commit_sha, status,
	duration_ms, test_count, failure_count, skipped_count, created_at
) VALUES
	('result-prevent-current', 'repo-prevent-1', 'suite-prevent-api', 'api', 'main',
	 'abc123', 'failed', 1200, 4, 1, 0, ?),
	('result-prevent-previous', 'repo-prevent-1', 'suite-prevent-ui', 'ui', 'main',
	 'def456', 'passed', 600, 3, 0, 0, ?)
`,
		currentResultAt.Format(time.RFC3339),
		previousResultAt.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed prevent test results: %v", err)
	}

	deps := sqliteAuthorizedDependencies(t, db, Dependencies{})
	return httptest.NewServer(NewRouter(deps)), db, base
}

func TestAPIPreventRepositoryManagement_SQLite(t *testing.T) {
	ts, _, base := newSQLitePreventServer(t)
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list repositories status = %d, want 200", resp.StatusCode)
	}
	var repos preventRepositoriesResponse
	decodeBody(t, resp, &repos)
	if repos.TotalCount != 1 || len(repos.Results) != 1 {
		t.Fatalf("unexpected repositories: %+v", repos)
	}
	if repos.Results[0].Name != "platform" || repos.Results[0].DefaultBranch != "main" {
		t.Fatalf("unexpected repository row: %+v", repos.Results[0])
	}
	if repos.Results[0].UpdatedAt != base.Add(-10*time.Minute).UTC().Format(time.RFC3339) || repos.Results[0].LatestCommitAt != base.Add(-2*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected repository timestamps: %+v", repos.Results[0])
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repository detail status = %d, want 200", resp.StatusCode)
	}
	var detail preventRepositoryDetailResponse
	decodeBody(t, resp, &detail)
	if detail.UploadToken != "gprevent_primary_full" || !detail.TestAnalyticsEnabled {
		t.Fatalf("unexpected repository detail: %+v", detail)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/tokens/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tokens status = %d, want 200", resp.StatusCode)
	}
	var tokens preventRepositoryTokensResponse
	decodeBody(t, resp, &tokens)
	if tokens.TotalCount != 1 || len(tokens.Results) != 1 || tokens.Results[0].Name != "platform" || tokens.Results[0].Token != "gprevent_primary_full" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}

	resp = authPost(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/token/regenerate/", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate token status = %d, want 200", resp.StatusCode)
	}
	var regenerate struct {
		Token string `json:"token"`
	}
	decodeBody(t, resp, &regenerate)
	if regenerate.Token == "" || regenerate.Token == "gprevent_primary_full" {
		t.Fatalf("unexpected regenerated token: %+v", regenerate)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/tokens/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tokens after regenerate status = %d, want 200", resp.StatusCode)
	}
	decodeBody(t, resp, &tokens)
	if tokens.TotalCount != 1 || tokens.Results[0].Token != regenerate.Token {
		t.Fatalf("unexpected tokens after regenerate: %+v", tokens)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/sync/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync GET status = %d, want 200", resp.StatusCode)
	}
	var sync preventRepositorySyncResponse
	decodeBody(t, resp, &sync)
	if sync.IsSyncing {
		t.Fatalf("unexpected sync status before start: %+v", sync)
	}

	resp = authPost(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/sync/", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync POST status = %d, want 200", resp.StatusCode)
	}
	decodeBody(t, resp, &sync)
	if !sync.IsSyncing {
		t.Fatalf("unexpected sync start response: %+v", sync)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repositories/sync/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync GET after start status = %d, want 200", resp.StatusCode)
	}
	decodeBody(t, resp, &sync)
	if !sync.IsSyncing {
		t.Fatalf("unexpected sync status after start: %+v", sync)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/branches/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("branches status = %d, want 200", resp.StatusCode)
	}
	var branches preventRepositoryBranchesResponse
	decodeBody(t, resp, &branches)
	if branches.DefaultBranch != "main" || branches.TotalCount != 2 || len(branches.Results) != 2 {
		t.Fatalf("unexpected branches: %+v", branches)
	}
	if branches.Results[0].Name != "main" || branches.Results[1].Name != "release/1.0" {
		t.Fatalf("unexpected branch order: %+v", branches.Results)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/test-suites/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test suites status = %d, want 200", resp.StatusCode)
	}
	var suites preventRepositoryTestSuitesResponse
	decodeBody(t, resp, &suites)
	if len(suites.TestSuites) != 2 || suites.TestSuites[0] != "api" || suites.TestSuites[1] != "ui" {
		t.Fatalf("unexpected test suites: %+v", suites)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/test-results/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test results status = %d, want 200", resp.StatusCode)
	}
	var results preventRepositoryTestResultsResponse
	decodeBody(t, resp, &results)
	if results.DefaultBranch != "main" || results.TotalCount != 2 || len(results.Results) != 2 {
		t.Fatalf("unexpected test results: %+v", results)
	}
	if results.Results[0].Name != "api" || results.Results[0].TotalFailCount != 1 || results.Results[0].TotalPassCount != 3 {
		t.Fatalf("unexpected first test result: %+v", results.Results[0])
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/test-results-aggregates/?interval=INTERVAL_7_DAY&branch=main")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test aggregates status = %d, want 200", resp.StatusCode)
	}
	var aggregates preventRepositoryTestResultsAggregatesResponse
	decodeBody(t, resp, &aggregates)
	if aggregates.TotalDuration != 1200 || aggregates.TotalFails != 1 || aggregates.TotalSkips != 0 {
		t.Fatalf("unexpected aggregates: %+v", aggregates)
	}
	if aggregates.SlowestTestsDuration != 300 || aggregates.TotalSlowTests != 0 || aggregates.TotalDurationPercentChange != 100 {
		t.Fatalf("unexpected aggregate changes: %+v", aggregates)
	}
}

func TestAPIPreventRepositoryFlakyResults_SQLite(t *testing.T) {
	ts, db, base := newSQLitePreventServer(t)
	defer ts.Close()

	if _, err := db.Exec(`
INSERT INTO prevent_repository_test_results (
	id, repository_id, suite_id, suite_name, branch_name, commit_sha, status,
	duration_ms, test_count, failure_count, skipped_count, created_at
) VALUES (
	'result-prevent-flaky-pass', 'repo-prevent-1', 'suite-prevent-api', 'api', 'main',
	'ghi789', 'passed', 800, 4, 0, 0, ?
)`,
		base.Add(-30*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed flaky prevent result: %v", err)
	}

	resp := authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/test-results/?interval=INTERVAL_7_DAY&filterBy=FLAKY_TESTS")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flaky test results status = %d, want 200", resp.StatusCode)
	}
	var results preventRepositoryTestResultsResponse
	decodeBody(t, resp, &results)
	if results.TotalCount != 2 || len(results.Results) != 2 {
		t.Fatalf("unexpected flaky test results: %+v", results)
	}
	for _, result := range results.Results {
		if result.Name != "api" {
			t.Fatalf("unexpected flaky result name: %+v", result)
		}
		if result.FlakeRate != 0.125 || result.TotalFlakyFailCount != 1 {
			t.Fatalf("unexpected flaky metrics: %+v", result)
		}
		if result.AvgDuration != 250 {
			t.Fatalf("unexpected flaky average duration: %+v", result)
		}
	}
	if results.Results[0].TotalFailCount != 1 || results.Results[1].TotalFailCount != 0 {
		t.Fatalf("unexpected flaky ordering: %+v", results.Results)
	}

	resp = authGet(t, ts, "/api/0/organizations/test-org/prevent/owner/sentry/repository/platform/test-results-aggregates/?interval=INTERVAL_7_DAY&branch=main")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flaky test aggregates status = %d, want 200", resp.StatusCode)
	}
	var aggregates preventRepositoryTestResultsAggregatesResponse
	decodeBody(t, resp, &aggregates)
	if aggregates.TotalDuration != 2000 || aggregates.SlowestTestsDuration != 250 {
		t.Fatalf("unexpected flaky aggregate durations: %+v", aggregates)
	}
	if aggregates.FlakeCount != 1 || aggregates.FlakeRate != 1 {
		t.Fatalf("unexpected flaky aggregate metrics: %+v", aggregates)
	}
}

func TestPaginatePrevent(t *testing.T) {
	items := []string{"a", "b", "c"}

	page, info, err := paginatePrevent(items, 2, "", "")
	if err != nil {
		t.Fatalf("first page error: %v", err)
	}
	if !reflect.DeepEqual(page, []string{"a", "b"}) {
		t.Fatalf("unexpected first page: %#v", page)
	}
	if info.HasPreviousPage || !info.HasNextPage {
		t.Fatalf("unexpected first page info: %+v", info)
	}
	if info.StartCursor == nil || *info.StartCursor != "0" || info.EndCursor == nil || *info.EndCursor != "1" {
		t.Fatalf("unexpected first page cursors: %+v", info)
	}

	page, info, err = paginatePrevent(items, 2, "1", "")
	if err != nil {
		t.Fatalf("next page error: %v", err)
	}
	if !reflect.DeepEqual(page, []string{"c"}) {
		t.Fatalf("unexpected next page: %#v", page)
	}
	if !info.HasPreviousPage || info.HasNextPage {
		t.Fatalf("unexpected next page info: %+v", info)
	}
	if info.StartCursor == nil || *info.StartCursor != "2" || info.EndCursor == nil || *info.EndCursor != "2" {
		t.Fatalf("unexpected next page cursors: %+v", info)
	}

	page, info, err = paginatePrevent(items, 2, "2", "prev")
	if err != nil {
		t.Fatalf("previous page error: %v", err)
	}
	if !reflect.DeepEqual(page, []string{"a", "b"}) {
		t.Fatalf("unexpected previous page: %#v", page)
	}
	if info.HasPreviousPage || !info.HasNextPage {
		t.Fatalf("unexpected previous page info: %+v", info)
	}
	if info.StartCursor == nil || *info.StartCursor != "0" || info.EndCursor == nil || *info.EndCursor != "1" {
		t.Fatalf("unexpected previous page cursors: %+v", info)
	}

	page, info, err = paginatePrevent(items, 2, "2", "")
	if err != nil {
		t.Fatalf("past-end page error: %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("expected empty page past end, got %#v", page)
	}
	if !info.HasPreviousPage || info.HasNextPage || info.StartCursor != nil || info.EndCursor != nil {
		t.Fatalf("unexpected past-end page info: %+v", info)
	}

	if _, _, err := paginatePrevent(items, 2, "bad", ""); err == nil {
		t.Fatal("expected invalid cursor error")
	}
}
