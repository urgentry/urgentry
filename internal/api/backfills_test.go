package api

import (
	"net/http"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func TestAPIBackfillLifecycle(t *testing.T) {
	ts, db, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	create := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind":           "native_reprocess",
		"projectSlug":    "test-project",
		"releaseVersion": "ios@1.2.3",
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202", create.StatusCode)
	}
	var created BackfillRun
	decodeBody(t, create, &created)
	if created.ID == "" || created.Kind != "native_reprocess" || created.Status != "pending" {
		t.Fatalf("unexpected created run: %+v", created)
	}
	if created.ProjectID != "test-proj-id" || created.ReleaseVersion != "ios@1.2.3" {
		t.Fatalf("unexpected created scope: %+v", created)
	}
	duplicate := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind":           "native_reprocess",
		"projectSlug":    "test-project",
		"releaseVersion": "ios@1.2.3",
	})
	if duplicate.StatusCode != http.StatusAccepted {
		t.Fatalf("duplicate status = %d, want 202", duplicate.StatusCode)
	}
	var duplicateRun BackfillRun
	decodeBody(t, duplicate, &duplicateRun)
	if duplicateRun.ID != created.ID {
		t.Fatalf("duplicate run id = %q, want %q", duplicateRun.ID, created.ID)
	}

	list := sessionJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/backfills/", sessionToken, "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.StatusCode)
	}
	var runs []BackfillRun
	decodeBody(t, list, &runs)
	if len(runs) != 1 || runs[0].ID != created.ID {
		t.Fatalf("unexpected runs: %+v", runs)
	}

	detail := sessionJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/backfills/"+created.ID+"/", sessionToken, "", nil)
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.StatusCode)
	}
	var loaded BackfillRun
	decodeBody(t, detail, &loaded)
	if loaded.ID != created.ID || loaded.ReleaseVersion != "ios@1.2.3" || loaded.ProjectID != "test-proj-id" {
		t.Fatalf("unexpected run detail: %+v", loaded)
	}

	cancel := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/"+created.ID+"/cancel/", sessionToken, csrf, nil)
	if cancel.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want 202", cancel.StatusCode)
	}
	var cancelled BackfillRun
	decodeBody(t, cancel, &cancelled)
	if cancelled.Status != "cancelled" {
		t.Fatalf("unexpected cancelled run: %+v", cancelled)
	}
	audit := sessionJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/audit-logs/", sessionToken, "", nil)
	if audit.StatusCode != http.StatusOK {
		t.Fatalf("audit status = %d, want 200", audit.StatusCode)
	}
	var logs []AuditLogEntry
	decodeBody(t, audit, &logs)
	requested, cancelledSeen := false, false
	for _, item := range logs {
		if item.Action == "native.reprocess.requested" {
			requested = true
		}
		if item.Action == "native.reprocess.cancelled" {
			cancelledSeen = true
		}
	}
	if !requested || !cancelledSeen {
		t.Fatalf("expected audit log actions, got %+v", logs)
	}
	operatorLogs, err := sqlite.NewOperatorAuditStore(db).List(t.Context(), "test-org", 10)
	if err != nil {
		t.Fatalf("List() operator logs error = %v", err)
	}
	requested, cancelledSeen = false, false
	for _, item := range operatorLogs {
		if item.Action == "native.reprocess.requested" {
			requested = true
		}
		if item.Action == "native.reprocess.cancelled" {
			cancelledSeen = true
		}
	}
	if !requested || !cancelledSeen {
		t.Fatalf("expected operator ledger actions, got %+v", operatorLogs)
	}

	if _, err := db.Exec(`INSERT INTO backfill_runs (id, kind, status, organization_id, project_id, release_version, worker_id, lease_until, requested_via) VALUES ('run-active', 'native_reprocess', 'running', 'test-org-id', 'test-proj-id', 'ios@1.2.3', 'worker-1', ?, 'test')`, time.Now().UTC().Add(time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatalf("insert running run: %v", err)
	}
	conflict := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/run-active/cancel/", sessionToken, csrf, nil)
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("cancel running status = %d, want 409", conflict.StatusCode)
	}
}

func TestAPICreateBackfillRequiresBoundedScope(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	resp := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind": "native_reprocess",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := decodeAPIError(t, resp)
	if body.Code != "backfill_scope_required" {
		t.Fatalf("error body = %+v, want backfill_scope_required", body)
	}
}

func TestAPICreateTelemetryRebuildAllowsOrganizationScope(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	resp := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind": "telemetry_rebuild",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var created BackfillRun
	decodeBody(t, resp, &created)
	if created.Kind != "telemetry_rebuild" || created.ProjectID != "" || created.Status != "pending" {
		t.Fatalf("unexpected telemetry rebuild run: %+v", created)
	}
}

func TestAPICreateTelemetryRebuildRejectsReleaseAndTimeBounds(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	for _, body := range []map[string]any{
		{
			"kind":           "telemetry_rebuild",
			"releaseVersion": "ios@1.2.3",
		},
		{
			"kind":         "telemetry_rebuild",
			"startedAfter": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		},
	} {
		resp := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for body %+v", resp.StatusCode, body)
		}
		errBody := decodeAPIError(t, resp)
		if errBody.Code != "invalid_telemetry_rebuild_scope" {
			t.Fatalf("error body = %+v, want invalid_telemetry_rebuild_scope", errBody)
		}
	}
}

func TestAPICreateBackfillRejectsConflictingTelemetryRebuild(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	first := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind":        "telemetry_rebuild",
		"projectSlug": "test-project",
	})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.StatusCode)
	}

	conflict := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind": "telemetry_rebuild",
	})
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflict.StatusCode)
	}
	body := decodeAPIError(t, conflict)
	if body.Code != "backfill_conflict" {
		t.Fatalf("error body = %+v, want backfill_conflict", body)
	}
}

func TestAPICreateBackfillRejectsConflictingNativeScope(t *testing.T) {
	ts, _, sessionToken, csrf := newSessionAuthorizedAPIServer(t)
	defer ts.Close()

	first := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind":           "native_reprocess",
		"projectSlug":    "test-project",
		"releaseVersion": "ios@1.2.3",
	})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.StatusCode)
	}

	conflict := sessionJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/backfills/", sessionToken, csrf, map[string]any{
		"kind":        "native_reprocess",
		"projectSlug": "test-project",
	})
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflict.StatusCode)
	}
}
