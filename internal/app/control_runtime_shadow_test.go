package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

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

func (f *fakeAdminStore) GetOrgMember(context.Context, string, string) (*controlplane.OrgMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) UpdateOrgMemberRole(context.Context, string, string, string) (*controlplane.OrgMemberRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) ListTeams(context.Context, string) ([]*controlplane.TeamRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) GetTeam(context.Context, string, string) (*controlplane.TeamRecord, int, int, error) {
	return nil, 0, 0, nil
}

func (f *fakeAdminStore) CreateTeam(context.Context, string, string, string) (*controlplane.TeamRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) UpdateTeam(_ context.Context, _, _ string, _, _ *string) (*controlplane.TeamRecord, error) {
	return nil, nil
}

func (f *fakeAdminStore) DeleteTeam(context.Context, string, string) (bool, error) {
	return false, nil
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
func (f *fakeAdminStore) ListTeamProjects(context.Context, string, string) ([]controlplane.TeamProjectRecord, error) {
	return nil, nil
}
func (f *fakeAdminStore) ListUserTeams(context.Context, string, string) ([]*controlplane.TeamRecord, error) {
	return nil, nil
}
func (f *fakeAdminStore) ListOrgMemberTeams(context.Context, string) (map[string][]string, error) {
	return nil, nil
}
func (f *fakeAdminStore) AddMemberToTeamByMemberID(context.Context, string, string, string) (*controlplane.TeamMemberRecord, error) {
	return nil, nil
}
func (f *fakeAdminStore) RemoveMemberFromTeamByMemberID(context.Context, string, string, string) (bool, error) {
	return false, nil
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

func TestPrincipalShadowProjectorStatusTransitions(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	projector := &PrincipalShadowProjector{
		controlDB: &sql.DB{},
		shadows:   sqlite.NewPrincipalShadowStore(nil),
		now: func() time.Time {
			return now
		},
	}

	status := projector.Status(10 * time.Minute)
	if status.Status != "warn" || status.Detail != "not synced" {
		t.Fatalf("initial status = %+v, want warn/not synced", status)
	}

	projector.record(errors.New("database is locked: dsn=postgres://secret@example"))
	status = projector.Status(10 * time.Minute)
	if status.Status != "error" || status.Detail != "last sync failed" {
		t.Fatalf("error status = %+v, want generic failure detail", status)
	}
	if strings.Contains(status.Detail, "postgres://") {
		t.Fatalf("error status detail leaked backend detail: %q", status.Detail)
	}

	projector.record(nil)
	status = projector.Status(10 * time.Minute)
	if status.Status != "ok" || !strings.Contains(status.Detail, "2026-05-17T12:00:00Z") {
		t.Fatalf("success status = %+v, want last success timestamp", status)
	}

	now = now.Add(11 * time.Minute)
	status = projector.Status(10 * time.Minute)
	if status.Status != "warn" || status.Detail != "last sync stale" {
		t.Fatalf("stale status = %+v, want warn/stale", status)
	}
}

func TestPrincipalShadowProjectorRetriesSQLiteBusy(t *testing.T) {
	attempts := 0
	projector := &PrincipalShadowProjector{
		controlDB: &sql.DB{},
		shadows:   sqlite.NewPrincipalShadowStore(nil),
		now:       time.Now,
		syncFunc: func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("database is locked")
			}
			return nil
		},
	}

	if err := projector.SyncWithRetry(context.Background(), time.Second); err != nil {
		t.Fatalf("SyncWithRetry() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	status := projector.Status(time.Minute)
	if status.Status != "ok" {
		t.Fatalf("status after retry = %+v, want ok", status)
	}
}
