package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestMonitorStoreSaveCheckInAndList(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")

	store := NewMonitorStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	monitor, err := store.SaveCheckIn(ctx, &MonitorCheckIn{
		ProjectID:   "proj-1",
		CheckInID:   "check-in-1",
		MonitorSlug: "nightly-import",
		Status:      "ok",
		Environment: "production",
		DateCreated: now,
	}, &MonitorConfig{
		Schedule: MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}
	if monitor.ID == "" {
		t.Fatal("expected monitor ID")
	}
	if monitor.NextCheckInAt.IsZero() {
		t.Fatal("expected next check-in time")
	}

	monitors, err := store.ListMonitors(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 || monitors[0].Slug != "nightly-import" {
		t.Fatalf("unexpected monitors: %+v", monitors)
	}

	checkIns, err := store.ListCheckIns(ctx, "proj-1", "nightly-import", 10)
	if err != nil {
		t.Fatalf("ListCheckIns: %v", err)
	}
	if len(checkIns) != 1 || checkIns[0].CheckInID != "check-in-1" {
		t.Fatalf("unexpected check-ins: %+v", checkIns)
	}
}

func TestMonitorStoreMarkMissed(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")

	store := NewMonitorStore(db)
	ctx := context.Background()
	base := time.Now().UTC().Add(-10 * time.Minute)

	monitor, err := store.SaveCheckIn(ctx, &MonitorCheckIn{
		ProjectID:   "proj-1",
		CheckInID:   "check-in-1",
		MonitorSlug: "every-minute",
		Status:      "ok",
		DateCreated: base,
	}, &MonitorConfig{
		Schedule: MonitorSchedule{Type: "interval", Value: 1, Unit: "minute"},
		Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("SaveCheckIn: %v", err)
	}

	missed, err := store.MarkMissed(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkMissed: %v", err)
	}
	if len(missed) != 1 {
		t.Fatalf("len(missed) = %d, want 1", len(missed))
	}

	checkIns, err := store.ListCheckIns(ctx, "proj-1", "every-minute", 10)
	if err != nil {
		t.Fatalf("ListCheckIns: %v", err)
	}
	if len(checkIns) != 2 {
		t.Fatalf("len(checkIns) = %d, want 2", len(checkIns))
	}
	if checkIns[0].Status != "missed" {
		t.Fatalf("latest status = %q, want missed", checkIns[0].Status)
	}

	monitors, err := store.ListMonitors(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("len(monitors) = %d, want 1", len(monitors))
	}
	if monitors[0].ID != monitor.ID {
		t.Fatalf("monitor ID = %q, want %q", monitors[0].ID, monitor.ID)
	}
	if monitors[0].LastStatus != "missed" {
		t.Fatalf("LastStatus = %q, want missed", monitors[0].LastStatus)
	}
	if monitors[0].NextCheckInAt.IsZero() {
		t.Fatal("expected next check-in after missed update")
	}
}

func TestMonitorStoreUpsertGetDelete(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")

	store := NewMonitorStore(db)
	ctx := context.Background()
	created, err := store.UpsertMonitor(ctx, &Monitor{
		ProjectID:   "proj-1",
		Slug:        "nightly-import",
		Status:      "active",
		Environment: "production",
		Config: MonitorConfig{
			Schedule: MonitorSchedule{Type: "interval", Value: 10, Unit: "minute"},
			Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatalf("UpsertMonitor create: %v", err)
	}
	if created == nil || created.ID == "" {
		t.Fatalf("created monitor = %+v", created)
	}

	updated, err := store.UpsertMonitor(ctx, &Monitor{
		ProjectID:   "proj-1",
		Slug:        "nightly-import",
		Status:      "disabled",
		Environment: "staging",
		Config: MonitorConfig{
			Schedule: MonitorSchedule{Type: "crontab", Crontab: "*/15 * * * *"},
			Timezone: "UTC",
		},
	})
	if err != nil {
		t.Fatalf("UpsertMonitor update: %v", err)
	}
	if updated.Status != "disabled" || updated.Config.Schedule.Type != "crontab" {
		t.Fatalf("updated monitor = %+v", updated)
	}

	got, err := store.GetMonitor(ctx, "proj-1", "nightly-import")
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got == nil || got.Slug != "nightly-import" || got.Status != "disabled" {
		t.Fatalf("GetMonitor = %+v", got)
	}

	if err := store.DeleteMonitor(ctx, "proj-1", "nightly-import"); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	got, err = store.GetMonitor(ctx, "proj-1", "nightly-import")
	if err != nil {
		t.Fatalf("GetMonitor after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestMonitorStoreListOrgMonitors(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-2', 'org-1', 'payments', 'Payments', 'go', 'active')`); err != nil {
		t.Fatalf("insert second org project: %v", err)
	}
	seedReleaseHealthProject(t, db, "org-2", "other", "proj-3", "mobile")

	store := NewMonitorStore(db)
	ctx := context.Background()
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
