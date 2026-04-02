package postgrescontrol

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/issue"
	sharedstore "urgentry/internal/store"
)

type controlFixture struct {
	OrgID       string
	OrgSlug     string
	TeamID      string
	TeamSlug    string
	UserID      string
	UserEmail   string
	ProjectID   string
	ProjectSlug string
}

func seedControlFixture(t *testing.T) (*sql.DB, controlFixture) {
	t.Helper()

	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	fx := controlFixture{
		OrgID:       "org-1",
		OrgSlug:     "acme",
		TeamID:      "team-1",
		TeamSlug:    "backend",
		UserID:      "user-1",
		UserEmail:   "dev@example.com",
		ProjectID:   "proj-1",
		ProjectSlug: "checkout",
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES ($1, $2, 'Acme', $3, $3)`,
		fx.OrgID, fx.OrgSlug, now,
	); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO teams (id, organization_id, slug, name, created_at, updated_at) VALUES ($1, $2, $3, 'Backend', $4, $4)`,
		fx.TeamID, fx.OrgID, fx.TeamSlug, now,
	); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, created_at, updated_at) VALUES ($1, $2, 'Dev User', $3, $3)`,
		fx.UserID, fx.UserEmail, now,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, created_at, updated_at) VALUES ($1, $2, $3, $4, 'Checkout', 'go', $5, $5)`,
		fx.ProjectID, fx.OrgID, fx.TeamID, fx.ProjectSlug, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db, fx
}

func createTestGroup(t *testing.T, store *GroupStore, projectID, key, title, culprit string, seen time.Time) *issue.Group {
	t.Helper()

	group := &issue.Group{
		ProjectID:       projectID,
		GroupingVersion: "urgentry-v1",
		GroupingKey:     key,
		Title:           title,
		Culprit:         culprit,
		Level:           "error",
		FirstSeen:       seen,
		LastSeen:        seen,
		LastEventID:     key + "-event",
	}
	if err := store.UpsertGroup(context.Background(), group); err != nil {
		t.Fatalf("UpsertGroup(%s): %v", key, err)
	}
	return group
}

func TestGroupStore_WorkflowLifecycle(t *testing.T) {
	db, fx := seedControlFixture(t)
	store := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	source := createTestGroup(t, store, fx.ProjectID, "grp-source", "checkout failure", "checkout/service.go", now)
	target := createTestGroup(t, store, fx.ProjectID, "grp-target", "payments failure", "payments/service.go", now.Add(time.Minute))

	status := "resolved"
	assignee := "dev@example.com"
	priority := 1
	substatus := "next_release"
	if err := store.PatchIssue(ctx, source.ID, sharedstore.IssuePatch{
		Status:              &status,
		Assignee:            &assignee,
		Priority:            &priority,
		ResolutionSubstatus: &substatus,
	}); err != nil {
		t.Fatalf("PatchIssue: %v", err)
	}

	got, err := store.GetGroup(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if got.Status != "resolved" || got.Assignee != assignee || got.ResolutionSubstatus != "next_release" {
		t.Fatalf("unexpected patched group: %+v", got)
	}

	var isResolved bool
	if err := db.QueryRowContext(ctx, `SELECT is_resolved FROM group_states WHERE group_id = $1`, source.ID).Scan(&isResolved); err != nil {
		t.Fatalf("load group state: %v", err)
	}
	if !isResolved {
		t.Fatal("group state was not marked resolved")
	}

	comment, err := store.AddIssueComment(ctx, source.ID, fx.UserID, "Investigating")
	if err != nil {
		t.Fatalf("AddIssueComment: %v", err)
	}
	if comment.ProjectID != fx.ProjectID || comment.UserID != fx.UserID {
		t.Fatalf("unexpected comment: %+v", comment)
	}
	if err := store.RecordIssueActivity(ctx, source.ID, fx.UserID, "assign", "Assigned issue", assignee); err != nil {
		t.Fatalf("RecordIssueActivity: %v", err)
	}

	comments, err := store.ListIssueComments(ctx, source.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || comments[0].UserEmail != fx.UserEmail {
		t.Fatalf("unexpected comments: %+v", comments)
	}
	activity, err := store.ListIssueActivity(ctx, source.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueActivity: %v", err)
	}
	if len(activity) < 2 {
		t.Fatalf("expected at least 2 activity entries, got %d", len(activity))
	}

	if err := store.ToggleIssueBookmark(ctx, source.ID, fx.UserID, true); err != nil {
		t.Fatalf("ToggleIssueBookmark: %v", err)
	}
	if err := store.ToggleIssueSubscription(ctx, source.ID, fx.UserID, true); err != nil {
		t.Fatalf("ToggleIssueSubscription: %v", err)
	}
	state, err := store.GetIssueWorkflowState(ctx, source.ID, fx.UserID)
	if err != nil {
		t.Fatalf("GetIssueWorkflowState: %v", err)
	}
	if !state.Bookmarked || !state.Subscribed || state.ResolutionSubstatus != "next_release" {
		t.Fatalf("unexpected workflow state: %+v", state)
	}

	if err := store.MergeIssue(ctx, source.ID, target.ID, fx.UserID); err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	merged, err := store.GetGroup(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGroup after merge: %v", err)
	}
	if merged.Status != "ignored" || merged.MergedIntoGroupID != target.ID || merged.ResolutionSubstatus != "merged" {
		t.Fatalf("unexpected merged group: %+v", merged)
	}

	if err := store.UnmergeIssue(ctx, source.ID, fx.UserID); err != nil {
		t.Fatalf("UnmergeIssue: %v", err)
	}
	unmerged, err := store.GetGroup(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetGroup after unmerge: %v", err)
	}
	if unmerged.Status != "unresolved" || unmerged.MergedIntoGroupID != "" || unmerged.ResolutionSubstatus != "" {
		t.Fatalf("unexpected unmerged group: %+v", unmerged)
	}
}

func TestGroupStoreBatchIssueCommentCounts(t *testing.T) {
	db, fx := seedControlFixture(t)
	store := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	source := createTestGroup(t, store, fx.ProjectID, "grp-comment-source", "checkout failure", "checkout/service.go", now)
	other := createTestGroup(t, store, fx.ProjectID, "grp-comment-other", "payments failure", "payments/service.go", now.Add(time.Minute))

	if _, err := store.AddIssueComment(ctx, source.ID, fx.UserID, "first"); err != nil {
		t.Fatalf("AddIssueComment first: %v", err)
	}
	if _, err := store.AddIssueComment(ctx, source.ID, fx.UserID, "second"); err != nil {
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
