package sqlite

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/store"
)

func TestOpenCreatesDirAndDB(t *testing.T) {
	dir := t.TempDir() + "/sub/deep"
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify migrations ran by checking for the _migrations table.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("query _migrations: %v", err)
	}
	if count != len(migrations) {
		t.Errorf("expected %d migrations applied, got %d", len(migrations), count)
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	// Open twice to verify migrations don't fail on re-run.
	db1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("query _migrations count: %v", err)
	}
	if count != len(migrations) {
		t.Errorf("expected %d migrations, got %d", len(migrations), count)
	}
}

func TestMigrationsHaveUniqueVersions(t *testing.T) {
	seen := make(map[int]struct{}, len(migrations))
	for _, migration := range migrations {
		if _, exists := seen[migration.version]; exists {
			t.Fatalf("duplicate migration version %d", migration.version)
		}
		seen[migration.version] = struct{}{}
	}
}

func TestEventStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	es := NewEventStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	evt := &store.StoredEvent{
		ID:             generateID(),
		ProjectID:      "proj-1",
		EventID:        "evt-abc123",
		GroupID:        "grp-1",
		ReleaseID:      "1.0.0",
		Environment:    "production",
		Platform:       "go",
		Level:          "error",
		EventType:      "log",
		Title:          "NullPointerException: oops",
		Culprit:        "main.go in handleRequest",
		Message:        "oops",
		Tags:           map[string]string{"env": "prod", "server": "web-1"},
		NormalizedJSON: json.RawMessage(`{"event_id":"evt-abc123"}`),
		OccurredAt:     now,
	}

	if err := es.SaveEvent(ctx, evt); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	// Duplicate should be silently ignored (INSERT OR IGNORE).
	if err := es.SaveEvent(ctx, evt); err != nil {
		t.Fatalf("SaveEvent duplicate: %v", err)
	}

	got, err := es.GetEvent(ctx, "proj-1", "evt-abc123")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.Title != evt.Title {
		t.Errorf("Title = %q, want %q", got.Title, evt.Title)
	}
	if got.Tags["server"] != "web-1" {
		t.Errorf("Tags[server] = %q, want %q", got.Tags["server"], "web-1")
	}
	if got.EventType != "log" {
		t.Errorf("EventType = %q, want log", got.EventType)
	}

	// GetEvent not found.
	_, err = es.GetEvent(ctx, "proj-1", "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("GetEvent not found: got %v, want ErrNotFound", err)
	}
}

func TestEventStore_ListEvents(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	es := NewEventStore(db)
	ctx := context.Background()

	// Insert 5 events.
	for i := 0; i < 5; i++ {
		evt := &store.StoredEvent{
			ID:             generateID(),
			ProjectID:      "proj-list",
			EventID:        generateID(),
			Level:          "error",
			OccurredAt:     time.Now().UTC().Add(time.Duration(i) * time.Minute),
			NormalizedJSON: json.RawMessage(`{}`),
		}
		if err := es.SaveEvent(ctx, evt); err != nil {
			t.Fatalf("SaveEvent %d: %v", i, err)
		}
	}

	events, err := es.ListEvents(ctx, "proj-list", store.ListOpts{Limit: 3})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("ListEvents returned %d events, want 3", len(events))
	}

	// Different project returns empty.
	events, err = es.ListEvents(ctx, "proj-other", store.ListOpts{})
	if err != nil {
		t.Fatalf("ListEvents other: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("ListEvents other returned %d events, want 0", len(events))
	}
}

func TestGroupStore_UpsertAndGet(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	g := &issue.Group{
		ProjectID:       "proj-1",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "abc123hash",
		Title:           "TypeError: null is not an object",
		Culprit:         "app.js in onClick",
		Level:           "error",
		FirstSeen:       now,
		LastSeen:        now,
		LastEventID:     "evt-1",
	}

	// First upsert creates the group.
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatalf("UpsertGroup create: %v", err)
	}
	if g.ID == "" {
		t.Fatal("group ID should be set after upsert")
	}
	firstID := g.ID

	// Verify retrieval.
	got, err := gs.GetGroup(ctx, firstID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if got == nil {
		t.Fatal("GetGroup returned nil")
	}
	if got.TimesSeen != 1 {
		t.Errorf("TimesSeen = %d, want 1", got.TimesSeen)
	}
	if got.Title != g.Title {
		t.Errorf("Title = %q, want %q", got.Title, g.Title)
	}

	// Second upsert (same key) should update, not create.
	g2 := &issue.Group{
		ProjectID:       "proj-1",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "abc123hash",
		Title:           "TypeError: null is not an object",
		Culprit:         "app.js in onClick",
		Level:           "error",
		FirstSeen:       now,
		LastSeen:        now.Add(time.Minute),
		LastEventID:     "evt-2",
	}
	if err := gs.UpsertGroup(ctx, g2); err != nil {
		t.Fatalf("UpsertGroup update: %v", err)
	}
	if g2.ID != firstID {
		t.Errorf("ID after update = %q, want %q (should reuse existing)", g2.ID, firstID)
	}

	got2, _ := gs.GetGroup(ctx, firstID)
	if got2.TimesSeen != 2 {
		t.Errorf("TimesSeen after update = %d, want 2", got2.TimesSeen)
	}
	if got2.LastEventID != "evt-2" {
		t.Errorf("LastEventID = %q, want %q", got2.LastEventID, "evt-2")
	}
}

func TestGroupStore_GetGroupByKey(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	g := &issue.Group{
		ProjectID:       "proj-key",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "keytest",
		Title:           "test",
		Level:           "error",
		FirstSeen:       now,
		LastSeen:        now,
		LastEventID:     "evt-1",
	}
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	got, err := gs.GetGroupByKey(ctx, "proj-key", "urgentry-v1", "keytest")
	if err != nil {
		t.Fatalf("GetGroupByKey: %v", err)
	}
	if got == nil {
		t.Fatal("GetGroupByKey returned nil")
	}
	if got.ID != g.ID {
		t.Errorf("ID = %q, want %q", got.ID, g.ID)
	}

	// Not found.
	got, err = gs.GetGroupByKey(ctx, "proj-key", "urgentry-v1", "nope")
	if err != nil {
		t.Fatalf("GetGroupByKey not found: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for not found, got %+v", got)
	}
}

func TestGroupStore_ListGroups(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		g := &issue.Group{
			ProjectID:       "proj-list",
			GroupingVersion: "urgentry-v1",
			GroupingKey:     generateID(),
			Title:           "error",
			Level:           "error",
			FirstSeen:       now.Add(time.Duration(i) * time.Minute),
			LastSeen:        now.Add(time.Duration(i) * time.Minute),
			LastEventID:     generateID(),
		}
		if err := gs.UpsertGroup(ctx, g); err != nil {
			t.Fatalf("UpsertGroup %d: %v", i, err)
		}
	}

	groups, err := gs.ListGroups(ctx, "proj-list", issue.ListOpts{Limit: 3})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 3 {
		t.Errorf("ListGroups returned %d, want 3", len(groups))
	}
}

func TestGroupStore_UpdateStatus(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	g := &issue.Group{
		ProjectID:       "proj-status",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "statustest",
		Title:           "test",
		Level:           "error",
		FirstSeen:       now,
		LastSeen:        now,
		LastEventID:     "evt-1",
	}
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	if err := gs.UpdateStatus(ctx, g.ID, "resolved"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := gs.GetGroup(ctx, g.ID)
	if got.Status != "resolved" {
		t.Errorf("Status = %q, want %q", got.Status, "resolved")
	}
}

func TestGroupStore_PatchIssue(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	g := &issue.Group{
		ProjectID:       "proj-patch",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "patchtest",
		Title:           "test",
		Level:           "error",
		FirstSeen:       now,
		LastSeen:        now,
		LastEventID:     "evt-1",
	}
	if err := gs.UpsertGroup(ctx, g); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	status := "ignored"
	assignee := "dev@example.com"
	priority := 1
	if err := gs.PatchIssue(ctx, g.ID, store.IssuePatch{
		Status:   &status,
		Assignee: &assignee,
		Priority: &priority,
	}); err != nil {
		t.Fatalf("PatchIssue: %v", err)
	}

	var gotStatus, gotAssignee string
	var gotPriority int
	if err := db.QueryRowContext(ctx, `SELECT status, COALESCE(assignee, ''), priority FROM groups WHERE id = ?`, g.ID).Scan(&gotStatus, &gotAssignee, &gotPriority); err != nil {
		t.Fatalf("load group: %v", err)
	}
	if gotStatus != status || gotAssignee != assignee || gotPriority != priority {
		t.Fatalf("patched group = (%q, %q, %d), want (%q, %q, %d)", gotStatus, gotAssignee, gotPriority, status, assignee, priority)
	}
}

func TestGroupStore_ConcurrentUpserts(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	gs := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := &issue.Group{
				ProjectID:       "proj-conc",
				GroupingVersion: "urgentry-v1",
				GroupingKey:     "concurrent-key",
				Title:           "concurrent error",
				Level:           "error",
				FirstSeen:       now,
				LastSeen:        now.Add(time.Duration(i) * time.Second),
				LastEventID:     generateID(),
			}
			errs[i] = gs.UpsertGroup(ctx, g)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	// Verify exactly one group with correct count.
	got, err := gs.GetGroupByKey(ctx, "proj-conc", "urgentry-v1", "concurrent-key")
	if err != nil {
		t.Fatalf("GetGroupByKey: %v", err)
	}
	if got == nil {
		t.Fatal("group not found after concurrent upserts")
	}
	if got.TimesSeen != int64(n) {
		t.Errorf("TimesSeen = %d, want %d", got.TimesSeen, n)
	}
}
