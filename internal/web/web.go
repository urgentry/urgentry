// Package web serves the Urgentry HTML UI using Go templates + HTMX.
// All templates and static assets are embedded in the binary.
package web

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

// rawHTML is an alias for template.HTML to pass pre-escaped HTML into templates.
type rawHTML = template.HTML

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Handler holds dependencies for the web UI.
type Handler struct {
	webStore        store.WebStore // required for all mounted web routes
	queries         telemetryquery.Service
	replays         store.ReplayReadStore
	db              *sql.DB // retained for write-side stores and search persistence
	blobStore       store.BlobStore
	catalog         controlplane.CatalogStore
	issues          controlplane.IssueWorkflowStore
	ownership       controlplane.OwnershipStore
	releases        controlplane.ReleaseStore
	alerts          controlplane.AlertStore
	outbox          controlplane.NotificationOutboxStore
	deliveries      controlplane.NotificationDeliveryStore
	monitors        controlplane.MonitorStore
	nativeControl   *sqlite.NativeControlStore
	dashboards      analyticsservice.DashboardStore
	snapshots       analyticsservice.SnapshotStore
	reportSchedules analyticsservice.ReportScheduleStore
	operators       store.OperatorStore
	operatorAudits  store.OperatorAuditStore
	queryGuard      sqlite.QueryGuard
	sourceResolver  *sourcemap.Resolver
	codeMappings    store.CodeMappingStore
	quotaStore      *sqlite.QuotaStore
	pages           map[string]*template.Template
	login           *template.Template
	dataDir         string // data directory path for DB file stats
	searches        analyticsservice.SearchStore
	startedAt       time.Time // server start time for time-to-first-event
	authz           *auth.Authorizer
	tokenManager    auth.TokenManager // optional: nil disables PAT management UI
}

type Dependencies struct {
	WebStore       store.WebStore
	Replays        store.ReplayReadStore
	Queries        telemetryquery.Service
	DB             *sql.DB
	BlobStore      store.BlobStore
	DataDir        string
	Auth           *auth.Authorizer
	Control        controlplane.Services
	Operators      store.OperatorStore
	OperatorAudits store.OperatorAuditStore
	QueryGuard     sqlite.QueryGuard
	NativeControl  *sqlite.NativeControlStore
	Analytics      analyticsservice.Services
	SourceMaps     sourcemap.Store // optional: nil disables source map resolution
	CodeMappings   store.CodeMappingStore // optional: nil disables code mapping links
	QuotaStore     *sqlite.QuotaStore // optional: nil disables quota page
	TokenManager   auth.TokenManager // optional: nil disables PAT management UI
}

// ValidateDependencies checks the runtime dependencies needed to mount the web
// UI. Request-layer constructors still panic on invalid deps, but callers that
// want startup-time validation can use this helper first.
func ValidateDependencies(deps Dependencies) error {
	if deps.WebStore == nil || deps.Replays == nil || deps.Queries == nil || deps.DB == nil || deps.QueryGuard == nil {
		return errors.New("requires prebuilt web and query services")
	}
	if deps.Control.Catalog == nil || deps.Control.Issues == nil || deps.Control.Releases == nil || deps.Control.Alerts == nil || deps.Control.Monitors == nil {
		return errors.New("requires fully constructed control-plane services")
	}
	if err := analyticsservice.Validate(deps.Analytics); err != nil {
		return err
	}
	if deps.NativeControl == nil {
		return errors.New("requires prebuilt analytics and native stores")
	}
	return nil
}

func NewHandlerWithDeps(deps Dependencies) *Handler {
	if err := ValidateDependencies(deps); err != nil {
		panic("web.NewHandler " + err.Error())
	}
	funcMap := template.FuncMap{
		"inc":       func(i int) int { return i + 1 },
		"sub":       func(a, b int) int { return a - b },
		"hasPrefix": strings.HasPrefix,
	}

	// Parse base template first, then clone it for each page template.
	// Each page defines its own "content" block, so they must be separate
	// template sets to avoid collisions.
	base := template.Must(
		template.New("base.html").Funcs(funcMap).ParseFS(templateFS, "templates/base.html"),
	)

	pageFiles := []string{
		"dashboard.html",
		"dashboards.html",
		"dashboard-detail.html",
		"dashboard-widget-detail.html",
		"discover.html",
		"discover-query-detail.html",
		"logs.html",
		"replays.html",
		"replay-detail.html",
		"profiles.html",
		"profile-detail.html",
		"trace-detail.html",
		"release-detail.html",
		"issue-list.html",
		"issue-detail.html",
		"issue-events-tab.html",
		"issue-activity-tab.html",
		"issue-similar-tab.html",
		"issue-merged-tab.html",
		"issue-tags-tab.html",
		"issue-replays-tab.html",
		"event-detail.html",
		"alerts.html",
		"alert-detail.html",
		"monitors.html",
		"monitor-detail.html",
		"feedback.html",
		"feedback-detail.html",
		"releases.html",
		"ops.html",
		"manage-dashboard.html",
		"manage-organizations.html",
		"manage-projects.html",
		"manage-users.html",
		"manage-settings.html",
		"manage-status.html",
		"settings.html",
		"settings-account.html",
		"settings-account-security.html",
		"settings-account-notifications.html",
		"settings-account-api.html",
		"settings-account-close.html",
		"analytics-snapshot.html",
		"performance.html",
		"performance-queues.html",
		"performance-spans.html",
		"quota.html",
		"metrics.html",
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, name := range pageFiles {
		clone := template.Must(base.Clone())
		template.Must(clone.ParseFS(templateFS, "templates/"+name))
		pages[name] = clone
	}
	login := template.Must(template.New("login.html").Funcs(funcMap).ParseFS(templateFS, "templates/login.html"))
	control := deps.Control
	analytics := deps.Analytics

	var srcResolver *sourcemap.Resolver
	if deps.SourceMaps != nil {
		srcResolver = &sourcemap.Resolver{Store: deps.SourceMaps}
	}

	h := &Handler{
		webStore:        deps.WebStore,
		queries:         deps.Queries,
		replays:         deps.Replays,
		db:              deps.DB,
		blobStore:       deps.BlobStore,
		catalog:         control.Catalog,
		issues:          control.Issues,
		ownership:       control.Ownership,
		releases:        control.Releases,
		alerts:          control.Alerts,
		outbox:          control.Outbox,
		deliveries:      control.Deliveries,
		monitors:        control.Monitors,
		nativeControl:   deps.NativeControl,
		dashboards:      analytics.Dashboards,
		snapshots:       analytics.Snapshots,
		reportSchedules: analytics.ReportSchedules,
		operators:       deps.Operators,
		operatorAudits:  deps.OperatorAudits,
		queryGuard:      deps.QueryGuard,
		sourceResolver:  srcResolver,
		codeMappings:    deps.CodeMappings,
		quotaStore:      deps.QuotaStore,
		pages:           pages,
		login:           login,
		dataDir:         deps.DataDir,
		searches:        analytics.Searches,
		startedAt:       time.Now(),
		authz:           deps.Auth,
		tokenManager:    deps.TokenManager,
	}
	return h
}

// RegisterRoutes mounts all web routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /login/{$}", h.loginPage)
	mux.HandleFunc("POST /login/{$}", h.loginAction)
	wrap := func(handler http.Handler) http.Handler {
		handler = withPageRequestState(handler)
		if h.authz == nil {
			return handler
		}
		return h.authz.Web(handler)
	}

	mux.Handle("POST /logout", wrap(http.HandlerFunc(h.logout)))
	mux.Handle("GET /{$}", wrap(http.HandlerFunc(h.dashboardPage)))
	mux.Handle("GET /dashboards/{$}", wrap(http.HandlerFunc(h.dashboardsPage)))
	mux.Handle("GET /dashboards/{id}/{$}", wrap(http.HandlerFunc(h.dashboardDetailPage)))
	mux.Handle("POST /dashboards/", wrap(http.HandlerFunc(h.createDashboard)))
	mux.Handle("POST /dashboards/starter/{slug}/create", wrap(http.HandlerFunc(h.createDashboardTemplate)))
	mux.Handle("POST /dashboards/{id}/update", wrap(http.HandlerFunc(h.updateDashboard)))
	mux.Handle("POST /dashboards/{id}/duplicate", wrap(http.HandlerFunc(h.duplicateDashboard)))
	mux.Handle("POST /dashboards/{id}/delete", wrap(http.HandlerFunc(h.deleteDashboard)))
	mux.Handle("POST /dashboards/{id}/widgets", wrap(http.HandlerFunc(h.createDashboardWidget)))
	mux.Handle("GET /dashboards/{id}/widgets/{widget_id}/{$}", wrap(http.HandlerFunc(h.dashboardWidgetDetailPage)))
	mux.Handle("GET /dashboards/{id}/widgets/{widget_id}/export", wrap(http.HandlerFunc(h.exportDashboardWidget)))
	mux.Handle("POST /dashboards/{id}/widgets/{widget_id}/snapshot", wrap(http.HandlerFunc(h.createDashboardWidgetSnapshot)))
	mux.Handle("POST /dashboards/{id}/widgets/{widget_id}/reports", wrap(http.HandlerFunc(h.createDashboardWidgetReport)))
	mux.Handle("POST /dashboards/{id}/widgets/{widget_id}/reports/{report_id}/delete", wrap(http.HandlerFunc(h.deleteDashboardWidgetReport)))
	mux.Handle("POST /dashboards/{id}/widgets/{widget_id}/delete", wrap(http.HandlerFunc(h.deleteDashboardWidget)))
	mux.Handle("GET /discover/{$}", wrap(http.HandlerFunc(h.discoverPage)))
	mux.Handle("GET /discover/starters/{slug}/{$}", wrap(http.HandlerFunc(h.discoverStarterPage)))
	mux.Handle("POST /discover/save-query", wrap(http.HandlerFunc(h.saveDiscoverQuery)))
	mux.Handle("GET /discover/queries/{id}/{$}", wrap(http.HandlerFunc(h.discoverQueryDetailPage)))
	mux.Handle("POST /discover/queries/{id}/favorite", wrap(http.HandlerFunc(h.updateDiscoverQueryFavorite)))
	mux.Handle("POST /discover/queries/{id}/clone", wrap(http.HandlerFunc(h.cloneDiscoverQuery)))
	mux.Handle("POST /discover/queries/{id}/update", wrap(http.HandlerFunc(h.updateDiscoverQuery)))
	mux.Handle("POST /discover/queries/{id}/delete", wrap(http.HandlerFunc(h.deleteDiscoverQuery)))
	mux.Handle("POST /discover/queries/{id}/snapshot", wrap(http.HandlerFunc(h.createDiscoverQuerySnapshot)))
	mux.Handle("POST /discover/queries/{id}/reports", wrap(http.HandlerFunc(h.createDiscoverQueryReport)))
	mux.Handle("POST /discover/queries/{id}/reports/{report_id}/delete", wrap(http.HandlerFunc(h.deleteDiscoverQueryReport)))
	mux.Handle("GET /metrics/{$}", wrap(http.HandlerFunc(h.metricsPage)))
	mux.Handle("GET /performance/{$}", wrap(http.HandlerFunc(h.performancePage)))
	mux.Handle("GET /performance/queues/{$}", wrap(http.HandlerFunc(h.performanceQueuesPage)))
	mux.Handle("GET /performance/spans/{$}", wrap(http.HandlerFunc(h.performanceSpansPage)))
	mux.Handle("GET /logs/starters/{slug}/{$}", wrap(http.HandlerFunc(h.discoverStarterPage)))
	mux.Handle("GET /logs/{$}", wrap(http.HandlerFunc(h.logsPage)))
	mux.HandleFunc("GET /analytics/snapshots/{token}/{$}", h.analyticsSnapshotPage)
	mux.Handle("GET /replays/{$}", wrap(http.HandlerFunc(h.replaysPage)))
	mux.Handle("GET /replays/{id}/{$}", wrap(http.HandlerFunc(h.replayDetailPage)))
	mux.Handle("GET /profiles/{$}", wrap(http.HandlerFunc(h.profilesPage)))
	mux.Handle("GET /profiles/{id}/{$}", wrap(http.HandlerFunc(h.profileDetailPage)))
	mux.Handle("GET /traces/{trace_id}/{$}", wrap(http.HandlerFunc(h.traceDetailPage)))
	mux.Handle("GET /issues/{$}", wrap(http.HandlerFunc(h.issueListPage)))
	mux.Handle("GET /issues/errors/{$}", wrap(http.HandlerFunc(h.issueListErrorsPage)))
	mux.Handle("GET /issues/warnings/{$}", wrap(http.HandlerFunc(h.issueListWarningsPage)))
	mux.Handle("GET /issues/{id}/{$}", wrap(http.HandlerFunc(h.issueDetailPage)))
	mux.Handle("GET /issues/{id}/events/{$}", wrap(http.HandlerFunc(h.issueEventsTab)))
	mux.Handle("GET /issues/{id}/activity/{$}", wrap(http.HandlerFunc(h.issueActivityTab)))
	mux.Handle("GET /issues/{id}/similar/{$}", wrap(http.HandlerFunc(h.issueSimilarTab)))
	mux.Handle("GET /issues/{id}/merged/{$}", wrap(http.HandlerFunc(h.issueMergedTab)))
	mux.Handle("GET /issues/{id}/tags/{$}", wrap(http.HandlerFunc(h.issueTagsTab)))
	mux.Handle("GET /issues/{id}/replays/{$}", wrap(http.HandlerFunc(h.issueReplaysTab)))
	mux.Handle("POST /issues/{id}/status", wrap(http.HandlerFunc(h.updateIssueStatus)))
	mux.Handle("POST /issues/{id}/assign", wrap(http.HandlerFunc(h.updateIssueAssignee)))
	mux.Handle("POST /issues/{id}/priority", wrap(http.HandlerFunc(h.updateIssuePriority)))
	mux.Handle("POST /issues/{id}/bookmark", wrap(http.HandlerFunc(h.toggleIssueBookmark)))
	mux.Handle("POST /issues/{id}/subscribe", wrap(http.HandlerFunc(h.toggleIssueSubscription)))
	mux.Handle("POST /issues/{id}/comments", wrap(http.HandlerFunc(h.addIssueComment)))
	mux.Handle("POST /issues/{id}/merge", wrap(http.HandlerFunc(h.mergeIssue)))
	mux.Handle("POST /issues/{id}/unmerge", wrap(http.HandlerFunc(h.unmergeIssue)))
	mux.Handle("GET /events/{id}/{$}", wrap(http.HandlerFunc(h.eventDetailPage)))
	mux.Handle("GET /alerts/{$}", wrap(http.HandlerFunc(h.alertsPage)))
	mux.Handle("GET /alerts/{id}/{$}", wrap(http.HandlerFunc(h.alertDetailPage)))
	mux.Handle("POST /alerts/", wrap(http.HandlerFunc(h.createAlertRule)))
	mux.Handle("POST /alerts/{id}/update", wrap(http.HandlerFunc(h.updateAlertRule)))
	mux.Handle("POST /alerts/{id}/delete", wrap(http.HandlerFunc(h.deleteAlertRule)))
	mux.Handle("GET /monitors/{$}", wrap(http.HandlerFunc(h.monitorsPage)))
	mux.Handle("GET /monitors/{project_id}/{slug}/{$}", wrap(http.HandlerFunc(h.monitorDetailPage)))
	mux.Handle("POST /monitors/", wrap(http.HandlerFunc(h.createMonitor)))
	mux.Handle("POST /monitors/{slug}/update", wrap(http.HandlerFunc(h.updateMonitor)))
	mux.Handle("POST /monitors/{slug}/delete", wrap(http.HandlerFunc(h.deleteMonitor)))
	mux.Handle("GET /feedback/{$}", wrap(http.HandlerFunc(h.feedbackPage)))
	mux.Handle("GET /feedback/{id}/{$}", wrap(http.HandlerFunc(h.feedbackDetailPage)))
	mux.Handle("GET /releases/{$}", wrap(http.HandlerFunc(h.releasesPage)))
	mux.Handle("GET /releases/{version}/{$}", wrap(http.HandlerFunc(h.releaseDetailPage)))
	mux.Handle("POST /releases/{version}/deploys", wrap(http.HandlerFunc(h.createReleaseDeploy)))
	mux.Handle("POST /releases/{version}/commits", wrap(http.HandlerFunc(h.createReleaseCommit)))
	mux.Handle("POST /releases/{version}/native/reprocess", wrap(http.HandlerFunc(h.createReleaseNativeReprocess)))
	mux.Handle("POST /releases/{version}/debug-files/{debug_file_id}/reprocess", wrap(http.HandlerFunc(h.createDebugFileNativeReprocess)))
	mux.Handle("GET /ops/{$}", wrap(http.HandlerFunc(h.opsPage)))
	mux.Handle("GET /manage/{$}", wrap(http.HandlerFunc(h.manageDashboardPage)))
	mux.Handle("GET /manage/organizations/{$}", wrap(http.HandlerFunc(h.manageOrganizationsPage)))
	mux.Handle("GET /manage/projects/{$}", wrap(http.HandlerFunc(h.manageProjectsPage)))
	mux.Handle("GET /manage/users/{$}", wrap(http.HandlerFunc(h.manageUsersPage)))
	mux.Handle("GET /manage/settings/{$}", wrap(http.HandlerFunc(h.manageSettingsPage)))
	mux.Handle("GET /manage/status/{$}", wrap(http.HandlerFunc(h.manageStatusPage)))
	mux.Handle("GET /settings/{$}", wrap(http.HandlerFunc(h.settingsPage)))
	mux.Handle("GET /settings/account/{$}", wrap(http.HandlerFunc(h.accountDetailsPage)))
	mux.Handle("GET /settings/account/security/{$}", wrap(http.HandlerFunc(h.accountSecurityPage)))
	mux.Handle("POST /settings/account/security/revoke-session", wrap(http.HandlerFunc(h.revokeAccountSession)))
	mux.Handle("GET /settings/account/notifications/{$}", wrap(http.HandlerFunc(h.accountNotificationsPage)))
	mux.Handle("GET /settings/account/api/{$}", wrap(http.HandlerFunc(h.accountAPIPage)))
	mux.Handle("POST /settings/account/api/create", wrap(http.HandlerFunc(h.createAccountAPIToken)))
	mux.Handle("POST /settings/account/api/{token_id}/revoke", wrap(http.HandlerFunc(h.revokeAccountAPIToken)))
	mux.Handle("GET /settings/account/close/{$}", wrap(http.HandlerFunc(h.accountClosePage)))
	mux.Handle("POST /settings/environment", wrap(http.HandlerFunc(h.setEnvironment)))
	mux.Handle("POST /settings/time-range", wrap(http.HandlerFunc(h.setTimeRangeAction)))
	mux.Handle("POST /settings/project", wrap(http.HandlerFunc(h.updateProjectSettings)))
	mux.Handle("POST /settings/ownership", wrap(http.HandlerFunc(h.createOwnershipRule)))
	mux.Handle("POST /settings/ownership/{id}/delete", wrap(http.HandlerFunc(h.deleteOwnershipRule)))
	mux.Handle("POST /settings/code-mappings", wrap(http.HandlerFunc(h.createCodeMapping)))
	mux.Handle("POST /settings/code-mappings/{id}/delete", wrap(http.HandlerFunc(h.deleteCodeMapping)))
	if h.quotaStore != nil {
		mux.Handle("GET /settings/quota/{$}", wrap(http.HandlerFunc(h.quotaPage)))
		mux.Handle("POST /settings/quota/rate-limit", wrap(http.HandlerFunc(h.upsertQuotaRateLimit)))
		mux.Handle("POST /settings/quota/rate-limit/{project_id}/delete", wrap(http.HandlerFunc(h.deleteQuotaRateLimit)))
	}
	mux.Handle("POST /api/ui/searches", wrap(http.HandlerFunc(h.saveSearch)))
	mux.Handle("GET /api/ui/searches/{id}", wrap(http.HandlerFunc(h.getSearch)))
	mux.Handle("PUT /api/ui/searches/{id}", wrap(http.HandlerFunc(h.updateSearch)))
	mux.Handle("POST /api/ui/searches/clone/{id}", wrap(http.HandlerFunc(h.cloneSearch)))
	mux.Handle("DELETE /api/ui/searches/{id}", wrap(http.HandlerFunc(h.deleteSearch)))
	mux.Handle("GET /api/ui/environments", wrap(http.HandlerFunc(h.listEnvironments)))
	mux.Handle("POST /api/0/projects/{org}/{proj}/star/", wrap(http.HandlerFunc(handleToggleStarProject(h))))
	mux.Handle("GET /api/search", wrap(api.HandleSearch(h.webStore)))
}

// render executes a named page template and writes the result.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	h.renderStatus(w, http.StatusOK, name, data)
}

func (h *Handler) renderStatus(w http.ResponseWriter, status int, name string, data any) {
	tmpl, ok := h.pages[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	// Buffer the template output so we can set headers before writing,
	// and handle errors cleanly without a superfluous WriteHeader.
	var buf strings.Builder
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

// Package-level aliases for shared helpers.
func parseDBTime(s string) time.Time { return sqlutil.ParseDBTime(s) }

// listRecentEventsDB returns the most recent events across all projects.
func (h *Handler) listRecentEventsDB(ctx context.Context, limit int) ([]store.WebEvent, error) {
	return h.webStore.ListRecentEvents(ctx, limit)
}

// countDistinctUsersForGroupDB returns distinct users for a specific group.
func (h *Handler) countDistinctUsersForGroupDB(ctx context.Context, groupID string) (int, error) {
	return h.webStore.CountDistinctUsersForGroup(ctx, groupID)
}

// loadEnvironments returns the list of distinct environments from the store.
func (h *Handler) loadEnvironments(ctx context.Context) []string {
	if h.webStore == nil {
		return nil
	}
	envs, err := h.webStore.ListEnvironments(ctx)
	if err != nil {
		return nil
	}
	return envs
}

func (h *Handler) databaseFileSize() int64 {
	if h.dataDir != "" {
		dbPath := h.dataDir + "/urgentry.db"
		if fi, statErr := os.Stat(dbPath); statErr == nil {
			return fi.Size()
		}
	}
	return 0
}
