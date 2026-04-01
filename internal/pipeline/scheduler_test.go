package pipeline

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestSchedulerRunOnceMarksMissedMonitors(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	jobStore := sqlite.NewJobStore(db)
	monitorStore := sqlite.NewMonitorStore(db)

	if _, err := monitorStore.SaveCheckIn(context.Background(), &sqlite.MonitorCheckIn{
		ProjectID:   "proj-1",
		CheckInID:   "check-in-1",
		MonitorSlug: "every-minute",
		Status:      "ok",
		DateCreated: time.Now().UTC().Add(-10 * time.Minute),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 1, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}

	scheduler := NewScheduler(jobStore, jobStore, nil, monitorStore, "scheduler-test", nil)
	scheduler.runOnce(context.Background())

	checkIns, err := monitorStore.ListCheckIns(context.Background(), "proj-1", "every-minute", 10)
	if err != nil {
		t.Fatalf("ListCheckIns: %v", err)
	}
	if len(checkIns) < 2 {
		t.Fatalf("len(checkIns) = %d, want at least 2", len(checkIns))
	}
	if checkIns[0].Status != "missed" {
		t.Fatalf("latest status = %q, want missed", checkIns[0].Status)
	}
}

func TestSchedulerRunOnceAppliesRetentionSweep(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	jobStore := sqlite.NewJobStore(db)
	retention := sqlite.NewRetentionStore(db, nil)
	old := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)

	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, first_seen, last_seen, times_seen)
		 VALUES ('grp-1', 'proj-1', 'urgentry-v1', 'grp-1', 'Old issue', ?, ?, 1)`,
		old, old,
	); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, title, ingested_at, event_type)
		 VALUES ('evt-1', 'proj-1', 'evt-1', 'grp-1', 'Old issue', ?, 'error')`,
		old,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_retention_policies (project_id, surface, retention_days, storage_tier, archive_retention_days)
		 VALUES ('proj-1', ?, 1, ?, 0)`,
		string(store.TelemetrySurfaceErrors), string(store.TelemetryStorageTierDelete),
	); err != nil {
		t.Fatalf("insert telemetry policy: %v", err)
	}

	scheduler := NewScheduler(jobStore, jobStore, retention, nil, "scheduler-test", nil)
	scheduler.runOnce(context.Background())

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'evt-1'`).Scan(&remaining); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining events = %d, want 0", remaining)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM groups WHERE id = 'grp-1'`).Scan(&remaining); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining groups = %d, want 0", remaining)
	}
}

func TestSchedulerRunOnceEnqueuesBackfillTick(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	jobStore := sqlite.NewJobStore(db)
	backfills := &recordingKeyedEnqueuer{}

	scheduler := NewScheduler(jobStore, jobStore, nil, nil, "scheduler-test", nil)
	scheduler.SetBackfillEnqueuer(backfills)
	scheduler.runOnce(context.Background())

	if len(backfills.calls) != 1 {
		t.Fatalf("len(backfills.calls) = %d, want 1", len(backfills.calls))
	}
	call := backfills.calls[0]
	if call.kind != sqlite.JobKindBackfill || call.dedupeKey != "backfill:tick" || string(call.payload) != "{}" {
		t.Fatalf("unexpected backfill enqueue: %+v", call)
	}
}

func TestSchedulerRunOnceExecutesAnalyticsReports(t *testing.T) {
	db := sqliteOpenForSchedulerTest(t)
	jobStore := sqlite.NewJobStore(db)
	reports := &recordingReportRunner{}

	scheduler := NewScheduler(jobStore, jobStore, nil, nil, "scheduler-test", nil)
	scheduler.SetReportRunner(reports)
	scheduler.runOnce(context.Background())

	if reports.calls != 1 {
		t.Fatalf("reports.calls = %d, want 1", reports.calls)
	}
}

type recordingKeyedEnqueuer struct {
	calls []recordingEnqueueCall
}

type recordingReportRunner struct {
	calls int
}

type recordingEnqueueCall struct {
	kind      string
	projectID string
	dedupeKey string
	payload   []byte
	limit     int
}

func (r *recordingKeyedEnqueuer) EnqueueKeyed(_ context.Context, kind, projectID, dedupeKey string, payload []byte, limit int) (bool, error) {
	r.calls = append(r.calls, recordingEnqueueCall{
		kind:      kind,
		projectID: projectID,
		dedupeKey: dedupeKey,
		payload:   append([]byte(nil), payload...),
		limit:     limit,
	})
	return true, nil
}

func (r *recordingReportRunner) RunDue(_ context.Context, _ time.Time) error {
	r.calls++
	return nil
}

var _ runtimeasync.KeyedEnqueuer = (*recordingKeyedEnqueuer)(nil)

func sqliteOpenForSchedulerTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES ('proj-1', 'org-1', 'backend', 'Backend')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
