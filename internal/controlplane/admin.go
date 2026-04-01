package controlplane

import "context"

type AdminStore interface {
	ListOrgMembers(ctx context.Context, orgSlug string) ([]*OrgMemberRecord, error)
	AddOrgMember(ctx context.Context, orgSlug, userID, role string) (*OrgMemberRecord, error)
	RemoveOrgMember(ctx context.Context, orgSlug, userID string) (bool, error)
	ListTeams(ctx context.Context, orgSlug string) ([]*TeamRecord, error)
	CreateTeam(ctx context.Context, orgSlug, slug, name string) (*TeamRecord, error)
	ListTeamMembers(ctx context.Context, orgSlug, teamSlug string) ([]*TeamMemberRecord, error)
	AddTeamMember(ctx context.Context, orgSlug, teamSlug, userID, role string) (*TeamMemberRecord, error)
	RemoveTeamMember(ctx context.Context, orgSlug, teamSlug, userID string) (bool, error)
	ListInvites(ctx context.Context, orgSlug string) ([]*InviteRecord, error)
	CreateInvite(ctx context.Context, orgSlug, email, role, teamSlug, createdByUserID string) (*InviteRecord, string, error)
	RevokeInvite(ctx context.Context, orgSlug, inviteID string) (bool, error)
	AcceptInvite(ctx context.Context, inviteToken, displayName, password string) (*InviteAcceptanceResult, error)
	ListProjectMembers(ctx context.Context, orgSlug, projectSlug string) ([]*ProjectMemberRecord, error)
	UpdateProjectMemberRole(ctx context.Context, orgSlug, projectSlug, memberID, role string) (*ProjectMemberRecord, error)
	AddProjectMember(ctx context.Context, orgSlug, projectSlug, userID, role string) (*ProjectMemberRecord, error)
}
