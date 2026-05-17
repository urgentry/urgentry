package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestQuotaUsageCountsRejectedOutcomesByReceiptTime(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at)
		 VALUES ('proj-1', 'org-1', 'app', 'App', 'go', 'active', ?)`,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := NewOutcomeStore(db).SaveOutcome(context.Background(), &Outcome{
		ProjectID:   "proj-1",
		Category:    "error",
		Reason:      "sample_rate",
		Quantity:    5,
		RecordedAt:  now.AddDate(0, -2, 0),
		DateCreated: now,
	}); err != nil {
		t.Fatalf("SaveOutcome() error = %v", err)
	}

	usage, err := NewQuotaStore(db).GetUsage(context.Background(), "proj-1", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.EventsRejected != 5 {
		t.Fatalf("GetUsage().EventsRejected = %d, want 5", usage.EventsRejected)
	}

	items, err := NewQuotaStore(db).GetAllProjectUsage(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("GetAllProjectUsage() error = %v", err)
	}
	if len(items) != 1 || items[0].EventsRejected != 5 {
		t.Fatalf("GetAllProjectUsage() = %+v, want one project with 5 rejected events", items)
	}
}
