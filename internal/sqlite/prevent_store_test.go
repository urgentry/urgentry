package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestPreventStoreRepositoryReadAndTokenRotation(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := db.Exec(`
INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?);
INSERT INTO repositories (id, organization_id, owner_slug, name, provider, url, external_slug, status, default_branch, sync_status, last_synced_at, last_sync_started_at, last_sync_error, created_at)
VALUES ('repo-1', 'org-1', 'sentry', 'platform', 'github', 'https://github.com/sentry/platform', 'sentry/platform', 'active', 'main', 'synced', ?, ?, '', ?);
INSERT INTO prevent_repository_branches (id, repository_id, name, is_default, status, last_synced_at, created_at) VALUES
	('branch-1', 'repo-1', 'main', 1, 'active', ?, ?),
	('branch-2', 'repo-1', 'release/1.0', 0, 'active', NULL, ?);
INSERT INTO prevent_repository_tokens (id, repository_id, label, token_prefix, token_hash, status, created_at, last_used_at, revoked_at, rotated_at) VALUES
	('token-1', 'repo-1', 'CI', 'gprevent_old', 'hash-old', 'active', ?, NULL, NULL, NULL);
INSERT INTO prevent_repository_test_suites (id, repository_id, external_suite_id, name, status, last_run_at, created_at) VALUES
	('suite-1', 'repo-1', 'suite-ext-1', 'Unit', 'active', ?, ?);
INSERT INTO prevent_repository_test_results (id, repository_id, suite_id, suite_name, branch_name, commit_sha, status, duration_ms, test_count, failure_count, skipped_count, created_at) VALUES
	('result-1', 'repo-1', 'suite-1', 'Unit', 'main', 'abc123', 'passed', 1200, 120, 0, 0, ?);
INSERT INTO prevent_repository_test_result_aggregates (id, repository_id, branch_name, total_runs, passing_runs, failing_runs, skipped_runs, avg_duration_ms, last_run_at, created_at) VALUES
	('agg-1', 'repo-1', 'main', 9, 8, 1, 0, 1200, ?, ?);
`, now, now, now, now, now, now, now, now, now, now, now, now, now); err != nil {
		t.Fatalf("seed prevent data: %v", err)
	}

	store := NewPreventStore(db)

	repos, err := store.ListRepositories(ctx, "acme", "sentry")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "platform" || repos[0].OwnerSlug != "sentry" || repos[0].SyncStatus != "synced" {
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
	if len(tokens) != 1 || tokens[0].TokenPrefix != "gprevent_old" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}

	rotated, raw, err := store.RegenerateRepositoryToken(ctx, "acme", "sentry", "platform", "token-1")
	if err != nil {
		t.Fatalf("RegenerateRepositoryToken: %v", err)
	}
	if raw == "" || rotated == nil || rotated.TokenPrefix == "gprevent_old" || rotated.RotatedAt == nil {
		t.Fatalf("unexpected rotated token: %+v raw=%q", rotated, raw)
	}

	syncStatus, err := store.GetRepositorySyncStatus(ctx, "acme", "sentry", "platform")
	if err != nil {
		t.Fatalf("GetRepositorySyncStatus: %v", err)
	}
	if syncStatus == nil || syncStatus.Status != "synced" || syncStatus.LastSyncedAt == nil {
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
