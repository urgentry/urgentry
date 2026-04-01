package attachment

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreSaveGetList(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	createdAt := time.Now().UTC()
	attachment := &Attachment{
		ID:          "att-1",
		EventID:     "evt-1",
		ProjectID:   "proj-1",
		Name:        "report.txt",
		ContentType: "text/plain",
		Size:        12,
		ObjectKey:   "attachments/proj-1/evt-1/report.txt",
		CreatedAt:   createdAt,
	}
	body := []byte("hello world!")

	if err := store.SaveAttachment(ctx, attachment, body); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	got, payload, err := store.GetAttachment(ctx, "att-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got == nil || got.Name != "report.txt" {
		t.Fatalf("GetAttachment = %+v", got)
	}
	if string(payload) != string(body) {
		t.Fatalf("payload = %q, want %q", payload, body)
	}

	items, err := store.ListByEvent(ctx, "evt-1")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(items) != 1 || items[0].ID != "att-1" {
		t.Fatalf("ListByEvent = %+v", items)
	}

	missing, payload, err := store.GetAttachment(ctx, "missing")
	if err != nil {
		t.Fatalf("GetAttachment missing: %v", err)
	}
	if missing != nil || payload != nil {
		t.Fatalf("missing attachment = %+v payload=%v", missing, payload)
	}
}
