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
	scimcore "urgentry/internal/scim"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

// authFunc is a per-request auth checker. Returns true if auth passes.
type authFunc func(w http.ResponseWriter, r *http.Request) bool

// Dependencies holds all stores needed by the API handlers.
type Dependencies struct {
	DB                  *sql.DB
	Auth                *auth.Authorizer
	Control             controlplane.Services
	TokenManager        auth.TokenManager
	PrincipalShadows    *sqlite.PrincipalShadowStore
	QueryGuard          sqlite.QueryGuard
	Operators           store.OperatorStore
	OperatorAudits      store.OperatorAuditStore
	Analytics           analyticsservice.Services
	Backfills           *sqlite.BackfillStore
	Audits              *sqlite.AuditStore
	NativeControl       *sqlite.NativeControlStore
	ReleaseHealth       *sqlite.ReleaseHealthStore
	DebugFiles          *sqlite.DebugFileStore
	Outcomes            *sqlite.OutcomeStore
	Retention           *sqlite.RetentionStore
	ImportExport        *sqlite.ImportExportStore
	Attachments         attachment.Store
	ProGuardStore       proguard.Store
	SourceMapStore      sourcemap.Store
	BlobStore           store.BlobStore
	Queries             telemetryquery.Service
	IntegrationRegistry *integration.Registry
	IntegrationStore    integration.Store
	SentryAppStore      integration.AppStore
	ExternalIssues      integration.ExternalIssueStore
	CodeMappings        store.CodeMappingStore
	ForwardingStore     store.ForwardingStore
	PreprodArtifacts    *sqlite.PreprodArtifactStore
	Autofix             *sqlite.AutofixStore
	SamplingRules       *sqlite.SamplingRuleStore
	UptimeMonitors      *sqlite.UptimeMonitorStore
	Quota               *sqlite.QuotaStore
	SymbolSources       *sqlite.SymbolSourceStore
	InboundFilters      *sqlite.InboundFilterStore
	Hooks               *sqlite.HookStore
	FeedbackStore       *sqlite.FeedbackStore
	Detectors           store.DetectorStore
	Workflows           store.WorkflowStore
	SCIMUsers           scimcore.UserStore
	ExternalUsers       store.ExternalUserStore
	ExternalTeams       store.ExternalTeamStore
	OrgForwarders       store.OrgForwarderStore
	Prevent             store.PreventStore
	NotificationActions *sqlite.NotificationActionStore
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

// BuildRouter creates an http.Handler with all Tier 1 API routes registered
// on its own internal mux.
func BuildRouter(deps Dependencies) (http.Handler, error) {
	if err := ValidateDependencies(deps); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	if err := RegisterRoutesInto(mux, deps); err != nil {
		return nil, err
	}
	return withCatalogContext(deps.Control.Catalog, mux), nil
}

// NewRouter keeps the legacy panic-on-invalid-deps behavior for callers that
// intentionally want a hard failure on invalid construction.
func NewRouter(deps Dependencies) http.Handler {
	handler, err := BuildRouter(deps)
	if err != nil {
		panic("api.NewRouter " + err.Error())
	}
	return handler
}

// RegisterRoutes keeps the legacy panic-on-invalid-deps behavior for direct
// misuse in tests or ad hoc wiring.
func RegisterRoutes(mux *http.ServeMux, deps Dependencies) {
	if err := RegisterRoutesInto(mux, deps); err != nil {
		panic("api.RegisterRoutes " + err.Error())
	}
}

// RegisterRoutesInto registers all API routes on the given mux. This allows
// sharing a mux with other route groups (e.g. ingest, web UI) without
// pattern conflicts.
func RegisterRoutesInto(mux *http.ServeMux, deps Dependencies) error {
	if err := ValidateDependencies(deps); err != nil {
		return err
	}
	control := deps.Control
	queryGuard := deps.QueryGuard
	queries := deps.Queries
	scimUsers := deps.SCIMUsers
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
	if scimUsers == nil {
		if candidate, ok := any(control.Admin).(scimcore.UserStore); ok {
			scimUsers = candidate
		}
	}

	// Root capabilities
	mux.Handle("GET /api/0/", handleRootCapabilities())

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
	RegisterIssueRoutes(mux, IssueRoutes{
		DB:             deps.DB,
		Catalog:        control.Catalog,
		IssueReads:     control.IssueReads,
		Issues:         control.Issues,
		Hooks:          deps.Hooks,
		Autofix:        deps.Autofix,
		ExternalIssues: deps.ExternalIssues,
		Queries:        queries,
		QueryGuard:     queryGuard,
		WithAuth:       withAuth,
	})
	RegisterAnalyticsRoutes(mux, AnalyticsRoutes{
		DB:         deps.DB,
		Catalog:    control.Catalog,
		Monitors:   control.Monitors,
		Analytics:  deps.Analytics,
		Queries:    queries,
		QueryGuard: queryGuard,
		BlobStore:  deps.BlobStore,
		WithAuth:   withAuth,
	})
	mux.Handle("GET /api/0/organizations/{org_slug}/backfills/", handleListBackfills(deps.DB, deps.Backfills, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/backfills/", handleCreateBackfill(deps.DB, deps.Backfills, deps.NativeControl, deps.Audits, deps.OperatorAudits, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/backfills/{run_id}/", handleGetBackfill(deps.DB, deps.Backfills, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/backfills/{run_id}/cancel/", handleCancelBackfill(deps.DB, deps.Backfills, deps.Audits, deps.OperatorAudits, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	RegisterIdentityIntegrationRoutes(mux, IdentityIntegrationRoutes{
		DB:                  deps.DB,
		Auth:                deps.Auth,
		Catalog:             control.Catalog,
		Admin:               control.Admin,
		TokenManager:        tokenManager,
		PrincipalShadows:    principalShadows,
		Audits:              deps.Audits,
		SCIMUsers:           scimUsers,
		IntegrationRegistry: deps.IntegrationRegistry,
		IntegrationStore:    deps.IntegrationStore,
		SentryAppStore:      deps.SentryAppStore,
		ExternalIssues:      deps.ExternalIssues,
		ExternalUsers:       deps.ExternalUsers,
		ExternalTeams:       deps.ExternalTeams,
		OrgForwarders:       deps.OrgForwarders,
		Prevent:             deps.Prevent,
		WithAuth:            withAuth,
	})
	RegisterReleaseArtifactRoutes(mux, ReleaseArtifactRoutes{
		DB:               deps.DB,
		Catalog:          control.Catalog,
		Releases:         control.Releases,
		NativeControl:    deps.NativeControl,
		ReleaseHealth:    deps.ReleaseHealth,
		DebugFiles:       deps.DebugFiles,
		PreprodArtifacts: deps.PreprodArtifacts,
		Attachments:      deps.Attachments,
		ProGuardStore:    deps.ProGuardStore,
		SourceMapStore:   deps.SourceMapStore,
		BlobStore:        deps.BlobStore,
		WithAuth:         withAuth,
	})
	mux.Handle("GET /api/0/organizations/{org_slug}/eventids/{event_id}/", handleResolveEventID(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/shortids/{short_id}/", handleResolveShortID(deps.DB, control.Issues, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/project-keys/", handleListOrgProjectKeys(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/stats_v2/", handleGetStatsV2(deps.DB, deps.Outcomes, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/sessions/", handleListOrgSessions(deps.DB, deps.ReleaseHealth, withAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))

	// Projects (global)
	mux.Handle("GET /api/0/projects/", handleListAllProjects(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceAnyMembership})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/", handleGetProject(deps.DB, control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/", handleUpdateProject(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
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
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/ownership/", handleCreateOwnershipRule(control.Catalog, control.Ownership, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
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

	RegisterMetricAlertRoutes(mux, MetricAlertRoutes{Catalog: control.Catalog, Store: control.MetricAlerts, WithAuth: withAuth})
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/outbox/", handleListAlertOutbox(control.Catalog, control.Outbox, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/alerts/deliveries/", handleListAlertDeliveries(control.Catalog, control.Deliveries, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.Auth != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/alerts/test-webhook/", handleTestAlertWebhook(control.Catalog, control.Deliveries, deps.Auth, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/outcomes/", handleListOutcomes(deps.DB, deps.Outcomes, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/", handleListMonitors(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/monitors/", handleCreateMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleGetMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleUpdateMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/", handleDeleteMonitor(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/check-ins/", handleListMonitorCheckIns(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/checkins/", handleListMonitorCheckIns(control.Catalog, control.Monitors, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/", handleGetProjectEvent(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/tags/{key}/values/", handleListProjectTagValues(deps.DB, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/environments/", handleListProjectEnvironments(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/environments/{env_name}/", handleGetProjectEnvironment(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/environments/{env_name}/", handleUpdateProjectEnvironment(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/teams/", handleListProjectTeams(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/teams/{team_slug}/", handleAddProjectTeam(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/teams/{team_slug}/", handleRemoveProjectTeam(control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	// Import / Export
	mux.Handle("POST /api/0/organizations/{org_slug}/import/", handleImport(deps.DB, deps.ImportExport, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/export/", handleExport(deps.DB, deps.ImportExport, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))

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

	// Symbol sources
	if deps.SymbolSources != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/symbol-sources/", handleListSymbolSources(control.Catalog, deps.SymbolSources, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/symbol-sources/", handleCreateSymbolSource(control.Catalog, deps.SymbolSources, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/symbol-sources/", handleUpdateSymbolSource(control.Catalog, deps.SymbolSources, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/symbol-sources/", handleDeleteSymbolSource(control.Catalog, deps.SymbolSources, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Notification actions
	if deps.NotificationActions != nil { //nolint:dupl
		mux.Handle("GET /api/0/organizations/{org_slug}/notifications/actions/", handleListNotificationActions(control.Catalog, deps.NotificationActions, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/notifications/actions/", handleCreateNotificationAction(control.Catalog, deps.NotificationActions, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/notifications/actions/{action_id}/", handleGetNotificationAction(control.Catalog, deps.NotificationActions, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/notifications/actions/{action_id}/", handleUpdateNotificationAction(control.Catalog, deps.NotificationActions, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/notifications/actions/{action_id}/", handleDeleteNotificationAction(control.Catalog, deps.NotificationActions, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}

	// Data filters
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/filters/", handleListDataFilters(control.Catalog, deps.InboundFilters, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.InboundFilters != nil {
		mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/filters/{filter_id}/", handleUpdateDataFilter(control.Catalog, deps.InboundFilters, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Service hooks
	if deps.Hooks != nil { //nolint:dupl
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/hooks/", handleListHooks(control.Catalog, deps.Hooks, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/hooks/", handleCreateHook(control.Catalog, deps.Hooks, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/hooks/{hook_id}/", handleGetHook(control.Catalog, deps.Hooks, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/hooks/{hook_id}/", handleUpdateHook(control.Catalog, deps.Hooks, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
		mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/hooks/{hook_id}/", handleDeleteHook(control.Catalog, deps.Hooks, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// User feedback
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/user-feedback/", handleListUserFeedback(control.Catalog, deps.FeedbackStore, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if deps.FeedbackStore != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/user-feedback/", handleSubmitUserFeedback(control.Catalog, deps.FeedbackStore, withAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	}

	// Project stats and users
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/stats/", handleGetProjectStats(deps.DB, control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/users/", handleListProjectUsers(deps.DB, control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/processingissues/", handleListProcessingIssues(withAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))

	// Spike protection
	mux.Handle("POST /api/0/organizations/{org_slug}/spike-protections/", handleEnableSpikeProtection(deps.DB, control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/spike-protections/", handleDisableSpikeProtection(deps.DB, control.Catalog, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))

	// Detectors
	if deps.Detectors != nil { //nolint:dupl
		mux.Handle("GET /api/0/organizations/{org_slug}/detectors/", handleListDetectors(control.Catalog, deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/detectors/", handleCreateDetector(control.Catalog, deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/detectors/", handleBulkUpdateDetectors(control.Catalog, deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/detectors/", handleBulkDeleteDetectors(control.Catalog, deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/detectors/{detector_id}/", handleGetDetector(deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/detectors/{detector_id}/", handleUpdateDetector(deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/detectors/{detector_id}/", handleDeleteDetector(deps.Detectors, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}

	// Workflows
	if deps.Workflows != nil { //nolint:dupl
		mux.Handle("GET /api/0/organizations/{org_slug}/workflows/", handleListWorkflows(control.Catalog, deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/workflows/", handleCreateWorkflow(control.Catalog, deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/workflows/", handleBulkUpdateWorkflows(control.Catalog, deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/workflows/", handleBulkDeleteWorkflows(control.Catalog, deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/workflows/{workflow_id}/", handleGetWorkflow(deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/workflows/{workflow_id}/", handleUpdateWorkflow(deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/workflows/{workflow_id}/", handleDeleteWorkflow(deps.Workflows, withAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	}

	// Stub endpoints (P3 - return empty data)
	mux.Handle("GET /api/0/organizations/{org_slug}/relay_usage/", handleRelayUsage(deps.Audits, withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/release-threshold-statuses/", handleReleaseThresholdStatuses(withAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/seer/models/", handleSeerModels())
	return nil
}
