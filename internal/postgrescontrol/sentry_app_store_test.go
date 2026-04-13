package postgrescontrol

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/integration"
)

func TestSentryAppStoreUpsertListGetDelete(t *testing.T) {
	db, _ := seedControlFixture(t)
	store := NewSentryAppStore(db)
	ctx := context.Background()

	record := &integration.AppRecord{
		ID:             "app-1",
		Slug:           "checkout-app",
		Name:           "Checkout App",
		Author:         "Dev User",
		Overview:       "Routes issues to the owning team.",
		Scopes:         []string{"event:read", "project:write"},
		Events:         []string{"issue", "event"},
		Schema:         json.RawMessage(`{"elements":[{"type":"text","name":"hello"}]}`),
		AllowedOrigins: []string{"https://example.com"},
		Status:         "published",
		RedirectURL:    "https://example.com/install",
		WebhookURL:     "https://example.com/webhook",
		IsAlertable:    true,
		VerifyInstall:  false,
	}
	if err := store.Upsert(ctx, record); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Get(ctx, "checkout-app")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.ID != record.ID || got.RedirectURL != record.RedirectURL || len(got.Scopes) != 2 {
		t.Fatalf("unexpected app record: %+v", got)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].Slug != "checkout-app" || !items[0].IsAlertable || items[0].VerifyInstall {
		t.Fatalf("unexpected app list: %+v", items)
	}

	if err := store.Delete(ctx, record.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deleted, err := store.Get(ctx, record.ID)
	if err != nil {
		t.Fatalf("Get deleted: %v", err)
	}
	if deleted == nil || deleted.Status != "deleted" || deleted.DeletedAt == nil || !deleted.Deleted() {
		t.Fatalf("unexpected deleted app record: %+v", deleted)
	}
}
