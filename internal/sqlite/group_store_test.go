package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/issue"
)

func TestGroupStoreBatchIssueCommentCounts(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO teams (id, organization_id, slug, name) VALUES ('team-1', 'org-1', 'backend', 'Backend')`); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'checkout', 'Checkout', 'go', 'active')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'dev@example.com', 'Dev User')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	store := NewGroupStore(db)
	source := &issue.Group{
		ID:              "grp-comment-source",
		ProjectID:       "proj-1",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "comment-source",
		Title:           "checkout failure",
		Culprit:         "checkout/service.go",
		Level:           "error",
		Status:          "unresolved",
		FirstSeen:       now,
		LastSeen:        now,
		LastEventID:     "evt-comment-source",
		TimesSeen:       1,
	}
	if err := store.UpsertGroup(ctx, source); err != nil {
		t.Fatalf("UpsertGroup source: %v", err)
	}
	other := &issue.Group{
		ID:              "grp-comment-other",
		ProjectID:       "proj-1",
		GroupingVersion: "urgentry-v1",
		GroupingKey:     "comment-other",
		Title:           "payments failure",
		Culprit:         "payments/service.go",
		Level:           "error",
		Status:          "unresolved",
		FirstSeen:       now.Add(time.Minute),
		LastSeen:        now.Add(time.Minute),
		LastEventID:     "evt-comment-other",
		TimesSeen:       1,
	}
	if err := store.UpsertGroup(ctx, other); err != nil {
		t.Fatalf("UpsertGroup other: %v", err)
	}

	if _, err := store.AddIssueComment(ctx, source.ID, "user-1", "first"); err != nil {
		t.Fatalf("AddIssueComment first: %v", err)
	}
	if _, err := store.AddIssueComment(ctx, source.ID, "user-1", "second"); err != nil {
		t.Fatalf("AddIssueComment second: %v", err)
	}

	counts, err := store.BatchIssueCommentCounts(ctx, []string{source.ID, other.ID, source.ID, "missing"})
	if err != nil {
		t.Fatalf("BatchIssueCommentCounts: %v", err)
	}
	if counts[source.ID] != 2 {
		t.Fatalf("count[%q] = %d, want 2", source.ID, counts[source.ID])
	}
	if counts[other.ID] != 0 {
		t.Fatalf("count[%q] = %d, want 0", other.ID, counts[other.ID])
	}
}
