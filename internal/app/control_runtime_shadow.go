package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/postgrescontrol"
	scimcore "urgentry/internal/scim"
	"urgentry/internal/sqlite"
)

type shadowingAuthStore struct {
	base    *postgrescontrol.AuthStore
	shadows *sqlite.PrincipalShadowStore
}

func newShadowingAuthStore(base *postgrescontrol.AuthStore, shadows *sqlite.PrincipalShadowStore) *shadowingAuthStore {
	return &shadowingAuthStore{base: base, shadows: shadows}
}

func (s *shadowingAuthStore) AuthenticateUserPassword(ctx context.Context, email, password string) (*auth.User, error) {
	return s.base.AuthenticateUserPassword(ctx, email, password)
}

func (s *shadowingAuthStore) CreateSession(ctx context.Context, userID, userAgent, ipAddress string, ttl time.Duration) (string, *auth.Principal, error) {
	raw, principal, err := s.base.CreateSession(ctx, userID, userAgent, ipAddress, ttl)
	if err != nil {
		return "", nil, err
	}
	if err := s.shadows.UpsertUser(ctx, principal.User); err != nil {
		return "", nil, err
	}
	return raw, principal, nil
}

func (s *shadowingAuthStore) AuthenticateSession(ctx context.Context, rawToken string) (*auth.Principal, error) {
	return s.base.AuthenticateSession(ctx, rawToken)
}

func (s *shadowingAuthStore) RevokeSession(ctx context.Context, sessionID string) error {
	return s.base.RevokeSession(ctx, sessionID)
}

func (s *shadowingAuthStore) AuthenticatePAT(ctx context.Context, rawToken string) (*auth.Principal, error) {
	return s.base.AuthenticatePAT(ctx, rawToken)
}

func (s *shadowingAuthStore) AuthenticateAutomationToken(ctx context.Context, rawToken string) (*auth.Principal, error) {
	return s.base.AuthenticateAutomationToken(ctx, rawToken)
}

func (s *shadowingAuthStore) ResolveOrganizationBySlug(ctx context.Context, slug string) (*auth.Organization, error) {
	return s.base.ResolveOrganizationBySlug(ctx, slug)
}

func (s *shadowingAuthStore) ResolveProjectByID(ctx context.Context, projectID string) (*auth.Project, error) {
	return s.base.ResolveProjectByID(ctx, projectID)
}

func (s *shadowingAuthStore) ResolveProjectBySlug(ctx context.Context, orgSlug, projectSlug string) (*auth.Project, error) {
	return s.base.ResolveProjectBySlug(ctx, orgSlug, projectSlug)
}

func (s *shadowingAuthStore) ResolveIssueProject(ctx context.Context, issueID string) (*auth.Project, error) {
	return s.base.ResolveIssueProject(ctx, issueID)
}

func (s *shadowingAuthStore) ResolveEventProject(ctx context.Context, eventID string) (*auth.Project, error) {
	return s.base.ResolveEventProject(ctx, eventID)
}

func (s *shadowingAuthStore) LookupUserOrgRole(ctx context.Context, userID, organizationID string) (string, error) {
	return s.base.LookupUserOrgRole(ctx, userID, organizationID)
}

func (s *shadowingAuthStore) ListUserOrgRoles(ctx context.Context, userID string) (map[string]string, error) {
	return s.base.ListUserOrgRoles(ctx, userID)
}

func (s *shadowingAuthStore) LookupUserProjectRole(ctx context.Context, userID, projectID string) (string, error) {
	return s.base.LookupUserProjectRole(ctx, userID, projectID)
}

func (s *shadowingAuthStore) CreatePersonalAccessToken(ctx context.Context, userID, label string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	return s.base.CreatePersonalAccessToken(ctx, userID, label, scopes, expiresAt, raw)
}

func (s *shadowingAuthStore) ListPersonalAccessTokens(ctx context.Context, userID string) ([]auth.PersonalAccessTokenRecord, error) {
	return s.base.ListPersonalAccessTokens(ctx, userID)
}

func (s *shadowingAuthStore) RevokePersonalAccessToken(ctx context.Context, tokenID, userID string) error {
	return s.base.RevokePersonalAccessToken(ctx, tokenID, userID)
}

func (s *shadowingAuthStore) CreateAutomationToken(ctx context.Context, projectID, label, createdByUserID string, scopes []string, expiresAt *time.Time, raw string) (string, error) {
	return s.base.CreateAutomationToken(ctx, projectID, label, createdByUserID, scopes, expiresAt, raw)
}

func (s *shadowingAuthStore) ListAutomationTokens(ctx context.Context, projectID string) ([]auth.AutomationTokenRecord, error) {
	return s.base.ListAutomationTokens(ctx, projectID)
}

func (s *shadowingAuthStore) RevokeAutomationToken(ctx context.Context, tokenID, projectID string) error {
	return s.base.RevokeAutomationToken(ctx, tokenID, projectID)
}

type shadowingAdminStore struct {
	base    controlplane.AdminStore
	shadows *sqlite.PrincipalShadowStore
}

func newShadowingAdminStore(base controlplane.AdminStore, shadows *sqlite.PrincipalShadowStore) *shadowingAdminStore {
	return &shadowingAdminStore{base: base, shadows: shadows}
}

func (s *shadowingAdminStore) ListOrgMembers(ctx context.Context, orgSlug string) ([]*controlplane.OrgMemberRecord, error) {
	return s.base.ListOrgMembers(ctx, orgSlug)
}

func (s *shadowingAdminStore) AddOrgMember(ctx context.Context, orgSlug, userID, role string) (*controlplane.OrgMemberRecord, error) {
	rec, err := s.base.AddOrgMember(ctx, orgSlug, userID, role)
	if err != nil || rec == nil {
		return rec, err
	}
	if err := s.shadows.UpsertOrganizationMember(ctx, *rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *shadowingAdminStore) RemoveOrgMember(ctx context.Context, orgSlug, userID string) (bool, error) {
	items, err := s.base.ListOrgMembers(ctx, orgSlug)
	if err != nil {
		return false, err
	}
	var organizationID string
	for _, item := range items {
		if item != nil && item.UserID == userID {
			organizationID = item.OrganizationID
			break
		}
	}
	removed, err := s.base.RemoveOrgMember(ctx, orgSlug, userID)
	if err != nil || !removed {
		return removed, err
	}
	if organizationID != "" {
		if err := s.shadows.DeleteOrganizationMember(ctx, organizationID, userID); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *shadowingAdminStore) GetOrgMember(ctx context.Context, orgSlug, memberID string) (*controlplane.OrgMemberRecord, error) {
	return s.base.GetOrgMember(ctx, orgSlug, memberID)
}

func (s *shadowingAdminStore) UpdateOrgMemberRole(ctx context.Context, orgSlug, memberID, role string) (*controlplane.OrgMemberRecord, error) {
	return s.base.UpdateOrgMemberRole(ctx, orgSlug, memberID, role)
}

func (s *shadowingAdminStore) ListTeams(ctx context.Context, orgSlug string) ([]*controlplane.TeamRecord, error) {
	return s.base.ListTeams(ctx, orgSlug)
}

func (s *shadowingAdminStore) GetTeam(ctx context.Context, orgSlug, teamSlug string) (*controlplane.TeamRecord, int, int, error) {
	return s.base.GetTeam(ctx, orgSlug, teamSlug)
}

func (s *shadowingAdminStore) CreateTeam(ctx context.Context, orgSlug, slug, name string) (*controlplane.TeamRecord, error) {
	return s.base.CreateTeam(ctx, orgSlug, slug, name)
}

func (s *shadowingAdminStore) UpdateTeam(ctx context.Context, orgSlug, teamSlug string, newName, newSlug *string) (*controlplane.TeamRecord, error) {
	return s.base.UpdateTeam(ctx, orgSlug, teamSlug, newName, newSlug)
}

func (s *shadowingAdminStore) DeleteTeam(ctx context.Context, orgSlug, teamSlug string) (bool, error) {
	return s.base.DeleteTeam(ctx, orgSlug, teamSlug)
}

func (s *shadowingAdminStore) ListTeamMembers(ctx context.Context, orgSlug, teamSlug string) ([]*controlplane.TeamMemberRecord, error) {
	return s.base.ListTeamMembers(ctx, orgSlug, teamSlug)
}

func (s *shadowingAdminStore) AddTeamMember(ctx context.Context, orgSlug, teamSlug, userID, role string) (*controlplane.TeamMemberRecord, error) {
	return s.base.AddTeamMember(ctx, orgSlug, teamSlug, userID, role)
}

func (s *shadowingAdminStore) RemoveTeamMember(ctx context.Context, orgSlug, teamSlug, userID string) (bool, error) {
	return s.base.RemoveTeamMember(ctx, orgSlug, teamSlug, userID)
}

func (s *shadowingAdminStore) ListInvites(ctx context.Context, orgSlug string) ([]*controlplane.InviteRecord, error) {
	return s.base.ListInvites(ctx, orgSlug)
}

func (s *shadowingAdminStore) CreateInvite(ctx context.Context, orgSlug, email, role, teamSlug, createdByUserID string) (*controlplane.InviteRecord, string, error) {
	return s.base.CreateInvite(ctx, orgSlug, email, role, teamSlug, createdByUserID)
}

func (s *shadowingAdminStore) RevokeInvite(ctx context.Context, orgSlug, inviteID string) (bool, error) {
	return s.base.RevokeInvite(ctx, orgSlug, inviteID)
}

func (s *shadowingAdminStore) ListProjectMembers(ctx context.Context, orgSlug, projectSlug string) ([]*controlplane.ProjectMemberRecord, error) {
	return s.base.ListProjectMembers(ctx, orgSlug, projectSlug)
}

func (s *shadowingAdminStore) UpdateProjectMemberRole(ctx context.Context, orgSlug, projectSlug, memberID, role string) (*controlplane.ProjectMemberRecord, error) {
	return s.base.UpdateProjectMemberRole(ctx, orgSlug, projectSlug, memberID, role)
}

func (s *shadowingAdminStore) AddProjectMember(ctx context.Context, orgSlug, projectSlug, userID, role string) (*controlplane.ProjectMemberRecord, error) {
	return s.base.AddProjectMember(ctx, orgSlug, projectSlug, userID, role)
}

func (s *shadowingAdminStore) ListUsers(ctx context.Context, orgID string, startIndex, count int, filter string) ([]scimcore.UserRecord, int, error) {
	store, ok := s.base.(scimcore.UserStore)
	if !ok {
		return []scimcore.UserRecord{}, 0, fmt.Errorf("scim user store unavailable")
	}
	return store.ListUsers(ctx, orgID, startIndex, count, filter)
}

func (s *shadowingAdminStore) GetUser(ctx context.Context, orgID, userID string) (*scimcore.UserRecord, error) {
	store, ok := s.base.(scimcore.UserStore)
	if !ok {
		return nil, fmt.Errorf("scim user store unavailable")
	}
	return store.GetUser(ctx, orgID, userID)
}

func (s *shadowingAdminStore) CreateUser(ctx context.Context, orgID string, user scimcore.UserRecord) (*scimcore.UserRecord, error) {
	store, ok := s.base.(scimcore.UserStore)
	if !ok {
		return nil, fmt.Errorf("scim user store unavailable")
	}
	return store.CreateUser(ctx, orgID, user)
}

func (s *shadowingAdminStore) PatchUser(ctx context.Context, orgID, userID string, ops []scimcore.PatchOp) (*scimcore.UserRecord, error) {
	store, ok := s.base.(scimcore.UserStore)
	if !ok {
		return nil, fmt.Errorf("scim user store unavailable")
	}
	return store.PatchUser(ctx, orgID, userID, ops)
}

func (s *shadowingAdminStore) ListTeamProjects(ctx context.Context, orgSlug, teamSlug string) ([]controlplane.TeamProjectRecord, error) {
	return s.base.ListTeamProjects(ctx, orgSlug, teamSlug)
}
func (s *shadowingAdminStore) ListUserTeams(ctx context.Context, orgSlug, userID string) ([]*controlplane.TeamRecord, error) {
	return s.base.ListUserTeams(ctx, orgSlug, userID)
}
func (s *shadowingAdminStore) AddMemberToTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (*controlplane.TeamMemberRecord, error) {
	return s.base.AddMemberToTeamByMemberID(ctx, orgSlug, memberID, teamSlug)
}
func (s *shadowingAdminStore) RemoveMemberFromTeamByMemberID(ctx context.Context, orgSlug, memberID, teamSlug string) (bool, error) {
	return s.base.RemoveMemberFromTeamByMemberID(ctx, orgSlug, memberID, teamSlug)
}
func (s *shadowingAdminStore) AcceptInvite(ctx context.Context, inviteToken, displayName, password string) (*controlplane.InviteAcceptanceResult, error) {
	result, err := s.base.AcceptInvite(ctx, inviteToken, displayName, password)
	if err != nil || result == nil {
		return result, err
	}
	if err := s.shadows.UpsertOrganizationMember(ctx, controlplane.OrgMemberRecord{
		OrganizationID:   result.OrganizationID,
		OrganizationSlug: result.OrganizationSlug,
		UserID:           result.User.ID,
		Email:            result.User.Email,
		Name:             result.User.DisplayName,
		Role:             result.Role,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func syncPostgresControlPlaneShadows(ctx context.Context, controlDB *sql.DB, shadows *sqlite.PrincipalShadowStore) error {
	if controlDB == nil || shadows == nil {
		return nil
	}
	users, err := postgrescontrol.ListActiveUsers(ctx, controlDB)
	if err != nil {
		return err
	}
	for i := range users {
		if err := shadows.UpsertUser(ctx, &users[i]); err != nil {
			return err
		}
	}
	members, err := postgrescontrol.ListOrganizationMembers(ctx, controlDB)
	if err != nil {
		return err
	}
	for i := range members {
		if err := shadows.UpsertOrganizationMember(ctx, members[i]); err != nil {
			return err
		}
	}
	return nil
}

func syncBootstrapUserShadow(ctx context.Context, store *postgrescontrol.AuthStore, shadows *sqlite.PrincipalShadowStore, email, password string) error {
	if store == nil || shadows == nil {
		return nil
	}
	user, err := store.AuthenticateUserPassword(ctx, email, password)
	if err != nil {
		return fmt.Errorf("load bootstrap user for sqlite shadow: %w", err)
	}
	if err := shadows.UpsertUser(ctx, user); err != nil {
		return err
	}
	return nil
}
