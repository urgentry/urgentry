package sqlite

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"
)

func seedReleaseHealthProject(t *testing.T, db *sql.DB, orgID, orgSlug, projectID, projectSlug string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES (?, ?, 'Acme')`, orgID, orgSlug); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES (?, ?, ?, 'Mobile', 'cocoa', 'active')`, projectID, orgID, projectSlug); err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func TestReleaseHealthStoreSaveSessionAndList(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	ctx := context.Background()
	store := NewReleaseHealthStore(db)

	if err := store.SaveSession(ctx, &ReleaseSession{
		ProjectID:   "proj-1",
		Release:     "ios@1.2.3",
		DistinctID:  "user-1",
		Status:      "ok",
		StartedAt:   time.Now().UTC(),
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession ok: %v", err)
	}
	if err := store.SaveSession(ctx, &ReleaseSession{
		ProjectID:   "proj-1",
		Release:     "ios@1.2.3",
		Status:      "crashed",
		Quantity:    2,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession crashed: %v", err)
	}

	summary, err := store.GetReleaseHealth(ctx, "proj-1", "ios@1.2.3")
	if err != nil {
		t.Fatalf("GetReleaseHealth: %v", err)
	}
	if summary.SessionCount != 3 {
		t.Fatalf("SessionCount = %d, want 3", summary.SessionCount)
	}
	if summary.CrashedSessions != 2 {
		t.Fatalf("CrashedSessions = %d, want 2", summary.CrashedSessions)
	}
	if summary.AffectedUsers != 1 {
		t.Fatalf("AffectedUsers = %d, want 1", summary.AffectedUsers)
	}
	if math.Abs(summary.CrashFreeRate-33.3333333333) > 0.01 {
		t.Fatalf("CrashFreeRate = %.2f, want about 33.33", summary.CrashFreeRate)
	}

	var releaseCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM releases WHERE organization_id = 'org-1' AND version = 'ios@1.2.3'`).Scan(&releaseCount); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releaseCount != 1 {
		t.Fatalf("release count = %d, want 1", releaseCount)
	}
}

func TestReleaseHealthStoreListOrganizationReleaseHealthIncludesEmptyReleases(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "mobile")

	if _, err := db.Exec(`INSERT INTO releases (id, organization_id, version, created_at) VALUES ('rel-empty', 'org-1', 'ios@2.0.0', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert empty release: %v", err)
	}

	store := NewReleaseHealthStore(db)
	summaries, err := store.ListOrganizationReleaseHealth(context.Background(), "acme", 10)
	if err != nil {
		t.Fatalf("ListOrganizationReleaseHealth: %v", err)
	}

	for _, summary := range summaries {
		if summary.ReleaseVersion != "ios@2.0.0" {
			continue
		}
		if summary.SessionCount != 0 {
			t.Fatalf("SessionCount = %d, want 0", summary.SessionCount)
		}
		if summary.CrashFreeRate != 100 {
			t.Fatalf("CrashFreeRate = %.1f, want 100", summary.CrashFreeRate)
		}
		return
	}

	t.Fatal("expected to find empty release summary")
}
