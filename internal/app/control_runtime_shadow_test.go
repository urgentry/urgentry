package app

import (
	"context"
	"testing"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
)

type fakeAdminStore struct {
	listOrgMembers []*controlplane.OrgMemberRecord
	addOrgMember   *controlplane.OrgMemberRecord
	acceptInvite   *controlplane.InviteAcceptanceResult
	removeCalled   bool
}

func (f *fakeAdminStore) ListOrgMembers(context.Context, string) ([]*controlplane.OrgMemberRecord, error) {
	return f.listOrgMembers, nil
}

func (f *fakeAdminStore) AddOrgMember(context.Context, string, string, string) (*controlplane.OrgMemberRecord, error) {
	return f.addOrgMember, nil
}

func (f *fakeAdminStore) RemoveOrgMember(context.Context, string, string) (bool, error) {
	f.removeCalled = true
	return true, nil
}

func (f *fakeAdminStore) ListTeams(context.Context, string) ([]*controlplane.TeamRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) CreateTeam(context.Context, string, string, string) (*controlplane.TeamRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) ListTeamMembers(context.Context, string, string) ([]*controlplane.TeamMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) AddTeamMember(context.Context, string, string, string, string) (*controlplane.TeamMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) RemoveTeamMember(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (f *fakeAdminStore) ListInvites(context.Context, string) ([]*controlplane.InviteRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) CreateInvite(context.Context, string, string, string, string, string) (*controlplane.InviteRecord, string, error) {
	return nil, "", nil
}

func (f *fakeAdminStore) RevokeInvite(context.Context, string, string) (bool, error) {
	return false, nil
}

func (f *fakeAdminStore) AcceptInvite(context.Context, string, string, string) (*controlplane.InviteAcceptanceResult, error) {
	return f.acceptInvite, nil
}

func (f *fakeAdminStore) ListProjectMembers(context.Context, string, string) ([]*controlplane.ProjectMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) UpdateProjectMemberRole(context.Context, string, string, string, string) (*controlplane.ProjectMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) AddProjectMember(context.Context, string, string, string, string) (*controlplane.ProjectMemberRecord, error) {
	return nil, nil
}

func TestShadowingAdminStoreSyncsMembershipLifecycle(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	added := &controlplane.OrgMemberRecord{
		ID:               "mem-1",
		OrganizationID:   "org-1",
		OrganizationSlug: "acme",
		UserID:           "user-1",
		Email:            "owner@example.com",
		Name:             "Owner",
		Role:             "owner",
	}
	base := &fakeAdminStore{
		listOrgMembers: []*controlplane.OrgMemberRecord{added},
		addOrgMember:   added,
		acceptInvite: &controlplane.InviteAcceptanceResult{
			OrganizationID:   "org-1",
			OrganizationSlug: "acme",
			Role:             "member",
			User: auth.User{
				ID:          "user-2",
				Email:       "member@example.com",
				DisplayName: "Member",
			},
		},
	}
	store := newShadowingAdminStore(base, sqlite.NewPrincipalShadowStore(db))

	rec, err := store.AddOrgMember(context.Background(), "acme", "user-1", "owner")
	if err != nil {
		t.Fatalf("AddOrgMember() error = %v", err)
	}
	if rec == nil || rec.UserID != "user-1" {
		t.Fatalf("AddOrgMember() = %+v, want user-1", rec)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-1'`).Scan(&count); err != nil {
		t.Fatalf("count added membership: %v", err)
	}
	if count != 1 {
		t.Fatalf("added membership count = %d, want 1", count)
	}

	accepted, err := store.AcceptInvite(context.Background(), "invite-token", "Member", "secret")
	if err != nil {
		t.Fatalf("AcceptInvite() error = %v", err)
	}
	if accepted == nil || accepted.User.ID != "user-2" {
		t.Fatalf("AcceptInvite() = %+v, want user-2", accepted)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-2'`).Scan(&count); err != nil {
		t.Fatalf("count accepted membership: %v", err)
	}
	if count != 1 {
		t.Fatalf("accepted membership count = %d, want 1", count)
	}

	removed, err := store.RemoveOrgMember(context.Background(), "acme", "user-1")
	if err != nil {
		t.Fatalf("RemoveOrgMember() error = %v", err)
	}
	if !removed || !base.removeCalled {
		t.Fatalf("RemoveOrgMember() = %v, removeCalled = %v, want true/true", removed, base.removeCalled)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-1'`).Scan(&count); err != nil {
		t.Fatalf("count removed membership: %v", err)
	}
	if count != 0 {
		t.Fatalf("removed membership count = %d, want 0", count)
	}
}
