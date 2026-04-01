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
