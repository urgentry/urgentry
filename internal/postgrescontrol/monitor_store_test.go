package postgrescontrol

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func TestMonitorStoreUpsertGetDelete(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedProject(t, db, "proj-1", "org-1", "checkout", "Checkout")

	store := NewMonitorStore(db)

	created, err := store.UpsertMonitor(ctx, &Monitor{
		ProjectID:   "proj-1",
		Slug:        "nightly-import",
		Status:      "active",
		Environment: "production",
		Config: sqlite.MonitorConfig{
			Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 10, Unit: "minute"},
			Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatalf("UpsertMonitor create: %v", err)
	}
	if created == nil || created.ID == "" || created.Config.Schedule.Type != "interval" {
		t.Fatalf("unexpected created monitor: %+v", created)
	}

	updated, err := store.UpsertMonitor(ctx, &Monitor{
		ProjectID: "proj-1",
		Slug:      "nightly-import",
		Status:    "disabled",
		Config: sqlite.MonitorConfig{
			Schedule: sqlite.MonitorSchedule{Type: "crontab", Crontab: "*/15 * * * *"},
			Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatalf("UpsertMonitor update: %v", err)
	}
	if updated == nil || updated.Status != "disabled" || updated.Config.Schedule.Type != "crontab" {
		t.Fatalf("unexpected updated monitor: %+v", updated)
	}

	got, err := store.GetMonitor(ctx, "proj-1", "nightly-import")
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got == nil || got.Slug != "nightly-import" || got.Config.Schedule.Crontab != "*/15 * * * *" {
		t.Fatalf("unexpected fetched monitor: %+v", got)
	}

	monitors, err := store.ListMonitors(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 || monitors[0].ID != got.ID {
		t.Fatalf("unexpected monitor list: %+v", monitors)
	}

	if err := store.DeleteMonitor(ctx, "proj-1", "nightly-import"); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	got, err = store.GetMonitor(ctx, "proj-1", "nightly-import")
	if err != nil {
		t.Fatalf("GetMonitor after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected monitor to be deleted, got %+v", got)
	}
}

func TestMonitorStoreSaveCheckInAndMarkMissed(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedProject(t, db, "proj-1", "org-1", "checkout", "Checkout")

	store := NewMonitorStore(db)
	base := time.Now().UTC().Add(-10 * time.Minute)

	monitor, err := store.SaveCheckIn(ctx, &MonitorCheckIn{
		ProjectID:   "proj-1",
		CheckInID:   "check-in-1",
		MonitorSlug: "every-minute",
		Status:      "ok",
		Duration:    4.2,
		Environment: "production",
		DateCreated: base,
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 1, Unit: "minute"},
		Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}
	if monitor == nil || monitor.LastStatus != "ok" || monitor.NextCheckInAt.IsZero() {
		t.Fatalf("unexpected monitor after check-in: %+v", monitor)
	}

	checkIns, err := store.ListCheckIns(ctx, "proj-1", "every-minute", 10)
	if err != nil {
		t.Fatalf("ListCheckIns: %v", err)
	}
	if len(checkIns) != 1 || checkIns[0].CheckInID != "check-in-1" {
		t.Fatalf("unexpected initial check-ins: %+v", checkIns)
	}
	if checkIns[0].Duration != 4.2 {
		t.Fatalf("initial check-in duration = %v, want 4.2", checkIns[0].Duration)
	}

	missed, err := store.MarkMissed(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkMissed: %v", err)
	}
	if len(missed) != 1 || missed[0].Status != "missed" {
		t.Fatalf("unexpected missed check-ins: %+v", missed)
	}

	checkIns, err = store.ListCheckIns(ctx, "proj-1", "every-minute", 10)
	if err != nil {
		t.Fatalf("ListCheckIns after missed: %v", err)
	}
	if len(checkIns) != 2 || checkIns[0].Status != "missed" {
		t.Fatalf("unexpected check-ins after missed: %+v", checkIns)
	}

	monitor, err = store.GetMonitor(ctx, "proj-1", "every-minute")
	if err != nil {
		t.Fatalf("GetMonitor after missed: %v", err)
	}
	if monitor == nil || monitor.LastStatus != "missed" || monitor.NextCheckInAt.IsZero() {
		t.Fatalf("unexpected monitor after missed: %+v", monitor)
	}
}

func TestMonitorStoreListOrgMonitors(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedOrganization(t, db, "org-2", "other", "Other")
	seedProject(t, db, "proj-1", "org-1", "checkout", "Checkout")
	seedProject(t, db, "proj-2", "org-1", "payments", "Payments")
	seedProject(t, db, "proj-3", "org-2", "mobile", "Mobile")

	store := NewMonitorStore(db)
	for _, monitor := range []Monitor{
		{ProjectID: "proj-1", Slug: "nightly-import", Status: "active"},
		{ProjectID: "proj-2", Slug: "hourly-cleanup", Status: "active"},
		{ProjectID: "proj-3", Slug: "foreign-monitor", Status: "active"},
	} {
		if _, err := store.UpsertMonitor(ctx, &monitor); err != nil {
			t.Fatalf("UpsertMonitor(%s): %v", monitor.Slug, err)
		}
	}

	monitors, err := store.ListOrgMonitors(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("ListOrgMonitors: %v", err)
	}
	if len(monitors) != 2 {
		t.Fatalf("len(monitors) = %d, want 2", len(monitors))
	}
	projectBySlug := map[string]string{}
	for _, monitor := range monitors {
		projectBySlug[monitor.Slug] = monitor.ProjectID
	}
	if projectBySlug["nightly-import"] != "proj-1" || projectBySlug["hourly-cleanup"] != "proj-2" {
		t.Fatalf("unexpected org monitors: %+v", monitors)
	}
}

func TestNextCronOccurrence(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 3, 27, 10, 5, 0, 0, time.UTC)
	next, ok := nextCronOccurrence(start, "*/15 * * * *", "UTC")
	if !ok {
		t.Fatal("expected cron match")
	}
	want := time.Date(2026, 3, 27, 10, 15, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func seedProject(t *testing.T, db *sql.DB, id, orgID, slug, name string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at, updated_at) VALUES ($1, $2, $3, $4, 'go', 'active', $5, $5)`, id, orgID, slug, name, time.Now().UTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}
