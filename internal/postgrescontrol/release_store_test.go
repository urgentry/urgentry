package postgrescontrol

import (
	"context"
	"testing"
	"time"

	sharedstore "urgentry/internal/store"
)

func TestReleaseStore_CreateReleaseWorkflow(t *testing.T) {
	db, fx := seedControlFixture(t)
	store := NewReleaseStore(db)
	groups := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	group := createTestGroup(t, groups, fx.ProjectID, "grp-release", "checkout panic", "checkout/service.go", now)
	status := "resolved"
	substatus := "next_release"
	if err := groups.PatchIssue(ctx, group.ID, sharedstore.IssuePatch{
		Status:              &status,
		ResolutionSubstatus: &substatus,
	}); err != nil {
		t.Fatalf("PatchIssue: %v", err)
	}

	release, err := store.CreateRelease(ctx, fx.OrgSlug, "checkout@2.0.0")
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if release == nil || release.Version != "checkout@2.0.0" {
		t.Fatalf("unexpected release: %+v", release)
	}
	bySlug, err := store.GetReleaseBySlug(ctx, fx.OrgSlug, "checkout@2.0.0")
	if err != nil {
		t.Fatalf("GetReleaseBySlug: %v", err)
	}
	if bySlug == nil || bySlug.ID != release.ID {
		t.Fatalf("unexpected release by slug: %+v", bySlug)
	}

	ref := "refs/tags/checkout@2.0.0"
	url := "https://deploy.example.com/releases/checkout@2.0.0"
	dateReleased := now.Add(2 * time.Minute).UTC().Truncate(time.Microsecond)
	updatedRelease, err := store.UpdateRelease(ctx, fx.OrgSlug, "checkout@2.0.0", &ref, &url, &dateReleased)
	if err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	if updatedRelease == nil || updatedRelease.Ref != ref || updatedRelease.URL != url || !updatedRelease.DateReleased.Equal(dateReleased) {
		t.Fatalf("unexpected updated release: %+v", updatedRelease)
	}

	var resolvedInRelease string
	if err := db.QueryRowContext(ctx, `SELECT resolved_in_release FROM groups WHERE id = $1`, group.ID).Scan(&resolvedInRelease); err != nil {
		t.Fatalf("load group: %v", err)
	}
	if resolvedInRelease != "checkout@2.0.0" {
		t.Fatalf("resolved_in_release = %q, want checkout@2.0.0", resolvedInRelease)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO release_projects (id, release_id, project_id, new_groups) VALUES ('rp-1', $1, $2, 3)`,
		release.ID, fx.ProjectID,
	); err != nil {
		t.Fatalf("seed release_projects: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO release_sessions
			(id, project_id, release, environment, session_id, status, timestamp, user_identifier)
		 VALUES
			('sess-1', $1, 'checkout@2.0.0', 'production', 's1', 'ok', $2, 'user-a'),
			('sess-2', $1, 'checkout@2.0.0', 'production', 's2', 'crashed', $2, 'user-b')`,
		fx.ProjectID, now,
	); err != nil {
		t.Fatalf("seed release sessions: %v", err)
	}

	if _, err := store.AddDeploy(ctx, fx.OrgSlug, "checkout@2.0.0", sharedstore.ReleaseDeploy{
		Environment: "production",
		Name:        "deploy-42",
		URL:         "https://deploy.example.com/42",
	}); err != nil {
		t.Fatalf("AddDeploy: %v", err)
	}
	if _, err := store.AddCommit(ctx, fx.OrgSlug, "checkout@2.0.0", sharedstore.ReleaseCommit{
		CommitSHA:   "abc123",
		Repository:  "git@example.com/acme/checkout",
		AuthorName:  "Dev User",
		Message:     "Fix checkout",
		Files:       []string{"checkout/service.go"},
		DateCreated: now,
	}); err != nil {
		t.Fatalf("AddCommit: %v", err)
	}

	releases, err := store.ListReleases(ctx, fx.OrgID, 10)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(releases) != 1 || releases[0].EventCount != 3 || releases[0].SessionCount != 2 {
		t.Fatalf("unexpected releases: %+v", releases)
	}
	if releases[0].AffectedUsers != 2 || releases[0].CrashFreeRate >= 100 {
		t.Fatalf("unexpected release metrics: %+v", releases[0])
	}

	deploys, err := store.ListDeploys(ctx, fx.OrgSlug, "checkout@2.0.0", 10)
	if err != nil {
		t.Fatalf("ListDeploys: %v", err)
	}
	if len(deploys) != 1 || deploys[0].Environment != "production" {
		t.Fatalf("unexpected deploys: %+v", deploys)
	}
	commits, err := store.ListCommits(ctx, fx.OrgSlug, "checkout@2.0.0", 10)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 1 || commits[0].CommitSHA != "abc123" {
		t.Fatalf("unexpected commits: %+v", commits)
	}

	hasRelease, err := store.ProjectHasRelease(ctx, fx.ProjectID, "checkout@2.0.0")
	if err != nil {
		t.Fatalf("ProjectHasRelease matching project: %v", err)
	}
	if !hasRelease {
		t.Fatal("expected release association for matching project")
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, created_at, updated_at)
		 VALUES ('proj-2', $1, $2, 'mobile', 'Mobile', 'swift', $3, $3)`,
		fx.OrgID, fx.TeamID, now,
	); err != nil {
		t.Fatalf("seed second project: %v", err)
	}

	hasRelease, err = store.ProjectHasRelease(ctx, "proj-2", "checkout@2.0.0")
	if err != nil {
		t.Fatalf("ProjectHasRelease foreign project: %v", err)
	}
	if hasRelease {
		t.Fatal("expected no release association for foreign project")
	}

	if err := store.DeleteRelease(ctx, fx.OrgSlug, "checkout@2.0.0"); err != nil {
		t.Fatalf("DeleteRelease: %v", err)
	}
	deleted, err := store.GetReleaseBySlug(ctx, fx.OrgSlug, "checkout@2.0.0")
	if err != nil {
		t.Fatalf("GetReleaseBySlug after delete: %v", err)
	}
	if deleted != nil {
		t.Fatalf("expected deleted release lookup to return nil, got %+v", deleted)
	}
}

func TestReleaseStore_ListSuspects(t *testing.T) {
	db, fx := seedControlFixture(t)
	releases := NewReleaseStore(db)
	groups := NewGroupStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	group := createTestGroup(t, groups, fx.ProjectID, "grp-suspect", "checkout service panic", "checkout/service.go", now)
	release, err := releases.CreateRelease(ctx, fx.OrgSlug, "checkout@3.0.0")
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO release_projects (id, release_id, project_id, new_groups) VALUES ('rp-2', $1, $2, 1)`,
		release.ID, fx.ProjectID,
	); err != nil {
		t.Fatalf("seed release_projects: %v", err)
	}
	if err := groups.PatchIssue(ctx, group.ID, sharedstore.IssuePatch{ResolvedInRelease: stringPtr("checkout@3.0.0")}); err != nil {
		t.Fatalf("PatchIssue resolved_in_release: %v", err)
	}
	if _, err := releases.AddCommit(ctx, fx.OrgSlug, "checkout@3.0.0", sharedstore.ReleaseCommit{
		CommitSHA:   "def456",
		Repository:  "git@example.com/acme/checkout",
		AuthorName:  "Dev User",
		Message:     "Refactor checkout",
		Files:       []string{"checkout/service.go"},
		DateCreated: now,
	}); err != nil {
		t.Fatalf("AddCommit: %v", err)
	}

	suspects, err := releases.ListSuspects(ctx, fx.OrgSlug, "checkout@3.0.0", 10)
	if err != nil {
		t.Fatalf("ListSuspects: %v", err)
	}
	if len(suspects) == 0 || suspects[0].GroupID != group.ID || suspects[0].MatchedFile != "checkout/service.go" {
		t.Fatalf("unexpected suspects: %+v", suspects)
	}
}

func stringPtr(value string) *string {
	return &value
}
