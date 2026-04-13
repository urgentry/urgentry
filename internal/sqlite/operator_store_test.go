package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestOperatorStoreOverview(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'checkout', 'Checkout', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, total_items, processed_items, failed_items, requested_via, last_error, created_at, updated_at)
		 VALUES
		 	('run-1', 'native_reprocess', 'running', 'org-1', 'proj-1', 'checkout@1.2.3', 10, 6, 1, 'test', 'missing symbols', ?, ?)`,
		now.Add(-10*time.Minute).Format(time.RFC3339),
		now.Add(-2*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed backfill: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_archives
			(id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES
		 	('archive-1', 'proj-1', 'replays', 'replay', 'replay-1', 'archive/replay-1', '{}', ?)`,
		now.Add(-5*time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed retention outcome: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO auth_audit_logs
			(id, credential_type, user_id, organization_id, project_id, action, created_at)
		 VALUES
		 	('audit-1', 'session', 'owner@example.com', 'org-1', 'proj-1', 'backfill.requested', ?)`,
		now.Add(-time.Minute).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}
	if err := NewOperatorAuditStore(db).Record(context.Background(), store.OperatorAuditRecord{
		Action:         "upgrade.apply",
		Status:         "succeeded",
		Source:         "compose",
		Actor:          "ops-user",
		Detail:         "applied upgrade",
		MetadataJSON:   `{"window":"nightly"}`,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
	}); err != nil {
		t.Fatalf("seed operator audit: %v", err)
	}
	completed := true
	lifecycle := NewLifecycleStore(db)
	if _, err := lifecycle.SyncInstallState(context.Background(), store.InstallStateSync{
		Region:             "us",
		Environment:        "production",
		Version:            "v1.2.3",
		BootstrapCompleted: &completed,
		CapturedAt:         now,
	}); err != nil {
		t.Fatalf("seed install state: %v", err)
	}

	ops := NewOperatorStore(
		db,
		store.OperatorRuntime{Role: "api", Env: "test", AsyncBackend: "jetstream", CacheBackend: "valkey", BlobBackend: "s3"},
		lifecycle,
		nil,
		func(context.Context) (int, error) { return 3, nil },
		OperatorCheck{
			Name: "sqlite",
			Check: func(context.Context) (store.OperatorServiceStatus, error) {
				return store.OperatorServiceStatus{Name: "sqlite", Status: "ok", Detail: "reachable"}, nil
			},
		},
		OperatorCheck{
			Name: "jetstream",
			Check: func(context.Context) (store.OperatorServiceStatus, error) {
				return store.OperatorServiceStatus{Name: "jetstream", Status: "skipped", Detail: "sqlite backend"}, nil
			},
		},
	)
	overview, err := ops.Overview(context.Background(), "acme", 8)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if overview == nil {
		t.Fatal("Overview returned nil")
	}
	if overview.OrganizationSlug != "acme" {
		t.Fatalf("OrganizationSlug = %q, want acme", overview.OrganizationSlug)
	}
	if overview.Queue.Depth != 3 {
		t.Fatalf("Queue.Depth = %d, want 3", overview.Queue.Depth)
	}
	if overview.Install == nil || overview.Install.Region != "us" || !overview.Install.BootstrapCompleted {
		t.Fatalf("unexpected install state: %+v", overview.Install)
	}
	if len(overview.Services) != 2 || overview.Services[0].Name != "sqlite" {
		t.Fatalf("unexpected services: %+v", overview.Services)
	}
	if len(overview.FleetNodes) != 1 || overview.FleetNodes[0].Role != "api" {
		t.Fatalf("unexpected fleet nodes: %+v", overview.FleetNodes)
	}
	if len(overview.SLOs) == 0 {
		t.Fatalf("expected slo status, got %+v", overview.SLOs)
	}
	if len(overview.Alerts) == 0 {
		t.Fatalf("expected alerts, got %+v", overview.Alerts)
	}
	if len(overview.Backfills) != 1 || overview.Backfills[0].ID != "run-1" {
		t.Fatalf("unexpected backfills: %+v", overview.Backfills)
	}
	if len(overview.RetentionOutcomes) != 1 || overview.RetentionOutcomes[0].ID != "archive-1" {
		t.Fatalf("unexpected retention outcomes: %+v", overview.RetentionOutcomes)
	}
	if len(overview.AuditLogs) != 1 || overview.AuditLogs[0].ID != "audit-1" {
		t.Fatalf("unexpected audit logs: %+v", overview.AuditLogs)
	}
	if len(overview.InstallAudits) != 1 || overview.InstallAudits[0].Action != "upgrade.apply" {
		t.Fatalf("unexpected install audits: %+v", overview.InstallAudits)
	}
}
