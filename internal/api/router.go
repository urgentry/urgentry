package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/attachment"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/integration"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

// authFunc is a per-request auth checker. Returns true if auth passes.
type authFunc func(w http.ResponseWriter, r *http.Request) bool

// Dependencies holds all stores needed by the API handlers.
type Dependencies struct {
	DB               *sql.DB
	Auth             *auth.Authorizer
	Control          controlplane.Services
	TokenManager     auth.TokenManager
	PrincipalShadows *sqlite.PrincipalShadowStore
	QueryGuard       sqlite.QueryGuard
	Operators        store.OperatorStore
	OperatorAudits   store.OperatorAuditStore
	Analytics        analyticsservice.Services
	Backfills        *sqlite.BackfillStore
	Audits           *sqlite.AuditStore
	NativeControl    *sqlite.NativeControlStore
	ReleaseHealth    *sqlite.ReleaseHealthStore
	DebugFiles       *sqlite.DebugFileStore
	Outcomes         *sqlite.OutcomeStore
	Retention        *sqlite.RetentionStore
	ImportExport     *sqlite.ImportExportStore
	Attachments      attachment.Store
	ProGuardStore    proguard.Store
	SourceMapStore   sourcemap.Store
	BlobStore        store.BlobStore
	Queries              telemetryquery.Service
	IntegrationRegistry  *integration.Registry
	IntegrationStore     integration.Store
	CodeMappings         store.CodeMappingStore
	ForwardingStore      store.ForwardingStore
	SamplingRules        *sqlite.SamplingRuleStore
	UptimeMonitors       *sqlite.UptimeMonitorStore
	Quota                *sqlite.QuotaStore
}

// ValidateDependencies checks the runtime dependencies needed to mount API
// routes. Request-layer constructors still panic on invalid deps, but callers
// that want startup-time validation can use this helper first.
func ValidateDependencies(deps Dependencies) error {
	if deps.DB == nil {
		return errors.New("requires a SQLite database")
	}
	if deps.Auth == nil {
		return errors.New("requires an authorizer")
	}
	if deps.Control.Catalog == nil || deps.Control.Admin == nil || deps.Control.Issues == nil || deps.Control.IssueReads == nil || deps.Control.Releases == nil || deps.Control.Monitors == nil {
		return errors.New("requires fully constructed control-plane services")
	}
	if deps.QueryGuard == nil {
		return errors.New("requires a query guard")
	}
	if deps.Analytics.Dashboards == nil {
		return errors.New("requires dashboard analytics service")
	}
	if deps.Queries == nil {
		return errors.New("requires a query service")
	}
	if deps.PrincipalShadows == nil {
		return errors.New("requires a principal shadow store")
	}
	return nil
}

// NewRouter creates an http.Handler with all Tier 1 API routes registered
// on its own internal mux.
func NewRouter(deps Dependencies) http.Handler {
	if err := ValidateDependencies(deps); err != nil {
		panic("api.NewRouter " + err.Error())
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps)
	return withCatalogContext(deps.Control.Catalog, mux)
}

// RegisterRoutes registers all API routes on the given mux. This allows
// sharing a mux with other route groups (e.g. ingest, web UI) without
// pattern conflicts.
func RegisterRoutes(mux *http.ServeMux, deps Dependencies) {
	if err := ValidateDependencies(deps); err != nil {
		panic("api.RegisterRoutes " + err.Error())
	}
	control := deps.Control
	queryGuard := deps.QueryGuard
	queries := deps.Queries
	principalShadows := deps.PrincipalShadows
	tokenManager := deps.TokenManager
	baseAuth := deps.Auth.API
	withAuth := func(policy auth.Policy) authFunc {
		check := baseAuth(policy)
		return func(w http.ResponseWriter, r *http.Request) bool {
			if control.Catalog != nil {
				*r = *r.WithContext(context.WithValue(r.Context(), catalogContextKey{}, control.Catalog))
			}
			return check(w, r)
		}
	}

	// Organizations
	mux.Handle("GET /api/0/organizations/", handleListOrgs(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
	mux.Handle("GET /api/0/organizations/{org_slug}/", handleGetOrg(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/", handleUpdateOrg(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))

	// Org sub-resources
	mux.Handle("GET /api/0/organizations/{org_slug}/environments/", handleListOrgEnvironments(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/projects/", handleListOrgProjects(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/teams/", handleListTeams(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/audit-logs/", handleListAuditLogs(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/ops/overview/", handleGetOperatorOverview(deps.Operators, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/ops/diagnostics/", handleGetOperatorDiagnostics(deps.Operators, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/", handleListOrganizationIssues(control.Catalog, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/issues/", handleBulkMutateOrgIssues(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/issues/", handleBulkDeleteOrgIssues(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/events/{event_id}/", handleGetIssueEvent(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/hashes/", handleListIssueHashes(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/", handleGetIssueTagDetail(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/values/", handleListIssueTagValues(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/discover/", handleDiscover(control.Catalog, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/logs/", handleListOrganizationLogs(control.Catalog, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/dashboards/", handleListDashboards(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/dashboards/", handleCreateDashboard(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleGetDashboard(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleUpdateDashboard(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleDeleteDashboard(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/", handleCreateDashboardWidget(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/", handleUpdateDashboardWidget(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/", handleDeleteDashboardWidget(deps.Analytics.Dashboards, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/backfills/", handleListBackfills(deps.DB, deps.Backfills, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/backfills/", handleCreateBackfill(deps.DB, deps.Backfills, deps.NativeControl, deps.Audits, deps.OperatorAudits, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/backfills/{run_id}/", handleGetBackfill(deps.DB, deps.Backfills, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/backfills/{run_id}/cancel/", handleCancelBackfill(deps.DB, deps.Backfills, deps.Audits, deps.OperatorAudits, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/teams/", handleCreateTeam(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/eventids/{event_id}/", handleResolveEventID(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/shortids/{short_id}/", handleResolveShortID(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/members/", handleListOrgMembers(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/members/", handleAddOrgMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/members/{member_id}/", handleGetOrgMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/members/{member_id}/", handleUpdateOrgMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/members/{member_id}/", handleRemoveOrgMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/", handleAddMemberToTeam(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/members/{member_id}/teams/{team_slug}/", handleRemoveMemberFromTeam(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/user-teams/", handleListUserTeams(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/invites/", handleListInvites(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/invites/", handleCreateInvite(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/invites/{invite_id}/", handleRevokeInvite(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/events/", handleListOrgEvents(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/", handleListReleases(control.Catalog, control.Releases, deps.NativeControl, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/", handleCreateRelease(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/", handleGetRelease(control.Catalog, control.Releases, deps.NativeControl, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/releases/{version}/", handleDeleteRelease(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/releases/{version}/", handleUpdateRelease(control.Catalog, control.Releases, deps.NativeControl, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/deploys/", handleListReleaseDeploys(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/deploys/", handleCreateReleaseDeploy(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/commits/", handleListReleaseCommits(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/commits/", handleCreateReleaseCommit(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/suspects/", handleListReleaseSuspects(control.Releases, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))

	// Org-level release files
	if smStore, ok := deps.SourceMapStore.(*sqlite.SourceMapStore); ok && smStore != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/files/", handleListReleaseFiles(control.Catalog, smStore, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/files/", handleUploadReleaseFile(control.Catalog, smStore, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleGetReleaseFile(control.Catalog, smStore, withAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleUpdateReleaseFile(control.Catalog, smStore, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleDeleteReleaseFile(control.Catalog, smStore, withAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	}

	// Projects (global)
	mux.Handle("GET /api/0/projects/", handleListAllProjects(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceAnyMembership})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/", handleGetProject(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/", handleDeleteProject(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceProjectPath})))

	// Project sub-resources
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/members/", handleListProjectMembers(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/members/{member_id}/", handleUpdateProjectMemberRole(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/settings/", handleGetProjectSettings(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/settings/", handleUpdateProjectSettings(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archives/", handleListRetentionArchives(deps.DB, deps.Retention, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archive/", handleExecuteRetentionArchive(deps.DB, deps.Retention, deps.Audits, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/restore/", handleExecuteRetentionRestore(deps.DB, deps.Retention, deps.Audits, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/ownership/", handleListOwnershipRules(control.Catalog, control.Ownership, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/ownership/", handleCreateOwnershipRule(control.Catalog, control.Ownership, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/ownership/{rule_id}/", handleDeleteOwnershipRule(control.Catalog, control.Ownership, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/keys/", handleListKeys(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectKeysRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/keys/", handleCreateKey(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectKeysWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/", handleGetKey(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectKeysRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/", handleUpdateKey(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectKeysWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/keys/{key_id}/", handleDeleteKey(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectKeysWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/", handleListAlertRules(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/alerts/", handleCreateAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/", handleGetAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/", handleUpdateAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/alerts/{rule_id}/", handleDeleteAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))

	// Issue alert rules (Sentry-compatible /rules/ path)
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/rules/", handleListIssueAlertRules(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/rules/", handleCreateIssueAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/", handleGetIssueAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/", handleUpdateIssueAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/", handleDeleteIssueAlertRule(control.Catalog, control.Alerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))

	if control.MetricAlerts != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/metric-alerts/", handleListMetricAlertRules(control.Catalog, control.MetricAlerts, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/metric-alerts/", handleCreateMetricAlertRule(control.Catalog, control.MetricAlerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/metric-alerts/{rule_id}/", handleGetMetricAlertRule(control.Catalog, control.MetricAlerts, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/metric-alerts/{rule_id}/", handleUpdateMetricAlertRule(control.Catalog, control.MetricAlerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/metric-alerts/{rule_id}/", handleDeleteMetricAlertRule(control.Catalog, control.MetricAlerts, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/outbox/", handleListAlertOutbox(control.Catalog, control.Outbox, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/deliveries/", handleListAlertDeliveries(control.Catalog, control.Deliveries, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.Auth != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/alerts/test-webhook/", handleTestAlertWebhook(control.Catalog, control.Deliveries, deps.Auth, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}
	if deps.ProGuardStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/", handleListProGuardMappings(deps.DB, deps.ProGuardStore, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/", handleUploadProGuardMapping(deps.DB, deps.ProGuardStore, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/{uuid}/", handleLookupProGuardMapping(deps.DB, deps.ProGuardStore, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/health/", handleGetReleaseHealth(deps.DB, deps.ReleaseHealth, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/sessions/", handleListReleaseSessions(deps.DB, deps.ReleaseHealth, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/outcomes/", handleListOutcomes(deps.DB, deps.Outcomes, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/", handleListMonitors(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/monitors/", handleCreateMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleGetMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleUpdateMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleDeleteMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/check-ins/", handleListMonitorCheckIns(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.BlobStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/", handleListDebugFiles(deps.DB, deps.NativeControl, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/", handleUploadDebugFile(deps.DB, deps.DebugFiles, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/", handleDownloadDebugFile(deps.DB, deps.DebugFiles, deps.NativeControl, deps.BlobStore, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/reprocess/", handleReprocessDebugFile(deps.DB, deps.DebugFiles, deps.NativeControl, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceProjectPath})))
	}
	if deps.Auth != nil && tokenManager != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/", handleListAutomationTokens(control.Catalog, tokenManager, withAuth(auth.Policy{Scope: auth.ScopeProjectTokensRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/", handleCreateAutomationToken(control.Catalog, deps.Auth, tokenManager, principalShadows, withAuth(auth.Policy{Scope: auth.ScopeProjectTokensWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/{token_id}/", handleRevokeAutomationToken(control.Catalog, deps.Auth, tokenManager, withAuth(auth.Policy{Scope: auth.ScopeProjectTokensWrite, Resource: auth.ResourceProjectPath})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/issues/", handleListProjectIssues(control.Catalog, control.IssueReads, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/issues/", handleBulkMutateProjectIssues(control.Catalog, control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/issues/", handleBulkDeleteProjectIssues(control.Catalog, control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/", handleListProjectEvents(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/", handleGetProjectEvent(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/source-map-debug/", handleSourceMapDebug(deps.DB, deps.SourceMapStore, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/tags/{key}/values/", handleListProjectTagValues(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/environments/", handleListProjectEnvironments(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/environments/{env_name}/", handleGetProjectEnvironment(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/environments/{env_name}/", handleUpdateProjectEnvironment(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/teams/", handleListProjectTeams(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/teams/{team_slug}/", handleAddProjectTeam(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/teams/{team_slug}/", handleRemoveProjectTeam(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/", handleListReplays(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/", handleGetReplay(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/manifest/", handleGetReplayManifest(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/timeline/", handleListReplayTimeline(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/panes/{pane}/", handleListReplayPane(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.BlobStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/assets/{attachment_id}/", handleDownloadReplayAsset(deps.DB, queries, deps.BlobStore, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/", handleListProfiles(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/{profile_id}/", handleGetProfile(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/top-down/", handleProfileTopDown(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/bottom-up/", handleProfileBottomUp(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/flamegraph/", handleProfileFlamegraph(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/hot-path/", handleProfileHotPath(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/compare/", handleCompareProfiles(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/transactions/", handleListTransactions(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/traces/{trace_id}/", handleGetTrace(deps.DB, queries, queryGuard, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.Attachments != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/attachments/", handleUploadProjectAttachment(deps.DB, deps.Attachments, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/events/{event_id}/attachments/", handleListEventAttachments(deps.Attachments, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceEventPath})))
		mux.Handle("GET /api/0/events/{event_id}/attachments/{attachment_id}/", handleDownloadEventAttachment(deps.Attachments, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceEventPath})))
	}

	if deps.Auth != nil && tokenManager != nil {
		mux.Handle("GET /api/0/users/me/personal-access-tokens/", handleListPersonalAccessTokens(tokenManager, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
		mux.Handle("POST /api/0/users/me/personal-access-tokens/", handleCreatePersonalAccessToken(deps.Auth, tokenManager, principalShadows, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
		mux.Handle("DELETE /api/0/users/me/personal-access-tokens/{token_id}/", handleRevokePersonalAccessToken(deps.Auth, tokenManager, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceAnyMembership})))
	}

	// Teams
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/", handleGetTeamDetail(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/teams/{org_slug}/{team_slug}/", handleUpdateTeam(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/teams/{org_slug}/{team_slug}/", handleDeleteTeam(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/projects/", handleListTeamProjects(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/teams/{org_slug}/{team_slug}/projects/", handleCreateProject(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/teams/{org_slug}/{team_slug}/members/", handleListTeamMembers(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/teams/{org_slug}/{team_slug}/members/", handleAddTeamMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/teams/{org_slug}/{team_slug}/members/{member_id}/", handleRemoveTeamMember(control.Admin, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))

	// Issues
	mux.Handle("GET /api/0/issues/{issue_id}/", handleGetIssue(control.IssueReads, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("PUT /api/0/issues/{issue_id}/", handleUpdateIssue(control.IssueReads, control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("DELETE /api/0/issues/{issue_id}/", handleDeleteIssue(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/events/", handleListIssueEvents(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/events/latest/", handleGetLatestIssueEvent(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/comments/", handleListIssueComments(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/comments/", handleCreateIssueComment(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/activity/", handleListIssueActivity(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/merge/", handleMergeIssue(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/unmerge/", handleUnmergeIssue(control.Issues, withAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))

	// Import / Export
	mux.Handle("POST /api/0/organizations/{org_slug}/import/", handleImport(deps.DB, deps.ImportExport, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/export/", handleExport(deps.DB, deps.ImportExport, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/invites/{invite_token}/accept/", handleAcceptInvite(control.Admin))

	// Integrations
	if deps.IntegrationRegistry != nil && deps.IntegrationStore != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/integrations/", handleListIntegrations(control.Catalog, deps.IntegrationRegistry, deps.IntegrationStore, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/integrations/{integration_id}/install", handleInstallIntegration(control.Catalog, deps.IntegrationRegistry, deps.IntegrationStore, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/integrations/{integration_id}/", handleUninstallIntegration(control.Catalog, deps.IntegrationStore, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/integrations/{integration_id}/webhook", handleIntegrationWebhook(control.Catalog, deps.IntegrationRegistry, deps.IntegrationStore))
	}

	// Source map uploads
	if deps.SourceMapStore != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/", handleUploadSourceMap(deps.DB, deps.SourceMapStore, withAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
	}

	// Code mappings
	if deps.CodeMappings != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/code-mappings/", handleListCodeMappings(control.Catalog, deps.CodeMappings, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/code-mappings/", handleCreateCodeMapping(control.Catalog, deps.CodeMappings, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/code-mappings/{mapping_id}/", handleDeleteCodeMapping(control.Catalog, deps.CodeMappings, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Data forwarding
	if deps.ForwardingStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/data-forwarding/", handleListDataForwarding(control.Catalog, deps.ForwardingStore, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/data-forwarding/", handleCreateDataForwarding(control.Catalog, deps.ForwardingStore, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/data-forwarding/{forwarding_id}/", handleDeleteDataForwarding(control.Catalog, deps.ForwardingStore, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Sampling rules
	if deps.SamplingRules != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/sampling-rules/", handleListSamplingRules(control.Catalog, deps.SamplingRules, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/sampling-rules/", handleCreateSamplingRule(control.Catalog, deps.SamplingRules, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/sampling-rules/{rule_id}/", handleDeleteSamplingRule(control.Catalog, deps.SamplingRules, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Uptime monitors
	if deps.UptimeMonitors != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/uptime-monitors/", handleListUptimeMonitors(control.Catalog, deps.UptimeMonitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/uptime-monitors/", handleCreateUptimeMonitor(control.Catalog, deps.UptimeMonitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/uptime-monitors/{monitor_id}/", handleGetUptimeMonitor(deps.UptimeMonitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/uptime-monitors/{monitor_id}/", handleDeleteUptimeMonitor(deps.UptimeMonitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/uptime-monitors/{monitor_id}/results/", handleListUptimeCheckResults(deps.UptimeMonitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	}

	// Quota management
	if deps.Quota != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/quota/usage/", handleGetQuotaUsage(control.Catalog, deps.Quota, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/quota/rate-limits/", handleListQuotaRateLimits(deps.Quota, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/quota/rate-limits/", handleUpsertQuotaRateLimit(deps.Quota, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/quota/rate-limits/{project_id}/", handleDeleteQuotaRateLimit(deps.Quota, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}
}
