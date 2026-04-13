package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestAnalyticsSnapshotStoreCreateAndLoad(t *testing.T) {
	db := openStoreTestDB(t)
	store := NewAnalyticsSnapshotStore(db)

	item, err := store.Create(context.Background(), "test-org", "user-1", "saved_query", "search-1", "Ops snapshot", SnapshotBody{
		ViewType: "table",
		Columns:  []string{"project", "count"},
		Rows: [][]string{
			{"frontend", "4"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.ShareToken == "" {
		t.Fatal("expected share token")
	}
	loaded, err := store.GetByShareToken(context.Background(), item.ShareToken)
	if err != nil {
		t.Fatalf("GetByShareToken: %v", err)
	}
	if loaded.Title != item.Title || loaded.Body.Rows[0][0] != "frontend" {
		t.Fatalf("loaded snapshot = %+v", loaded)
	}
}

func TestAnalyticsSnapshotStoreDeletesExpiredRows(t *testing.T) {
	db := openStoreTestDB(t)
	store := NewAnalyticsSnapshotStore(db)
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO analytics_snapshots
			(id, organization_slug, source_type, source_id, title, share_token, payload_json, created_by_user_id, created_at, expires_at)
		 VALUES
			('snap-old', 'test-org', 'saved_query', 'search-1', 'Old', 'token-old', '{}', 'user-1', ?, ?)`,
		now.Add(-48*time.Hour).Format(time.RFC3339),
		now.Add(-time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed expired snapshot: %v", err)
	}
	if err := store.DeleteExpired(context.Background()); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if _, err := store.GetByShareToken(context.Background(), "token-old"); err != ErrAnalyticsSnapshotNotFound {
		t.Fatalf("GetByShareToken expired err = %v, want %v", err, ErrAnalyticsSnapshotNotFound)
	}
}
