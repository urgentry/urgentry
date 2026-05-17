package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/integration"
	scimcore "urgentry/internal/scim"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type IdentityIntegrationRoutes struct {
	DB                  *sql.DB
	Auth                *auth.Authorizer
	Catalog             controlplane.CatalogStore
	Admin               controlplane.AdminStore
	TokenManager        auth.TokenManager
	PrincipalShadows    *sqlite.PrincipalShadowStore
	Audits              *sqlite.AuditStore
	SCIMUsers           scimcore.UserStore
	IntegrationRegistry *integration.Registry
	IntegrationStore    integration.Store
	SentryAppStore      integration.AppStore
	ExternalIssues      integration.ExternalIssueStore
	ExternalUsers       store.ExternalUserStore
	ExternalTeams       store.ExternalTeamStore
	OrgForwarders       store.OrgForwarderStore
	Prevent             store.PreventStore
	WithAuth            policyAuthFunc
}

func RegisterIdentityIntegrationRoutes(mux *http.ServeMux, routes IdentityIntegrationRoutes) {
	// Organization identity, membership, and SCIM-adjacent management.
	mux.Handle("POST /api/0/organizations/{org_slug}/teams/", handleCreateTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/users/", handleListOrgUsers(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/members/", handleListOrgMembers(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/members/", handleCreateInvite(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/members/{member_id}/", handleGetOrgMember(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/members/{member_id}/", handleUpdateOrgMember(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/members/{member_id}/", handleRemoveOrgMember(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	RegisterSCIMRoutes(mux, routes.Catalog, routes.SCIMUsers, routes.Audits, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath}))
	RegisterSCIMGroupRoutes(mux, routes.Catalog, routes.Admin, routes.Audits, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath}))
	mux.Handle("POST /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/", handleAddMemberToTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/", handleAddMemberToTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/", handleRemoveMemberFromTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/user-teams/", handleListUserTeams(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/invites/", handleListInvites(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/invites/", handleCreateInvite(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/invites/{invite_id}/", handleRevokeInvite(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/invites/{invite_token}/accept/", handleAcceptInvite(routes.Admin))

	if routes.Auth != nil && routes.TokenManager != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/", handleListAutomationTokens(routes.Catalog, routes.TokenManager, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/", handleCreateAutomationToken(routes.Catalog, routes.Auth, routes.TokenManager, routes.PrincipalShadows, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/{token_id}/", handleRevokeAutomationToken(routes.Catalog, routes.Auth, routes.TokenManager, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("GET /api/0/users/me/personal-access-tokens/", handleListPersonalAccessTokens(routes.TokenManager, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
		mux.Handle("POST /api/0/users/me/personal-access-tokens/", handleCreatePersonalAccessToken(routes.Auth, routes.TokenManager, routes.PrincipalShadows, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
		mux.Handle("DELETE /api/0/users/me/personal-access-tokens/{token_id}/", handleRevokePersonalAccessToken(routes.Auth, routes.TokenManager, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
	}

	// Team management aliases.
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/", handleGetTeamDetail(routes.Catalog, routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/teams/{org_slug}/{team_slug}/", handleUpdateTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/teams/{org_slug}/{team_slug}/", handleDeleteTeam(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/projects/", handleListTeamProjects(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/teams/{org_slug}/{team_slug}/projects/", handleCreateProject(routes.Catalog, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/members/", handleListTeamMembers(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/teams/{org_slug}/{team_slug}/members/", handleAddTeamMember(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/teams/{org_slug}/{team_slug}/members/{member_id}/", handleRemoveTeamMember(routes.Admin, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))

	// Prevent repository and test analytics surfaces.
	mux.Handle("GET /api/0/organizations/{org_slug}/repos/", handleListOrgRepos(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/repos/", handleCreateOrgRepo(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/repos/{repo_id}/commits/", handleListRepoCommits(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/", handleListPreventRepositories(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/sync/", handleGetPreventRepositoriesSync(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/sync/", handleStartPreventRepositoriesSync(routes.Catalog, routes.Auth, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/tokens/", handleListPreventRepositoryTokens(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/", handleGetPreventRepository(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/branches/", handleListPreventRepositoryBranches(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-results/", handleListPreventRepositoryTestResults(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-suites/", handleListPreventRepositoryTestSuites(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-results-aggregates/", handleListPreventRepositoryTestResultsAggregates(routes.Catalog, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/token/regenerate/", handleRegeneratePreventRepositoryToken(routes.Catalog, routes.Auth, routes.Prevent, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectTokensWrite, Resource: auth.ResourceOrganizationPath})))

	// Integrations and Sentry app surfaces.
	if routes.IntegrationRegistry != nil && routes.IntegrationStore != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/integrations/", handleListIntegrations(routes.Catalog, routes.IntegrationRegistry, routes.IntegrationStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/integrations/{integration_id}/", handleGetIntegration(routes.Catalog, routes.IntegrationRegistry, routes.IntegrationStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/integrations/{integration_id}/install", handleInstallIntegration(routes.Catalog, routes.IntegrationRegistry, routes.IntegrationStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/integrations/{integration_id}/", handleUninstallIntegration(routes.Catalog, routes.IntegrationStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/integrations/{integration_id}/webhook", handleIntegrationWebhook(routes.Catalog, routes.IntegrationRegistry, routes.IntegrationStore))
		mux.Handle("GET /api/0/organizations/{org_slug}/config/integrations/", handleListIntegrationConfigs(routes.IntegrationRegistry, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		if routes.SentryAppStore != nil {
			mux.Handle("GET /api/0/organizations/{org_slug}/sentry-apps/", handleListSentryApps(routes.Catalog, routes.IntegrationRegistry, routes.SentryAppStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
			mux.Handle("GET /api/0/organizations/{org_slug}/sentry-app-installations/", handleListSentryAppInstallations(routes.Catalog, routes.IntegrationRegistry, routes.SentryAppStore, routes.IntegrationStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
			mux.Handle("GET /api/0/sentry-apps/{sentry_app_id_or_slug}/", handleGetSentryApp(routes.IntegrationRegistry, routes.SentryAppStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
			mux.Handle("PUT /api/0/sentry-apps/{sentry_app_id_or_slug}/", handleUpdateSentryApp(routes.IntegrationRegistry, routes.SentryAppStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceAnyMembership})))
			mux.Handle("DELETE /api/0/sentry-apps/{sentry_app_id_or_slug}/", handleDeleteSentryApp(routes.IntegrationRegistry, routes.SentryAppStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceAnyMembership})))
		}
		if routes.ExternalIssues != nil {
			mux.Handle("POST /api/0/sentry-app-installations/{uuid}/external-issues/", handleUpsertInstallationExternalIssue(routes.DB, routes.Catalog, routes.Auth, routes.IntegrationStore, routes.ExternalIssues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceAnyMembership})))
			mux.Handle("DELETE /api/0/sentry-app-installations/{uuid}/external-issues/{external_issue_id}/", handleDeleteInstallationExternalIssue(routes.Auth, routes.ExternalIssues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceAnyMembership})))
		}
	}

	if routes.ExternalUsers != nil {
		mux.Handle("POST /api/0/organizations/{org_slug}/external-users/", handleCreateExternalUser(routes.Catalog, routes.ExternalUsers, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/external-users/{id}/", handleUpdateExternalUser(routes.ExternalUsers, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/external-users/{id}/", handleDeleteExternalUser(routes.ExternalUsers, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}
	if routes.ExternalTeams != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/external-teams/", handleListExternalTeams(routes.Catalog, routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/external-teams/", handleCreateExternalTeam(routes.Catalog, routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/external-teams/{id}/", handleUpdateExternalTeam(routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/external-teams/{id}/", handleDeleteExternalTeam(routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/teams/{org_slug}/{team_slug}/external-teams/", handleCreateTeamExternalTeam(routes.Catalog, routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/teams/{org_slug}/{team_slug}/external-teams/{external_team_id}/", handleUpdateTeamExternalTeam(routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/teams/{org_slug}/{team_slug}/external-teams/{external_team_id}/", handleDeleteTeamExternalTeam(routes.ExternalTeams, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}
	if routes.OrgForwarders != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/forwarding/", handleListOrgForwarding(routes.Catalog, routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/forwarding/", handleCreateOrgForwarding(routes.Catalog, routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/forwarding/{id}/", handleUpdateOrgForwarding(routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/forwarding/{id}/", handleDeleteOrgForwarding(routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/data-forwarding/", handleListOrgForwarding(routes.Catalog, routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/data-forwarding/", handleCreateOrgForwarding(routes.Catalog, routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/data-forwarding/{id}/", handleDeleteOrgForwarding(routes.OrgForwarders, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}
}
