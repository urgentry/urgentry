#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC1091
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-paths.sh"
resolve_urgentry_paths "$0"

bash "$REPO_ROOT/scripts/check-markdown-links.sh"

cd "$APP_DIR"

make build
make tiny-smoke

go test ./internal/auth -run 'TestAuthorizer(LoginAndRevokeSession|WebMiddleware|APIAndHelpers|RejectsInvalidCredentials)' -count=1
go test ./internal/sqlite -run 'Test(MigrationsIdempotent|DashboardStoreMigrationApplied|QueryGuardStoreCheckAndRecord|QueryGuardStoreRejectsOversizedQuery|RetentionStoreApplyDeletePolicies|RetentionStoreArchiveAndRestore|RetentionStoreArchiveAndRestoreProfiles|ReplayStoreSaveAndIndex|ProfileStoreSaveAndRead|ProfileStoreQueryViews)' -count=1
go test ./internal/ingest -run 'TestEnvelopeHandler(StoresReplayMetadataAndAssets|ReplayRetryIsIdempotent|AppliesReplaySamplingPolicy|ScrubsReplayPrivacyFields|EnforcesReplaySizeCap|StoresProfileMetadata)' -count=1
go test ./internal/api -run 'Test(DashboardAPISharingAndWidgetCRUD|APIOrganizationDiscover_SQLite|APIOrganizationDiscoverQuotaExhaustion|APIReplayQueryCostGuard|APIReplaysAndProfiles_SQLite|RetentionArchiveAndRestoreReplays|MonitorCRUD|CreateRelease)' -count=1
go test ./internal/web -run 'Test(DiscoverAndLogsPages|DiscoverSavedQueryAndDashboardFlows|DashboardStarterTemplates|AnalyticsPagesShowOnboardingGuides|DiscoverPageReturnsQueryGuardRateLimit|ReplayAndProfilePages|SettingsAndReleaseDetailPages|ReleasesPage)' -count=1

make lint
make release VERSION=tiny-launch-gate

(
	cd dist
	sha256sum -c SHA256SUMS
)

rm -rf dist
