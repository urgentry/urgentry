package web

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestOperatorPageRequiresSessionAndRendersOverview(t *testing.T) {
	srv, _, sessionToken, csrf := setupAuthorizedTestServerWithDeps(t, func(db *sql.DB, authz *auth.Authorizer, dataDir string, deps Dependencies) Dependencies {
		if _, err := db.Exec(
			`INSERT INTO backfill_runs
				(id, kind, status, organization_id, project_id, release_version, total_items, processed_items, failed_items, requested_via, last_error, created_at, updated_at)
			 VALUES
				('run-ops-web-1', 'native_reprocess', 'running', 'test-org', 'test-proj', 'backend@1.2.3', 8, 3, 1, 'test', 'missing symbols', datetime('now', '-10 minutes'), datetime('now', '-2 minutes'))`,
		); err != nil {
			t.Fatalf("seed backfill: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO telemetry_archives
				(id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
			 VALUES
				('archive-ops-web-1', 'test-proj', 'replays', 'replay', 'replay-1', 'archive/replay-1', '{}', datetime('now', '-4 minutes'))`,
		); err != nil {
			t.Fatalf("seed retention outcome: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO auth_audit_logs
				(id, credential_type, user_id, organization_id, project_id, action, created_at)
			 VALUES
				('audit-ops-web-1', 'session', 'owner@example.com', 'test-org', 'test-proj', 'backfill.requested', datetime('now', '-1 minutes'))`,
		); err != nil {
			t.Fatalf("seed audit log: %v", err)
		}
		if err := sqlite.NewOperatorAuditStore(db).Record(context.Background(), store.OperatorAuditRecord{
			Action:         "maintenance.enabled",
			Status:         "succeeded",
			Source:         "compose",
			Actor:          "ops-user",
			Detail:         "upgrade window",
			MetadataJSON:   `{"maintenanceMode":true}`,
			OrganizationID: "test-org",
			ProjectID:      "test-proj",
		}); err != nil {
			t.Fatalf("seed operator audit log: %v", err)
		}
		completed := true
		lifecycle := sqlite.NewLifecycleStore(db)
		if _, err := lifecycle.SyncInstallState(context.Background(), store.InstallStateSync{
			Region:             "us",
			Environment:        "production",
			Version:            "v1.2.3",
			BootstrapCompleted: &completed,
		}); err != nil {
			t.Fatalf("seed install state: %v", err)
		}
		deps.Operators = sqlite.NewOperatorStore(
			db,
			store.OperatorRuntime{Role: "api", Env: "test", AsyncBackend: "jetstream", CacheBackend: "valkey", BlobBackend: "s3"},
			lifecycle,
			nil,
			func(context.Context) (int, error) { return 6, nil },
			sqlite.OperatorCheck{
				Name: "sqlite",
				Check: func(context.Context) (store.OperatorServiceStatus, error) {
					return store.OperatorServiceStatus{Name: "sqlite", Status: "ok", Detail: "reachable"}, nil
				},
			},
		)
		return deps
	})
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := sessionRequest(t, client, http.MethodGet, srv.URL+"/ops/", "", "", "", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("unauthenticated status = %d, want 303", resp.StatusCode)
	}
	resp.Body.Close()

	resp = sessionRequest(t, client, http.MethodGet, srv.URL+"/ops/", sessionToken, csrf, "", nil)
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Operator") ||
		!strings.Contains(body, "Queue Backlog") ||
		!strings.Contains(body, "Download redacted diagnostics JSON") ||
		!strings.Contains(body, "Install State") ||
		!strings.Contains(body, "v1.2.3") ||
		!strings.Contains(body, "Install SLOs") ||
		!strings.Contains(body, "Operator Alerts") ||
		!strings.Contains(body, "run-ops-web-1") ||
		!strings.Contains(body, "replay-1") ||
		!strings.Contains(body, "maintenance.enabled") ||
		!strings.Contains(body, "backfill.requested") {
		t.Fatalf("unexpected operator body: %s", body)
	}
}
