package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

type AnalyticsRoutes struct {
	DB         *sql.DB
	Catalog    controlplane.CatalogStore
	Monitors   controlplane.MonitorStore
	Analytics  analyticsservice.Services
	Queries    telemetryquery.Service
	QueryGuard sqlite.QueryGuard
	BlobStore  store.BlobStore
	WithAuth   policyAuthFunc
}

func RegisterAnalyticsRoutes(mux *http.ServeMux, routes AnalyticsRoutes) {
	mux.Handle("GET /api/0/organizations/{org_slug}/discover/", handleDiscover(routes.Catalog, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/logs/", handleListOrganizationLogs(routes.Catalog, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))

	mux.Handle("GET /api/0/organizations/{org_slug}/dashboards/", handleListDashboards(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/dashboards/", handleCreateDashboard(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleGetDashboard(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleUpdateDashboard(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/", handleDeleteDashboard(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/", handleCreateDashboardWidget(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/", handleUpdateDashboardWidget(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/", handleDeleteDashboardWidget(routes.Analytics.Dashboards, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))

	mux.Handle("GET /api/0/organizations/{org_slug}/events-timeseries/", handleListEventTimeSeries(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/stats-summary/", handleGetStatsSummary(routes.DB, routes.Catalog, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/events/", handleListOrgEvents(routes.DB, routes.Queries, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))

	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/", handleListProjectEvents(routes.DB, routes.Queries, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/", handleListReplays(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/", handleGetReplay(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/manifest/", handleGetReplayManifest(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/timeline/", handleListReplayTimeline(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/panes/{pane}/", handleListReplayPane(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if routes.BlobStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/assets/{attachment_id}/", handleDownloadReplayAsset(routes.DB, routes.Queries, routes.BlobStore, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/", handleListProfiles(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/{profile_id}/", handleGetProfile(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/top-down/", handleProfileTopDown(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/bottom-up/", handleProfileBottomUp(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/flamegraph/", handleProfileFlamegraph(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/hot-path/", handleProfileHotPath(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/profiles/compare/", handleCompareProfiles(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/transactions/", handleListTransactions(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/traces/{trace_id}/", handleGetTrace(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))

	mux.Handle("GET /api/0/organizations/{org_slug}/discover/saved/", handleListDiscoverSavedQueries(routes.Analytics.Searches, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/discover/saved/", handleCreateDiscoverSavedQuery(routes.Analytics.Searches, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/discover/saved/{query_id}/", handleGetDiscoverSavedQuery(routes.Analytics.Searches, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/discover/saved/{query_id}/", handleUpdateDiscoverSavedQuery(routes.Analytics.Searches, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/discover/saved/{query_id}/", handleDeleteDiscoverSavedQuery(routes.Analytics.Searches, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryWrite, Resource: auth.ResourceOrganizationPath})))

	mux.Handle("GET /api/0/organizations/{org_slug}/monitors/", handleListOrgMonitors(routes.DB, routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/monitors/", handleCreateOrgMonitor(routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/monitors/{monitor_slug}/", handleGetOrgMonitor(routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/monitors/{monitor_slug}/", handleUpdateOrgMonitor(routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/monitors/{monitor_slug}/", handleDeleteOrgMonitor(routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/monitors/{monitor_slug}/checkins/", handleListOrgMonitorCheckIns(routes.Catalog, routes.Monitors, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/replays/", handleListOrgReplays(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/replays/{replay_id}/", handleGetOrgReplay(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/replays/{replay_id}/", handleDeleteOrgReplay(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/replay-count/", handleGetReplayCount(routes.DB, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/replay-selectors/", handleGetReplaySelectors(routes.DB, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))

	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/", handleDeleteReplay(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/jobs/delete/", handleReplayDeletionJobs(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/replays/jobs/delete/", handleReplayDeletionJobs(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/jobs/delete/{job_id}/", handleGetReplayDeletionJob(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/clicks/", handleListReplayClicks(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/recording-segments/", handleListReplayRecordingSegments(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/recording-segments/{segment_id}/", handleGetReplayRecordingSegment(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/viewed-by/", handleListReplayViewedBy(routes.DB, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
}
