package postgrescontrol

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAdminStoreMembershipLifecycle(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedTeam(t, db, "team-1", "org-1", "backend", "Backend")
	seedUser(t, db, "user-1", "owner@example.com", "Owner")
	seedUser(t, db, "user-2", "dev@example.com", "Dev")

	store := NewAdminStore(db)

	member, err := store.AddOrgMember(ctx, "acme", "user-1", "owner")
	if err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}
	if member == nil || member.Role != "owner" || member.Email != "owner@example.com" {
		t.Fatalf("unexpected org member: %+v", member)
	}

	team, err := store.CreateTeam(ctx, "acme", "ops", "Operations")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team == nil || team.Slug != "ops" {
		t.Fatalf("unexpected created team: %+v", team)
	}

	teams, err := store.ListTeams(ctx, "acme")
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("len(teams) = %d, want 2", len(teams))
	}

	teamMember, err := store.AddTeamMember(ctx, "acme", "backend", "user-2", "maintainer")
	if err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	if teamMember == nil || teamMember.Role != "maintainer" || teamMember.Email != "dev@example.com" {
		t.Fatalf("unexpected team member: %+v", teamMember)
	}

	orgMembers, err := store.ListOrgMembers(ctx, "acme")
	if err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}
	if len(orgMembers) != 2 {
		t.Fatalf("len(orgMembers) = %d, want 2", len(orgMembers))
	}

	teamMembers, err := store.ListTeamMembers(ctx, "acme", "backend")
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if len(teamMembers) != 1 || teamMembers[0].UserID != "user-2" {
		t.Fatalf("unexpected team members: %+v", teamMembers)
	}

	removedTeam, err := store.RemoveTeamMember(ctx, "acme", "backend", "user-2")
	if err != nil {
		t.Fatalf("RemoveTeamMember: %v", err)
	}
	if !removedTeam {
		t.Fatal("expected RemoveTeamMember to remove a row")
	}

	removedOrg, err := store.RemoveOrgMember(ctx, "acme", "user-2")
	if err != nil {
		t.Fatalf("RemoveOrgMember: %v", err)
	}
	if !removedOrg {
		t.Fatal("expected RemoveOrgMember to remove a row")
	}
}

func TestAdminStoreInviteLifecycle(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedTeam(t, db, "team-1", "org-1", "backend", "Backend")
	seedUser(t, db, "user-1", "owner@example.com", "Owner")

	store := NewAdminStore(db)

	invite, rawToken, err := store.CreateInvite(ctx, "acme", "new@example.com", "member", "backend", "user-1")
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if invite == nil || rawToken == "" || invite.Status != "pending" {
		t.Fatalf("unexpected invite: %+v raw=%q", invite, rawToken)
	}

	invites, err := store.ListInvites(ctx, "acme")
	if err != nil {
		t.Fatalf("ListInvites pending: %v", err)
	}
	if len(invites) != 1 || invites[0].Status != "pending" {
		t.Fatalf("unexpected pending invites: %+v", invites)
	}

	result, err := store.AcceptInvite(ctx, rawToken, "New Hire", "")
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if result == nil || result.User.Email != "new@example.com" || result.TeamSlug != "backend" {
		t.Fatalf("unexpected acceptance result: %+v", result)
	}
	if result.TemporaryPassword == "" {
		t.Fatal("expected generated temporary password for new user")
	}

	invites, err = store.ListInvites(ctx, "acme")
	if err != nil {
		t.Fatalf("ListInvites accepted: %v", err)
	}
	if len(invites) != 1 || invites[0].Status != "accepted" || invites[0].AcceptedAt == nil {
		t.Fatalf("unexpected accepted invite state: %+v", invites)
	}
	if invites[0].AcceptedByUserID != "" {
		t.Fatalf("AcceptedByUserID = %q, want empty because schema does not persist it", invites[0].AcceptedByUserID)
	}

	members, err := store.ListOrgMembers(ctx, "acme")
	if err != nil {
		t.Fatalf("ListOrgMembers after accept: %v", err)
	}
	if len(members) != 1 || members[0].Email != "new@example.com" {
		t.Fatalf("unexpected org members after accept: %+v", members)
	}

	teamMembers, err := store.ListTeamMembers(ctx, "acme", "backend")
	if err != nil {
		t.Fatalf("ListTeamMembers after accept: %v", err)
	}
	if len(teamMembers) != 1 || teamMembers[0].Email != "new@example.com" {
		t.Fatalf("unexpected team members after accept: %+v", teamMembers)
	}

	if _, err := store.AcceptInvite(ctx, rawToken, "", ""); !errors.Is(err, ErrInviteConsumed) {
		t.Fatalf("second AcceptInvite error = %v, want ErrInviteConsumed", err)
	}

	revoked, secondToken, err := store.CreateInvite(ctx, "acme", "other@example.com", "member", "", "user-1")
	if err != nil {
		t.Fatalf("CreateInvite second: %v", err)
	}
	if revoked == nil || secondToken == "" {
		t.Fatalf("unexpected second invite: %+v %q", revoked, secondToken)
	}
	ok, err := store.RevokeInvite(ctx, "acme", revoked.ID)
	if err != nil {
		t.Fatalf("RevokeInvite: %v", err)
	}
	if !ok {
		t.Fatal("expected RevokeInvite to update a row")
	}

	invites, err = store.ListInvites(ctx, "acme")
	if err != nil {
		t.Fatalf("ListInvites revoked: %v", err)
	}
	if len(invites) != 2 || invites[0].Status != "revoked" {
		t.Fatalf("unexpected revoked invite list: %+v", invites)
	}
}

func TestAdminStoreListOrgMemberTeams(t *testing.T) {
	t.Parallel()

	db := openMigratedControlDB(t)
	ctx := context.Background()

	seedOrganization(t, db, "org-1", "acme", "Acme")
	seedOrganization(t, db, "org-2", "other", "Other")
	seedTeam(t, db, "team-1", "org-1", "backend", "Backend")
	seedTeam(t, db, "team-2", "org-1", "ops", "Operations")
	seedTeam(t, db, "team-3", "org-2", "mobile", "Mobile")
	seedUser(t, db, "user-1", "owner@example.com", "Owner")
	seedUser(t, db, "user-2", "other@example.com", "Other")
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO team_members (id, team_id, user_id, role, created_at) VALUES
			('tm-1', 'team-1', 'user-1', 'member', $1),
			('tm-2', 'team-2', 'user-1', 'member', $1),
			('tm-3', 'team-3', 'user-2', 'member', $1)`,
		now,
	); err != nil {
		t.Fatalf("insert team members: %v", err)
	}

	store := NewAdminStore(db)
	teamsByUser, err := store.ListOrgMemberTeams(ctx, "acme")
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

func openMigratedControlDB(t *testing.T) *sql.DB {
	t.Helper()
	return openMigratedTestDatabase(t)
}

func seedOrganization(t *testing.T, db *sql.DB, id, slug, name string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)`, id, slug, name, time.Now().UTC()); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
}

func seedTeam(t *testing.T, db *sql.DB, id, orgID, slug, name string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO teams (id, organization_id, slug, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5)`, id, orgID, slug, name, time.Now().UTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
}

func seedUser(t *testing.T, db *sql.DB, id, email, displayName string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES ($1, $2, $3, TRUE, $4, $4)`, id, email, displayName, time.Now().UTC()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}
