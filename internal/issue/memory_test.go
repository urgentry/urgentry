package issue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"urgentry/internal/store"
)

func newTestGroup(id, projectID, key string) *Group {
	now := time.Now().UTC()
	return &Group{
		ID:              id,
		ProjectID:       projectID,
		GroupingVersion: "urgentry-v1",
		GroupingKey:     key,
		Title:           "ValueError: bad input",
		Culprit:         "app.views in handle_request",
		Level:           "error",
		Status:          "unresolved",
		FirstSeen:       now,
		LastSeen:        now,
		TimesSeen:       1,
		LastEventID:     "evt-1",
	}
}

func TestMemoryGroupStore_CreateNew(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	g := newTestGroup("grp-1", "proj-1", "abc123")
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	got, err := gs.GetGroup(ctx, "grp-1")
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if got.Title != "ValueError: bad input" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.TimesSeen != 1 {
		t.Fatalf("TimesSeen = %d, want 1", got.TimesSeen)
	}
	if got.Status != "unresolved" {
		t.Fatalf("Status = %q, want unresolved", got.Status)
	}
}

func TestMemoryGroupStore_UpsertIncrementsCounter(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	g := newTestGroup("grp-1", "proj-1", "abc123")
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatal(err)
	}

	// Upsert same key again
	g2 := &Group{
		ID:              "grp-ignored", // ID ignored on upsert match
		ProjectID:       "proj-1",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "abc123",
		Title:           "ValueError: bad input (updated)",
		LastSeen:        time.Now().UTC().Add(time.Minute),
		LastEventID:     "evt-2",
		Level:           "error",
	}
	if err := gs.UpsertGroup(ctx, g2); err != nil {
		t.Fatal(err)
	}

	// Should have gotten the real ID back
	if g2.ID != "grp-1" {
		t.Fatalf("ID = %q, want grp-1", g2.ID)
	}
	if g2.TimesSeen != 2 {
		t.Fatalf("TimesSeen = %d, want 2", g2.TimesSeen)
	}
	if g2.Title != "ValueError: bad input (updated)" {
		t.Fatalf("Title = %q", g2.Title)
	}

	// Verify via GetGroupByKey
	got, err := gs.GetGroupByKey(ctx, "proj-1", "urgentry-v1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.TimesSeen != 2 {
		t.Fatalf("TimesSeen via GetGroupByKey = %d, want 2", got.TimesSeen)
	}
	if got.LastEventID != "evt-2" {
		t.Fatalf("LastEventID = %q, want evt-2", got.LastEventID)
	}
}

func TestMemoryGroupStore_ConcurrentUpsert(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	// Seed the group
	g := newTestGroup("grp-1", "proj-1", "concurrency-key")
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatal(err)
	}

	// Spawn 100 goroutines upserting the same group key
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			u := &Group{
				ID:              fmt.Sprintf("ignored-%d", n),
				ProjectID:       "proj-1",
				GroupingVersion: "urgentry-v1",
				GroupingKey:     "concurrency-key",
				Title:           "concurrent",
				LastSeen:        time.Now().UTC(),
				LastEventID:     fmt.Sprintf("evt-%d", n),
				Level:           "error",
			}
			if err := gs.UpsertGroup(ctx, u); err != nil {
				t.Errorf("goroutine %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := gs.GetGroup(ctx, "grp-1")
	if err != nil {
		t.Fatal(err)
	}
	// 1 initial + 100 upserts = 101
	if got.TimesSeen != 101 {
		t.Fatalf("TimesSeen = %d, want 101", got.TimesSeen)
	}
}

func TestMemoryGroupStore_ListWithFilters(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		g := &Group{
			ID:              fmt.Sprintf("grp-%d", i),
			ProjectID:       "proj-1",
			GroupingVersion: "urgentry-v1",
			GroupingKey:     fmt.Sprintf("key-%d", i),
			Title:           fmt.Sprintf("Error %d", i),
			Level:           "error",
			Status:          "unresolved",
			FirstSeen:       base.Add(time.Duration(i) * time.Hour),
			LastSeen:        base.Add(time.Duration(i) * time.Hour),
			LastEventID:     fmt.Sprintf("evt-%d", i),
		}
		if i == 3 {
			g.Status = "resolved"
		}
		if err := gs.UpsertGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	// List all
	groups, err := gs.ListGroups(ctx, "proj-1", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 5 {
		t.Fatalf("len = %d, want 5", len(groups))
	}

	// Filter by status=unresolved
	groups, err = gs.ListGroups(ctx, "proj-1", ListOpts{Status: "unresolved"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 4 {
		t.Fatalf("unresolved count = %d, want 4", len(groups))
	}

	// Filter by status=resolved
	groups, err = gs.ListGroups(ctx, "proj-1", ListOpts{Status: "resolved"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("resolved count = %d, want 1", len(groups))
	}

	// Limit
	groups, err = gs.ListGroups(ctx, "proj-1", ListOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("limited count = %d, want 2", len(groups))
	}

	// Wrong project returns nothing
	groups, err = gs.ListGroups(ctx, "proj-nope", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("wrong project count = %d, want 0", len(groups))
	}
}

func TestMemoryGroupStore_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	g := newTestGroup("grp-1", "proj-1", "status-key")
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatal(err)
	}

	// Resolve
	if err := gs.UpdateStatus(ctx, "grp-1", "resolved"); err != nil {
		t.Fatal(err)
	}
	got, _ := gs.GetGroup(ctx, "grp-1")
	if got.Status != "resolved" {
		t.Fatalf("Status = %q, want resolved", got.Status)
	}

	// Ignore
	if err := gs.UpdateStatus(ctx, "grp-1", "ignored"); err != nil {
		t.Fatal(err)
	}
	got, _ = gs.GetGroup(ctx, "grp-1")
	if got.Status != "ignored" {
		t.Fatalf("Status = %q, want ignored", got.Status)
	}

	// Back to unresolved
	if err := gs.UpdateStatus(ctx, "grp-1", "unresolved"); err != nil {
		t.Fatal(err)
	}
	got, _ = gs.GetGroup(ctx, "grp-1")
	if got.Status != "unresolved" {
		t.Fatalf("Status = %q, want unresolved", got.Status)
	}

	// Invalid status
	if err := gs.UpdateStatus(ctx, "grp-1", "bogus"); err == nil {
		t.Fatal("expected error for invalid status")
	}

	// Not found
	if err := gs.UpdateStatus(ctx, "nonexistent", "resolved"); err != store.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestMemoryGroupStore_GetGroupByKey_NotFound(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	_, err := gs.GetGroupByKey(ctx, "proj-1", "v1", "nonexistent")
	if err != store.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestMemoryGroupStore_GetGroup_NotFound(t *testing.T) {
	ctx := context.Background()
	gs := NewMemoryGroupStore()

	_, err := gs.GetGroup(ctx, "nonexistent")
	if err != store.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
