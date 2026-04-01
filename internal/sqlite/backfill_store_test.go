package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackfillStoreLifecycle(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES ('proj-1', 'org-1', 'backend', 'Backend')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	store := NewBackfillStore(db)
	run, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		DebugFileID:    "debug-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run == nil || run.ID == "" || run.Status != BackfillStatusPending {
		t.Fatalf("unexpected run: %+v", run)
	}

	duplicate, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		DebugFileID:    "debug-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun duplicate: %v", err)
	}
	if duplicate == nil || duplicate.ID != run.ID {
		t.Fatalf("duplicate = %+v, want %s", duplicate, run.ID)
	}
	distinct, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.4",
		DebugFileID:    "debug-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun distinct: %v", err)
	}
	if distinct == nil || distinct.ID == run.ID {
		t.Fatalf("distinct = %+v, want different run", distinct)
	}
	if _, err := db.Exec(`UPDATE backfill_runs SET created_at = ?, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Add(time.Second).Format(time.RFC3339),
		time.Now().UTC().Add(time.Second).Format(time.RFC3339),
		distinct.ID,
	); err != nil {
		t.Fatalf("order distinct run: %v", err)
	}

	claimed, err := store.ClaimNextRunnable(ctx, "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextRunnable: %v", err)
	}
	if claimed == nil || claimed.ID != run.ID || claimed.Status != BackfillStatusRunning {
		t.Fatalf("claimed = %+v", claimed)
	}

	if _, err := store.SetTotalItems(ctx, run.ID, "worker-1", 2); err != nil {
		t.Fatalf("SetTotalItems: %v", err)
	}
	advanced, err := store.AdvanceRun(ctx, run.ID, "worker-1", 11, 1, 1, 0, false, "")
	if err != nil {
		t.Fatalf("AdvanceRun pending: %v", err)
	}
	if advanced == nil || advanced.Status != BackfillStatusPending || advanced.ProcessedItems != 1 || advanced.UpdatedItems != 1 {
		t.Fatalf("advanced = %+v", advanced)
	}

	runs, err := store.ListRuns(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
	seen := map[string]bool{runs[0].ID: true, runs[1].ID: true}
	if !seen[run.ID] || !seen[distinct.ID] {
		t.Fatalf("runs = %+v", runs)
	}

	cancelled, err := store.CancelRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if cancelled == nil || cancelled.Status != BackfillStatusCancelled {
		t.Fatalf("cancelled = %+v", cancelled)
	}

	got, err := store.GetRun(ctx, "org-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got == nil || got.Status != BackfillStatusCancelled {
		t.Fatalf("got = %+v", got)
	}
}

func TestBackfillStoreClaimsExpiredRun(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	store := NewBackfillStore(db)
	run, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE backfill_runs SET status = 'running', lease_until = ?, worker_id = 'dead-worker' WHERE id = ?`, past, run.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	claimed, err := store.ClaimNextRunnable(ctx, "worker-2", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextRunnable: %v", err)
	}
	if claimed == nil || claimed.ID != run.ID || claimed.WorkerID != "worker-2" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if _, err := store.SetTotalItems(ctx, run.ID, "dead-worker", 10); !errors.Is(err, ErrBackfillLeaseLost) {
		t.Fatalf("SetTotalItems stale worker err = %v, want ErrBackfillLeaseLost", err)
	}
}

func TestBackfillStoreRejectsOverlappingTelemetryScopes(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES ('proj-1', 'org-1', 'backend', 'Backend'), ('proj-2', 'org-1', 'frontend', 'Frontend')`); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	store := NewBackfillStore(db)
	run, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun project scope: %v", err)
	}
	if run == nil || run.ID == "" {
		t.Fatalf("unexpected run: %+v", run)
	}

	_, err = store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		RequestedVia:   "test",
	})
	if !errors.Is(err, ErrBackfillConflict) {
		t.Fatalf("organization scope err = %v, want ErrBackfillConflict", err)
	}
	var conflict *BackfillConflictError
	if !errors.As(err, &conflict) || conflict.Run.ID != run.ID {
		t.Fatalf("conflict = %+v, want run %s", conflict, run.ID)
	}

	allowed, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		ProjectID:      "proj-2",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun different project: %v", err)
	}
	if allowed == nil || allowed.ID == run.ID {
		t.Fatalf("allowed = %+v, want distinct run", allowed)
	}
}

func TestBackfillStoreRejectsOverlappingNativeScopes(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES ('proj-1', 'org-1', 'backend', 'Backend')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	store := NewBackfillStore(db)
	exact, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		DebugFileID:    "debug-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun exact: %v", err)
	}
	duplicate, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		DebugFileID:    "debug-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun duplicate: %v", err)
	}
	if duplicate == nil || duplicate.ID != exact.ID {
		t.Fatalf("duplicate = %+v, want %s", duplicate, exact.ID)
	}

	_, err = store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.3",
		DebugFileID:    "debug-2",
		RequestedVia:   "test",
	})
	if !errors.Is(err, ErrBackfillConflict) {
		t.Fatalf("different debug file err = %v, want ErrBackfillConflict", err)
	}

	january, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.4",
		StartedAfter:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		EndedBefore:    time.Date(2026, time.January, 31, 23, 59, 59, 0, time.UTC),
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun january: %v", err)
	}
	february, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindNativeReprocess,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		ReleaseVersion: "ios@1.2.4",
		StartedAfter:   time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		EndedBefore:    time.Date(2026, time.February, 28, 23, 59, 59, 0, time.UTC),
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun february: %v", err)
	}
	if january == nil || february == nil || january.ID == february.ID {
		t.Fatalf("expected distinct bounded runs, got january=%+v february=%+v", january, february)
	}
}

func TestBackfillStoreClaimSkipsConflictingPendingRunAcrossWorkers(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES ('proj-1', 'org-1', 'backend', 'Backend'), ('proj-2', 'org-1', 'frontend', 'Frontend')`); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	store := NewBackfillStore(db)
	runOne, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun runOne: %v", err)
	}
	runTwo, err := store.CreateRun(ctx, CreateBackfillRun{
		Kind:           BackfillKindTelemetryRebuild,
		OrganizationID: "org-1",
		ProjectID:      "proj-2",
		RequestedVia:   "test",
	})
	if err != nil {
		t.Fatalf("CreateRun runTwo: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO backfill_runs
			(id, kind, status, organization_id, project_id, release_version, requested_via, created_at, updated_at)
		 VALUES ('run-conflict', 'telemetry_rebuild', 'pending', 'org-1', 'proj-1', 'legacy-overlap', 'test', ?, ?)`,
		now.Add(2*time.Second).Format(time.RFC3339),
		now.Add(2*time.Second).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed conflicting run: %v", err)
	}
	if _, err := db.Exec(`UPDATE backfill_runs SET created_at = ?, updated_at = ? WHERE id = ?`,
		now.Format(time.RFC3339), now.Format(time.RFC3339), runOne.ID); err != nil {
		t.Fatalf("order runOne: %v", err)
	}
	if _, err := db.Exec(`UPDATE backfill_runs SET created_at = ?, updated_at = ? WHERE id = ?`,
		now.Add(3*time.Second).Format(time.RFC3339), now.Add(3*time.Second).Format(time.RFC3339), runTwo.ID); err != nil {
		t.Fatalf("order runTwo: %v", err)
	}

	claimedOne, err := store.ClaimNextRunnable(ctx, "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextRunnable first: %v", err)
	}
	if claimedOne == nil || claimedOne.ID != runOne.ID {
		t.Fatalf("claimedOne = %+v, want %s", claimedOne, runOne.ID)
	}
	claimedTwo, err := store.ClaimNextRunnable(ctx, "worker-2", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextRunnable second: %v", err)
	}
	if claimedTwo == nil || claimedTwo.ID != runTwo.ID {
		t.Fatalf("claimedTwo = %+v, want %s", claimedTwo, runTwo.ID)
	}
	conflictRun, err := store.GetRun(ctx, "org-1", "run-conflict")
	if err != nil {
		t.Fatalf("GetRun conflict: %v", err)
	}
	if conflictRun == nil || conflictRun.Status != BackfillStatusPending {
		t.Fatalf("conflictRun = %+v, want pending", conflictRun)
	}
}
