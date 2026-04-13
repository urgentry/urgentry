package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestAnalyticsReportScheduleStoreLifecycle(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewAnalyticsReportScheduleStore(db)
	ctx := context.Background()
	item, err := store.Create(ctx, "acme", "saved_query", "search-1", "user-1", "ops@example.com", AnalyticsReportCadenceDaily)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	items, err := store.ListBySource(ctx, "acme", "saved_query", "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource: %v", err)
	}
	if len(items) != 1 || items[0].Recipient != "ops@example.com" || items[0].Cadence != AnalyticsReportCadenceDaily {
		t.Fatalf("unexpected schedules: %+v", items)
	}

	due, err := store.ListDue(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 || due[0].ID != item.ID {
		t.Fatalf("unexpected due schedules: %+v", due)
	}

	now := time.Now().UTC()
	if err := store.MarkDelivered(ctx, item.ID, now, AnalyticsReportCadenceDaily, "token-1"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	items, err = store.ListBySource(ctx, "acme", "saved_query", "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource after delivery: %v", err)
	}
	if items[0].LastRunAt == nil || items[0].LastSnapshotToken != "token-1" || items[0].LastError != "" {
		t.Fatalf("unexpected delivered schedule: %+v", items[0])
	}

	if err := store.MarkFailed(ctx, item.ID, now, AnalyticsReportCadenceWeekly, "query failed"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	items, err = store.ListBySource(ctx, "acme", "saved_query", "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource after failure: %v", err)
	}
	if items[0].LastError != "query failed" {
		t.Fatalf("unexpected last error: %+v", items[0])
	}
	if items[0].NextRunAt.Before(now.Add(6 * 24 * time.Hour)) {
		t.Fatalf("expected weekly next run after failure, got %s", items[0].NextRunAt)
	}

	if err := store.Delete(ctx, "acme", "user-1", item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	items, err = store.ListBySource(ctx, "acme", "saved_query", "search-1", "user-1")
	if err != nil {
		t.Fatalf("ListBySource after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no schedules, got %+v", items)
	}
}
