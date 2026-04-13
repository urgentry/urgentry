package postgrescontrol

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPreventStoreRepositoryReadAndTokenRotation(t *testing.T) {
	t.Parallel()

	template := postgresControlPostgres.NewTemplate("prevent", func(db *sql.DB) error {
		return Migrate(context.Background(), db)
	})
	db := template.OpenDatabase(t, "prevent")
	ctx := context.Background()
	store := NewPreventStore(db)

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	lastSynced := base.Add(10 * time.Minute)
	lastStarted := base.Add(5 * time.Minute)
	branchTime := base.Add(20 * time.Minute)
	suiteRun := base.Add(30 * time.Minute)
	resultTime := base.Add(40 * time.Minute)
	aggRun := base.Add(50 * time.Minute)

	if _, err := db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO repositories (id, organization_id, owner_slug, name, provider, url, external_slug, status, default_branch, test_analytics_enabled, sync_status, last_synced_at, last_sync_started_at, last_sync_error, created_at)
VALUES ('repo-1', 'org-1', 'sentry', 'platform', 'github', 'https://github.com/sentry/platform', 'sentry/platform', 'active', 'main', TRUE, 'synced', $1, $2, '', $3)`,
		lastSynced, lastStarted, base,
	); err != nil {
		t.Fatalf("seed repository: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO prevent_repository_branches (id, repository_id, name, is_default, status, last_synced_at, created_at) VALUES
	('branch-1', 'repo-1', 'main', TRUE, 'active', $1, $2),
	('branch-2', 'repo-1', 'release/1.0', FALSE, 'active', NULL, $3)`,
		branchTime, branchTime, branchTime,
	); err != nil {
		t.Fatalf("seed branches: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO prevent_repository_tokens (id, repository_id, label, token_value, token_prefix, token_hash, status, created_at, last_used_at, revoked_at, rotated_at)
VALUES ('token-1', 'repo-1', 'CI', 'gprevent_old_full', 'gprevent_old', 'hash-old', 'active', $1, NULL, NULL, NULL)`,
		base,
	); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO prevent_repository_test_suites (id, repository_id, external_suite_id, name, status, last_run_at, created_at)
VALUES ('suite-1', 'repo-1', 'suite-ext-1', 'Unit', 'active', $1, $2)`,
		suiteRun, suiteRun,
	); err != nil {
		t.Fatalf("seed suite: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO prevent_repository_test_results (id, repository_id, suite_id, suite_name, branch_name, commit_sha, status, duration_ms, test_count, failure_count, skipped_count, created_at)
VALUES ('result-1', 'repo-1', 'suite-1', 'Unit', 'main', 'abc123', 'passed', 1200, 120, 0, 0, $1)`,
		resultTime,
	); err != nil {
		t.Fatalf("seed result: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO prevent_repository_test_result_aggregates (id, repository_id, branch_name, total_runs, passing_runs, failing_runs, skipped_runs, avg_duration_ms, last_run_at, created_at)
VALUES ('agg-1', 'repo-1', 'main', 9, 8, 1, 0, 1200, $1, $2)`,
		aggRun, aggRun,
	); err != nil {
		t.Fatalf("seed aggregate: %v", err)
	}

	repos, err := store.ListRepositories(ctx, "acme", "sentry")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "platform" || repos[0].OwnerSlug != "sentry" || repos[0].SyncStatus != "synced" || !repos[0].TestAnalyticsEnabled {
		t.Fatalf("unexpected repositories: %+v", repos)
	}

	repo, err := store.GetRepository(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo == nil || repo.DefaultBranch != "main" || repo.LastSyncedAt == nil || repo.LastSyncStartedAt == nil {
		t.Fatalf("unexpected repository: %+v", repo)
	}

	branches, err := store.ListRepositoryBranches(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("ListRepositoryBranches: %v", err)
	}
	if len(branches) != 2 || !branches[0].IsDefault || branches[0].Name != "main" {
		t.Fatalf("unexpected branches: %+v", branches)
	}

	tokens, err := store.ListRepositoryTokens(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("ListRepositoryTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].TokenPrefix != "gprevent_old" || tokens[0].Token != "gprevent_old_full" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}

	rotated, raw, err := store.RegenerateRepositoryToken(ctx, "acme", "sentry", "platform", "token-1")
	if err != nil {
		t.Fatalf("RegenerateRepositoryToken: %v", err)
	}
	if raw == "" || rotated == nil || rotated.TokenPrefix == "gprevent_old" || rotated.RotatedAt == nil {
		t.Fatalf("unexpected rotated token: %+v raw=%q", rotated, raw)
	}
	if rotated.Token != raw {
		t.Fatalf("rotated token = %q, want raw token", rotated.Token)
	}

	syncing, err := store.GetOwnerSyncStatus(ctx, "acme", "sentry")
	if err != nil || syncing {
		t.Fatalf("GetOwnerSyncStatus before start = %v err=%v, want false nil", syncing, err)
	}
	started, err := store.StartOwnerSync(ctx, "acme", "sentry")
	if err != nil || !started {
		t.Fatalf("StartOwnerSync = %v err=%v, want true nil", started, err)
	}
	syncing, err = store.GetOwnerSyncStatus(ctx, "acme", "sentry")
	if err != nil || !syncing {
		t.Fatalf("GetOwnerSyncStatus after start = %v err=%v, want true nil", syncing, err)
	}

	syncStatus, err := store.GetRepositorySyncStatus(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("GetRepositorySyncStatus: %v", err)
	}
	if syncStatus == nil || syncStatus.Status != "syncing" || syncStatus.LastSyncedAt == nil {
		t.Fatalf("unexpected sync status: %+v", syncStatus)
	}

	suites, err := store.ListRepositoryTestSuites(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("ListRepositoryTestSuites: %v", err)
	}
	if len(suites) != 1 || suites[0].ExternalID != "suite-ext-1" || suites[0].LastRunAt == nil {
		t.Fatalf("unexpected suites: %+v", suites)
	}

	results, err := store.ListRepositoryTestResults(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("ListRepositoryTestResults: %v", err)
	}
	if len(results) != 1 || results[0].SuiteName != "Unit" || results[0].FailureCount != 0 {
		t.Fatalf("unexpected results: %+v", results)
	}

	aggregates, err := store.ListRepositoryTestAggregates(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("ListRepositoryTestAggregates: %v", err)
	}
	if len(aggregates) != 1 || aggregates[0].TotalRuns != 9 || aggregates[0].LastRunAt == nil {
		t.Fatalf("unexpected aggregates: %+v", aggregates)
	}
}
