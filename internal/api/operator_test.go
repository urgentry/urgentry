package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func seedOperatorOverviewFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, total_items, processed_items, failed_items, requested_via, last_error, created_at, updated_at)
		 VALUES
		 	('run-ops-1', 'native_reprocess', 'running', 'test-org-id', 'test-proj-id', 'ios@1.2.3', 4, 2, 1, 'test', 'missing symbols', datetime('now', '-10 minutes'), datetime('now', '-2 minutes'))`,
	); err != nil {
		t.Fatalf("seed backfill: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_archives
			(id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES
		 	('archive-ops-1', 'test-proj-id', 'profiles', 'profile', 'profile-1', 'archive/profile-1', '{}', datetime('now', '-4 minutes'))`,
	); err != nil {
		t.Fatalf("seed retention outcome: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO auth_audit_logs
			(id, credential_type, user_id, organization_id, project_id, action, created_at)
		 VALUES
		 	('audit-ops-1', 'session', 'owner@example.com', 'test-org-id', 'test-proj-id', 'backfill.requested', datetime('now', '-1 minutes'))`,
	); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}
	if err := sqlite.NewOperatorAuditStore(db).Record(t.Context(), store.OperatorAuditRecord{
		Action:         "backup.capture",
		Status:         "succeeded",
		Source:         "compose",
		Actor:          "ops-user",
		Detail:         "captured backup",
		MetadataJSON:   `{"dir":"/tmp/backup"}`,
		OrganizationID: "test-org-id",
		ProjectID:      "test-proj-id",
	}); err != nil {
		t.Fatalf("seed operator audit log: %v", err)
	}
	completed := true
	if _, err := sqlite.NewLifecycleStore(db).SyncInstallState(t.Context(), store.InstallStateSync{
		Region:             "us",
		Environment:        "production",
		Version:            "v1.2.3",
		BootstrapCompleted: &completed,
	}); err != nil {
		t.Fatalf("seed install state: %v", err)
	}
}

func TestAPIOperatorOverview(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	seedOperatorOverviewFixture(t, db)

	ops := sqlite.NewOperatorStore(
		db,
		store.OperatorRuntime{Role: "api", Env: "test", AsyncBackend: "jetstream", CacheBackend: "valkey", BlobBackend: "s3"},
		sqlite.NewLifecycleStore(db),
		nil,
		func(context.Context) (int, error) { return 5, nil },
		sqlite.OperatorCheck{
			Name: "sqlite",
			Check: func(context.Context) (store.OperatorServiceStatus, error) {
				return store.OperatorServiceStatus{Name: "sqlite", Status: "ok", Detail: "reachable"}, nil
			},
		},
	)

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{Operators: ops})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/ops/overview/", pat, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var overview store.OperatorOverview
	decodeBody(t, resp, &overview)
	if overview.OrganizationSlug != "test-org" {
		t.Fatalf("OrganizationSlug = %q, want test-org", overview.OrganizationSlug)
	}
	if overview.Queue.Depth != 5 {
		t.Fatalf("Queue.Depth = %d, want 5", overview.Queue.Depth)
	}
	if overview.Install == nil || overview.Install.Version != "v1.2.3" {
		t.Fatalf("unexpected install state: %+v", overview.Install)
	}
	if len(overview.SLOs) == 0 {
		t.Fatalf("expected slos, got %+v", overview.SLOs)
	}
	if len(overview.Alerts) == 0 {
		t.Fatalf("expected alerts, got %+v", overview.Alerts)
	}
	if len(overview.Backfills) != 1 || overview.Backfills[0].ID != "run-ops-1" {
		t.Fatalf("unexpected backfills: %+v", overview.Backfills)
	}
	if len(overview.RetentionOutcomes) != 1 || overview.RetentionOutcomes[0].ID != "archive-ops-1" {
		t.Fatalf("unexpected retention outcomes: %+v", overview.RetentionOutcomes)
	}
	foundAudit := false
	for _, item := range overview.AuditLogs {
		if item.ID == "audit-ops-1" && item.Action == "backfill.requested" {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected backfill.requested audit log, got %+v", overview.AuditLogs)
	}
	if len(overview.InstallAudits) != 1 || overview.InstallAudits[0].Action != "backup.capture" {
		t.Fatalf("unexpected install audits: %+v", overview.InstallAudits)
	}
}

func TestAPIOperatorOverviewRequiresOrgAdmin(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	seedOperatorOverviewFixture(t, db)

	ops := sqlite.NewOperatorStore(
		db,
		store.OperatorRuntime{Role: "api", Env: "test"},
		sqlite.NewLifecycleStore(db),
		nil,
		func(context.Context) (int, error) { return 1, nil },
	)
	ts, _ := newSQLiteAuthorizedServer(t, db, Dependencies{Operators: ops})
	defer ts.Close()

	authStore := sqlite.NewAuthStore(db)
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'owner@example.com'`).Scan(&userID); err != nil {
		t.Fatalf("query user id: %v", err)
	}
	limitedPAT, err := authStore.CreatePersonalAccessToken(context.Background(), userID, "Org Reader", []string{auth.ScopeOrgRead}, nil, "gpat_opsread_unique_token")
	if err != nil {
		t.Fatalf("CreatePersonalAccessToken: %v", err)
	}

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/ops/overview/", limitedPAT, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIOperatorDiagnostics(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	seedOperatorOverviewFixture(t, db)

	ops := sqlite.NewOperatorStore(
		db,
		store.OperatorRuntime{Role: "api", Env: "test", AsyncBackend: "jetstream", CacheBackend: "valkey", BlobBackend: "s3"},
		sqlite.NewLifecycleStore(db),
		nil,
		func(context.Context) (int, error) { return 5, nil },
		sqlite.OperatorCheck{
			Name: "sqlite",
			Check: func(context.Context) (store.OperatorServiceStatus, error) {
				return store.OperatorServiceStatus{Name: "sqlite", Status: "ok", Detail: "reachable"}, nil
			},
		},
	)

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{Operators: ops})
	defer ts.Close()

	resp := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/ops/diagnostics/", pat, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var bundle store.OperatorDiagnosticsBundle
	decodeBody(t, resp, &bundle)
	if bundle.OrganizationSlug != "test-org" {
		t.Fatalf("OrganizationSlug = %q, want test-org", bundle.OrganizationSlug)
	}
	if bundle.Install == nil || bundle.Install.Version != "v1.2.3" {
		t.Fatalf("unexpected install bundle state: %+v", bundle.Install)
	}
	if len(bundle.Redactions) == 0 {
		t.Fatalf("expected redactions, got %+v", bundle)
	}
	if len(bundle.InstallAudits) != 1 || bundle.InstallAudits[0].Action != "backup.capture" {
		t.Fatalf("unexpected install audits: %+v", bundle.InstallAudits)
	}
}
