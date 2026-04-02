package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestAdminStoreListOrgMemberTeams(t *testing.T) {
	db := openStoreTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme'), ('org-2', 'other', 'Other')`); err != nil {
		t.Fatalf("insert orgs: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO teams (id, organization_id, slug, name, created_at) VALUES
			('team-1', 'org-1', 'backend', 'Backend', ?),
			('team-2', 'org-1', 'ops', 'Operations', ?),
			('team-3', 'org-2', 'mobile', 'Mobile', ?)`,
		now, now, now,
	); err != nil {
		t.Fatalf("insert teams: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name) VALUES ('user-1', 'owner@example.com', 'Owner'), ('user-2', 'other@example.com', 'Other')`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO team_members (id, team_id, user_id, role, created_at) VALUES
			('tm-1', 'team-1', 'user-1', 'member', ?),
			('tm-2', 'team-2', 'user-1', 'member', ?),
			('tm-3', 'team-3', 'user-2', 'member', ?)`,
		now, now, now,
	); err != nil {
		t.Fatalf("insert team members: %v", err)
	}

	store := NewAdminStore(db)
	teamsByUser, err := store.ListOrgMemberTeams(context.Background(), "acme")
	if err != nil {
		t.Fatalf("ListOrgMemberTeams: %v", err)
	}
	if len(teamsByUser["user-1"]) != 2 || teamsByUser["user-1"][0] != "backend" || teamsByUser["user-1"][1] != "ops" {
		t.Fatalf("unexpected org teams: %+v", teamsByUser)
	}
	if _, ok := teamsByUser["user-2"]; ok {
		t.Fatalf("unexpected foreign-org teams: %+v", teamsByUser)
	}
}
