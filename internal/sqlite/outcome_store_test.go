package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestOutcomeStoreSaveAndList(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")

	store := NewOutcomeStore(db)
	ctx := context.Background()
	recordedAt := time.Now().UTC().Add(-time.Minute)

	if err := store.SaveOutcome(ctx, &Outcome{
		ProjectID:   "proj-1",
		EventID:     "evt-1",
		Category:    "error",
		Reason:      "sample_rate",
		Quantity:    5,
		Environment: "production",
		Release:     "api@1.2.3",
		PayloadJSON: json.RawMessage(`{"reason":"sample_rate"}`),
		RecordedAt:  recordedAt,
	}); err != nil {
		t.Fatalf("SaveOutcome: %v", err)
	}

	items, err := store.ListRecent(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Quantity != 5 {
		t.Fatalf("Quantity = %d, want 5", items[0].Quantity)
	}
	if items[0].Category != "error" || items[0].Reason != "sample_rate" {
		t.Fatalf("unexpected outcome: %+v", items[0])
	}
	if items[0].RecordedAt.IsZero() || !items[0].RecordedAt.Equal(recordedAt.Truncate(time.Second)) {
		t.Fatalf("RecordedAt = %v, want %v", items[0].RecordedAt, recordedAt.Truncate(time.Second))
	}
}
