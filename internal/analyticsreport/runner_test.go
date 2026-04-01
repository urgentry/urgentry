package analyticsreport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

type fakeFreezer struct {
	saved  *SourceSnapshot
	widget *SourceSnapshot
	err    error
}

func (f fakeFreezer) FreezeSavedQuery(_ context.Context, _, _, _ string) (*SourceSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.saved, nil
}

func (f fakeFreezer) FreezeDashboardWidget(_ context.Context, _, _, _ string) (*SourceSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.widget, nil
}

func TestRunnerRunDueQueuesFrozenSnapshotEmail(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schedules := sqlite.NewAnalyticsReportScheduleStore(db)
	outbox := sqlite.NewNotificationOutboxStore(db)
	deliveries := sqlite.NewNotificationDeliveryStore(db)
	ctx := context.Background()
	if _, err := schedules.Create(ctx, "acme", SourceTypeSavedQuery, "search-1", "user-1", "ops@example.com", sqlite.AnalyticsReportCadenceDaily); err != nil {
		t.Fatalf("Create: %v", err)
	}

	now := time.Now().UTC()
	runner := &Runner{
		Schedules: schedules,
		Freezer: fakeFreezer{saved: &SourceSnapshot{
			Snapshot: &sqlite.AnalyticsSnapshot{
				ID:         "snap-1",
				Title:      "Checkout traces",
				ShareToken: "token-1",
				CreatedAt:  now,
				Body: sqlite.SnapshotBody{
					Filters:   []string{"env:production"},
					CostLabel: "Estimated planner cost: cheap (1)",
				},
			},
			ProjectID:  "proj-1",
			SourceName: "Checkout traces",
		}},
		Outbox:     outbox,
		Deliveries: deliveries,
		BaseURL:    "https://urgentry.example",
	}
	if err := runner.RunDue(ctx, now); err != nil {
		t.Fatalf("RunDue: %v", err)
	}

	emails, err := outbox.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent outbox: %v", err)
	}
	if len(emails) != 1 {
		t.Fatalf("len(emails) = %d, want 1", len(emails))
	}
	if emails[0].Transport != "tiny-report" || !strings.Contains(emails[0].Body, "https://urgentry.example/analytics/snapshots/token-1/") {
		t.Fatalf("unexpected email: %+v", emails[0])
	}

	items, err := schedules.ListBySource(ctx, "acme", SourceTypeSavedQuery, "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource: %v", err)
	}
	if len(items) != 1 || items[0].LastSnapshotToken != "token-1" || items[0].LastRunAt == nil || items[0].LastError != "" {
		t.Fatalf("unexpected schedules after delivery: %+v", items)
	}
}

func TestRunnerRunDueMarksFailures(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schedules := sqlite.NewAnalyticsReportScheduleStore(db)
	outbox := sqlite.NewNotificationOutboxStore(db)
	ctx := context.Background()
	if _, err := schedules.Create(ctx, "acme", SourceTypeSavedQuery, "search-1", "user-1", "ops@example.com", sqlite.AnalyticsReportCadenceDaily); err != nil {
		t.Fatalf("Create: %v", err)
	}

	now := time.Now().UTC()
	runner := &Runner{
		Schedules: schedules,
		Freezer:   fakeFreezer{err: errors.New("source deleted")},
		Outbox:    outbox,
	}
	if err := runner.RunDue(ctx, now); err != nil {
		t.Fatalf("RunDue: %v", err)
	}

	emails, err := outbox.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent outbox: %v", err)
	}
	if len(emails) != 0 {
		t.Fatalf("expected no emails, got %+v", emails)
	}

	items, err := schedules.ListBySource(ctx, "acme", SourceTypeSavedQuery, "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource: %v", err)
	}
	if len(items) != 1 || items[0].LastError != "source deleted" {
		t.Fatalf("unexpected failure state: %+v", items)
	}
}
