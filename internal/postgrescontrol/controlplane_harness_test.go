package postgrescontrol

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"urgentry/internal/alert"
	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/integration"
	"urgentry/internal/issue"
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/internal/web"
)

const (
	harnessOrgID       = "test-org-id"
	harnessOrgSlug     = "test-org"
	harnessTeamID      = "test-team-id"
	harnessTeamSlug    = "backend"
	harnessProjectID   = "test-proj-id"
	harnessProjectSlug = "test-project"
	harnessUserID      = "user-1"
	harnessEmail       = "owner@example.com"
	harnessPassword    = "password123!"
	harnessPAT         = "gpat_control_harness_admin"
)

var harnessScopes = []string{
	auth.ScopeOrgRead,
	auth.ScopeOrgAdmin,
	auth.ScopeProjectRead,
	auth.ScopeProjectWrite,
	auth.ScopeProjectTokensRead,
	auth.ScopeProjectTokensWrite,
	auth.ScopeProjectArtifactsWrite,
	auth.ScopeIssueWrite,
	auth.ScopeReleaseRead,
	auth.ScopeReleaseWrite,
}

type controlPlaneHarness struct {
	name         string
	queryDB      *sql.DB
	controlDB    *sql.DB
	control      controlplane.Services
	authz        *auth.Authorizer
	pat          string
	sessionToken string
	csrfToken    string
	apiServer    *httptest.Server
	webServer    *httptest.Server
}

type apiSnapshot struct {
	ProjectName          string
	ReplaySampleRate     float64
	ReplayMaxBytes       int64
	MemberCount          int
	PATCount             int
	AutomationTokenCount int
	ProjectIssueCount    int
	OrgIssueCount        int
	DiscoverIssueCount   int
	IssueTitle           string
	IssueStatus          string
	IssueSubstatus       string
	IssueRelease         string
	IssueCommentCount    int
	IssueActivityCount   int
	ReleaseVersion       string
	ReleaseDeployCount   int
	ReleaseCommitCount   int
	AlertRuleCount       int
	AlertDeliveryCount   int
	MonitorSlug          string
	MonitorStatus        string
	MonitorCheckInCount  int
}

type webSnapshot struct {
	SettingsUpdated bool
	AlertVisible    bool
	MonitorVisible  bool
	ReleaseVisible  bool
	DiscoverVisible bool
}

type preventSnapshot struct {
	RepositoryCount       int
	RepositoryName        string
	RepositoryDefault     string
	RepositoryUpdatedAt   string
	RepositoryLatestAt    string
	DetailHasUploadToken  bool
	DetailAnalytics       bool
	TokenCount            int
	TokenName             string
	TokenValue            string
	SyncBefore            bool
	SyncAfter             bool
	BranchCount           int
	BranchDefault         string
	BranchNames           []string
	Suites                []string
	TestResultCount       int
	TestResultName        string
	TestResultFailureRate float64
	TestResultFailCount   int
	FlakyResultCount      int
	FlakyResultName       string
	FlakyResultRate       float64
	FlakyResultFailCount  int
	AggregateTotalFails   int
	AggregateTotalSkips   int
	AggregateSlowTests    int
	AggregateFlakeCount   int
	AggregateFlakeRate    float64
	RegeneratedTokenValid bool
}

type integrationParitySnapshot struct {
	SentryAppName              string
	SentryAppWebhookURL        string
	SentryAppAllowedOrigins    []string
	InstallationCount          int
	InstallationAppName        string
	ExternalIssueDisplayName   string
	ExternalIssueServiceType   string
	ExternalIssueWebURL        string
	ExternalIssueCount         int
	ExternalIssueCountAfterDel int
	AppDeleteStatus            int
	AppGetAfterDeleteStatus    int
}

type preprodArtifactSnapshot struct {
	InstallBuildID      string
	InstallURL          string
	SizeState           string
	SizeBaseBuildID     string
	SizeComparisonCount int
	LatestBuildID       string
	CurrentBuildID      string
	InvalidQueryStatus  int
}

type autofixSnapshot struct {
	InitialNil    bool
	RunID         int64
	Status        string
	StepCount     int
	PullRequest   string
	InvalidStatus int
}

func TestControlPlaneAPIHarness(t *testing.T) {
	t.Parallel()

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)

	sqliteSnapshot := runControlPlaneAPISuite(t, sqliteHarness)
	postgresSnapshot := runControlPlaneAPISuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane API drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func TestControlPlaneWebHarness(t *testing.T) {
	t.Parallel()

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)

	sqliteSnapshot := runControlPlaneWebSuite(t, sqliteHarness)
	postgresSnapshot := runControlPlaneWebSuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane web drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func TestControlPlaneAutofixHarness(t *testing.T) {
	t.Parallel()

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)

	sqliteSnapshot := runControlPlaneAutofixSuite(t, sqliteHarness)
	postgresSnapshot := runControlPlaneAutofixSuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane autofix drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func TestControlPlanePreventAPIHarness(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-12 * time.Hour).Truncate(time.Second)

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)
	seedHarnessPreventControlPlane(t, sqliteHarness, base)
	seedHarnessPreventControlPlane(t, postgresHarness, base)

	sqliteSnapshot := runControlPlanePreventSuite(t, sqliteHarness)
	postgresSnapshot := runControlPlanePreventSuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane Prevent API drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func TestControlPlaneIntegrationParityHarness(t *testing.T) {
	t.Parallel()

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)

	sqliteSnapshot := runControlPlaneIntegrationSuite(t, sqliteHarness)
	postgresSnapshot := runControlPlaneIntegrationSuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane integration parity drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func TestControlPlanePreprodArtifactHarness(t *testing.T) {
	t.Parallel()

	sqliteHarness := newSQLiteControlPlaneHarness(t)
	postgresHarness := newPostgresControlPlaneHarness(t)

	sqliteSnapshot := runControlPlanePreprodArtifactSuite(t, sqliteHarness)
	postgresSnapshot := runControlPlanePreprodArtifactSuite(t, postgresHarness)

	if !reflect.DeepEqual(sqliteSnapshot, postgresSnapshot) {
		t.Fatalf("control-plane preprod artifact drift\nsqlite:   %+v\npostgres: %+v", sqliteSnapshot, postgresSnapshot)
	}
}

func newSQLiteControlPlaneHarness(t *testing.T) *controlPlaneHarness {
	t.Helper()

	db := openHarnessSQLite(t)
	seedHarnessQueryPlane(t, db)

	authStore := sqlite.NewAuthStore(db)
	if _, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: harnessOrgID,
		Email:                 harnessEmail,
		DisplayName:           "Owner",
		Password:              harnessPassword,
	}); err != nil {
		t.Fatalf("sqlite bootstrap auth: %v", err)
	}
	user, err := authStore.AuthenticateUserPassword(context.Background(), harnessEmail, harnessPassword)
	if err != nil {
		t.Fatalf("sqlite authenticate bootstrap user: %v", err)
	}
	if _, err := authStore.CreatePersonalAccessToken(context.Background(), user.ID, "Harness Admin", harnessScopes, nil, harnessPAT); err != nil {
		t.Fatalf("sqlite create harness PAT: %v", err)
	}
	authz := auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 24*time.Hour)
	sessionToken, principal, err := authz.Login(context.Background(), harnessEmail, harnessPassword, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("sqlite login: %v", err)
	}
	control := controlplane.SQLiteServices(db)
	return startControlPlaneHarness(t, "sqlite", db, db, control, sqlite.NewPreventStore(db), sqlite.NewIntegrationConfigStore(db), sqlite.NewSentryAppStore(db), sqlite.NewExternalIssueStore(db), authStore, authz, sessionToken, principal.CSRFToken)
}

func newPostgresControlPlaneHarness(t *testing.T) *controlPlaneHarness {
	t.Helper()

	queryDB := openHarnessSQLite(t)
	seedHarnessQueryPlane(t, queryDB)
	seedHarnessQueryMembership(t, queryDB)

	controlDB := openMigratedTestDatabase(t)
	seedHarnessPostgresControlPlane(t, controlDB)

	authStore := NewAuthStore(controlDB)
	if _, err := authStore.CreatePersonalAccessToken(context.Background(), harnessUserID, "Harness Admin", harnessScopes, nil, harnessPAT); err != nil {
		t.Fatalf("postgres create PAT: %v", err)
	}
	authz := auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 24*time.Hour)
	sessionToken, principal, err := authz.Login(context.Background(), harnessEmail, harnessPassword, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("postgres login: %v", err)
	}
	control := controlplane.Services{
		Catalog:    NewCatalogStore(controlDB),
		Admin:      NewAdminStore(controlDB),
		Issues:     NewGroupStore(controlDB),
		IssueReads: NewIssueReadStore(controlDB, queryDB),
		Ownership:  NewOwnershipStore(controlDB),
		Releases:   NewReleaseStore(controlDB),
		Alerts:     NewAlertStore(controlDB),
		Outbox:     NewNotificationOutboxStore(controlDB),
		Deliveries: NewNotificationDeliveryStore(controlDB),
		Monitors:   NewMonitorStore(controlDB),
	}
	return startControlPlaneHarness(t, "postgres", queryDB, controlDB, control, NewPreventStore(controlDB), NewIntegrationConfigStore(controlDB), NewSentryAppStore(controlDB), NewExternalIssueStore(controlDB), authStore, authz, sessionToken, principal.CSRFToken)
}

func startControlPlaneHarness(t *testing.T, name string, queryDB, controlDB *sql.DB, control controlplane.Services, prevent sharedstore.PreventStore, integrationStore integration.Store, sentryApps integration.AppStore, externalIssues integration.ExternalIssueStore, tokenManager auth.TokenManager, authz *auth.Authorizer, sessionToken, csrf string) *controlPlaneHarness {
	t.Helper()

	blobStore := sharedstore.NewMemoryBlobStore()
	webStore := sqlite.NewWebStore(queryDB)
	replayStore := sqlite.NewReplayStore(queryDB, blobStore)
	queryService := telemetryquery.NewService(queryDB, nil, telemetryquery.Dependencies{
		Blobs:       blobStore,
		IssueSearch: control.IssueReads,
		Web:         webStore,
		Discover:    sqlite.NewDiscoverEngine(queryDB),
		Traces:      sqlite.NewTraceStore(queryDB),
		Replays:     replayStore,
		Profiles:    sqlite.NewProfileStore(queryDB, blobStore),
	})
	h := &controlPlaneHarness{
		name:         name,
		queryDB:      queryDB,
		controlDB:    controlDB,
		control:      control,
		authz:        authz,
		pat:          harnessPAT,
		sessionToken: sessionToken,
		csrfToken:    csrf,
	}
	operatorAudits := sqlite.NewOperatorAuditStore(queryDB)
	queryGuard := sqlite.NewQueryGuardStore(queryDB)
	dashboards := sqlite.NewDashboardStore(queryDB)
	backfills := sqlite.NewBackfillStore(queryDB)
	audits := sqlite.NewAuditStore(queryDB)
	releaseHealth := sqlite.NewReleaseHealthStore(queryDB)
	debugFiles := sqlite.NewDebugFileStore(queryDB, blobStore)
	preprodArtifacts := sqlite.NewPreprodArtifactStore(queryDB)
	autofix := sqlite.NewAutofixStore(queryDB)
	outcomes := sqlite.NewOutcomeStore(queryDB)
	retention := sqlite.NewRetentionStore(queryDB, blobStore)
	nativeControl := sqlite.NewNativeControlStore(queryDB, blobStore, operatorAudits)
	importExport := sqlite.NewImportExportStore(queryDB, sqlite.NewAttachmentStore(queryDB, blobStore), nil, nil, blobStore)
	operatorStore := sqlite.NewOperatorStore(queryDB, sharedstore.OperatorRuntime{Role: "test", Env: "test"}, nil, operatorAudits, func(context.Context) (int, error) {
		return 0, nil
	})
	analytics := analyticsservice.Services{
		Dashboards:      dashboards,
		Snapshots:       sqlite.NewAnalyticsSnapshotStore(queryDB),
		ReportSchedules: sqlite.NewAnalyticsReportScheduleStore(queryDB),
		Searches:        sqlite.NewSearchStore(queryDB),
	}

	h.apiServer = httptest.NewServer(api.NewRouter(api.Dependencies{
		DB:                  queryDB,
		Auth:                authz,
		Control:             control,
		TokenManager:        tokenManager,
		PrincipalShadows:    sqlite.NewPrincipalShadowStore(queryDB),
		QueryGuard:          queryGuard,
		Operators:           operatorStore,
		OperatorAudits:      operatorAudits,
		Analytics:           analytics,
		Backfills:           backfills,
		Audits:              audits,
		NativeControl:       nativeControl,
		ReleaseHealth:       releaseHealth,
		DebugFiles:          debugFiles,
		PreprodArtifacts:    preprodArtifacts,
		Autofix:             autofix,
		Outcomes:            outcomes,
		Retention:           retention,
		ImportExport:        importExport,
		BlobStore:           blobStore,
		Queries:             queryService,
		IntegrationRegistry: integration.NewDefaultRegistry(),
		IntegrationStore:    integrationStore,
		SentryAppStore:      sentryApps,
		ExternalIssues:      externalIssues,
		Prevent:             prevent,
	}))
	t.Cleanup(h.apiServer.Close)

	webHandler := web.NewHandlerWithDeps(web.Dependencies{
		WebStore:       webStore,
		Replays:        replayStore,
		Queries:        queryService,
		DB:             queryDB,
		BlobStore:      blobStore,
		DataDir:        t.TempDir(),
		Auth:           authz,
		Control:        control,
		Operators:      operatorStore,
		OperatorAudits: operatorAudits,
		QueryGuard:     queryGuard,
		NativeControl:  nativeControl,
		Analytics:      analytics,
	})
	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)
	h.webServer = httptest.NewServer(mux)
	t.Cleanup(h.webServer.Close)

	return h
}

func runControlPlaneAPISuite(t *testing.T, h *controlPlaneHarness) apiSnapshot {
	t.Helper()
	client := &http.Client{}

	settingsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/settings/", h.pat, nil)
	if settingsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s get settings status=%d", h.name, settingsResp.StatusCode)
	}
	var settings sharedstore.ProjectSettings
	decodeJSONBody(t, settingsResp, &settings)

	updateSettingsResp := jsonRequest(t, h.apiServer, http.MethodPut, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/settings/", h.pat, map[string]any{
		"name":               "Harness Project",
		"platform":           "ios",
		"status":             "active",
		"eventRetentionDays": 14,
		"replayPolicy": map[string]any{
			"sampleRate": 0.5,
			"maxBytes":   2048,
		},
	})
	if updateSettingsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s update settings status=%d", h.name, updateSettingsResp.StatusCode)
	}
	decodeJSONBody(t, updateSettingsResp, &settings)

	inviteResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/invites/", h.pat, map[string]any{
		"email":    "new-user@example.com",
		"role":     "member",
		"teamSlug": harnessTeamSlug,
	})
	if inviteResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create invite status=%d", h.name, inviteResp.StatusCode)
	}
	var invite struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, inviteResp, &invite)
	acceptInviteResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/invites/"+invite.Token+"/accept/", "", map[string]any{
		"displayName": "New User",
		"password":    "temporary-pass-123",
	})
	if acceptInviteResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s accept invite status=%d", h.name, acceptInviteResp.StatusCode)
	}
	acceptInviteResp.Body.Close()
	membersResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/members/", h.pat, nil)
	if membersResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list members status=%d", h.name, membersResp.StatusCode)
	}
	var members []map[string]any
	decodeJSONBody(t, membersResp, &members)

	listPATResp := sessionJSONRequest(t, client, http.MethodGet, h.apiServer.URL+"/api/0/users/me/personal-access-tokens/", h.sessionToken, "", nil)
	if listPATResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list PATs baseline status=%d", h.name, listPATResp.StatusCode)
	}
	var beforePATs []map[string]any
	decodeJSONBody(t, listPATResp, &beforePATs)

	patResp := sessionJSONRequest(t, client, http.MethodPost, h.apiServer.URL+"/api/0/users/me/personal-access-tokens/", h.sessionToken, h.csrfToken, map[string]any{
		"label":  "CLI Token",
		"scopes": []string{auth.ScopeProjectRead},
	})
	if patResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create PAT status=%d", h.name, patResp.StatusCode)
	}
	patResp.Body.Close()
	listPATResp = sessionJSONRequest(t, client, http.MethodGet, h.apiServer.URL+"/api/0/users/me/personal-access-tokens/", h.sessionToken, "", nil)
	if listPATResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list PATs status=%d", h.name, listPATResp.StatusCode)
	}
	var pats []map[string]any
	decodeJSONBody(t, listPATResp, &pats)

	autoResp := sessionJSONRequest(t, client, http.MethodPost, h.apiServer.URL+"/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/automation-tokens/", h.sessionToken, h.csrfToken, map[string]any{
		"label":  "CI Token",
		"scopes": []string{auth.ScopeProjectArtifactsWrite},
	})
	if autoResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create automation token status=%d", h.name, autoResp.StatusCode)
	}
	autoResp.Body.Close()
	listAutoResp := sessionJSONRequest(t, client, http.MethodGet, h.apiServer.URL+"/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/automation-tokens/", h.sessionToken, "", nil)
	if listAutoResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list automation tokens status=%d", h.name, listAutoResp.StatusCode)
	}
	var autos []map[string]any
	decodeJSONBody(t, listAutoResp, &autos)

	seedHarnessIssues(t, h)
	projectIssuesResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/issues/?query=release:backend@2.0.0%20CheckoutError", h.pat, nil)
	if projectIssuesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list project issues status=%d", h.name, projectIssuesResp.StatusCode)
	}
	var projectIssues []api.Issue
	decodeJSONBody(t, projectIssuesResp, &projectIssues)
	issueDetailResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/issues/grp-harness-1/", h.pat, nil)
	if issueDetailResp.StatusCode != http.StatusOK {
		t.Fatalf("%s get issue status=%d", h.name, issueDetailResp.StatusCode)
	}
	var issueDetail api.Issue
	decodeJSONBody(t, issueDetailResp, &issueDetail)
	orgIssuesResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/issues/?query=release:backend@2.0.0%20CheckoutError", h.pat, nil)
	if orgIssuesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list org issues status=%d", h.name, orgIssuesResp.StatusCode)
	}
	var orgIssues []api.Issue
	decodeJSONBody(t, orgIssuesResp, &orgIssues)
	discoverIssuesResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/discover/?scope=issues&query=release:backend@2.0.0%20CheckoutError", h.pat, nil)
	if discoverIssuesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s discover issues status=%d", h.name, discoverIssuesResp.StatusCode)
	}
	var discoverIssues api.DiscoverResponse
	decodeJSONBody(t, discoverIssuesResp, &discoverIssues)

	updateIssueResp := jsonRequest(t, h.apiServer, http.MethodPut, "/api/0/issues/grp-harness-1/", h.pat, map[string]any{
		"status":              "resolved",
		"resolutionSubstatus": "next_release",
		"resolvedInRelease":   "backend@2.0.0",
	})
	if updateIssueResp.StatusCode != http.StatusOK {
		t.Fatalf("%s update issue status=%d", h.name, updateIssueResp.StatusCode)
	}
	var issueResp struct {
		Status              string `json:"status"`
		ResolutionSubstatus string `json:"resolutionSubstatus"`
		ResolvedInRelease   string `json:"resolvedInRelease"`
	}
	decodeJSONBody(t, updateIssueResp, &issueResp)

	commentResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/issues/grp-harness-1/comments/", h.pat, map[string]any{"body": "Investigating"})
	if commentResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create issue comment status=%d", h.name, commentResp.StatusCode)
	}
	commentResp.Body.Close()
	commentsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/issues/grp-harness-1/comments/", h.pat, nil)
	if commentsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list issue comments status=%d", h.name, commentsResp.StatusCode)
	}
	var comments []map[string]any
	decodeJSONBody(t, commentsResp, &comments)
	activityResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/issues/grp-harness-1/activity/", h.pat, nil)
	if activityResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list issue activity status=%d", h.name, activityResp.StatusCode)
	}
	var activity []map[string]any
	decodeJSONBody(t, activityResp, &activity)

	mergeResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/issues/grp-harness-merged/merge/", h.pat, map[string]any{
		"targetIssueId": "grp-harness-1",
	})
	if mergeResp.StatusCode != http.StatusOK {
		t.Fatalf("%s merge issue status=%d", h.name, mergeResp.StatusCode)
	}
	mergeResp.Body.Close()
	unmergeResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/issues/grp-harness-merged/unmerge/", h.pat, map[string]any{})
	if unmergeResp.StatusCode != http.StatusOK {
		t.Fatalf("%s unmerge issue status=%d", h.name, unmergeResp.StatusCode)
	}
	unmergeResp.Body.Close()

	releaseResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/releases/", h.pat, map[string]any{"version": "backend@2.0.0"})
	if releaseResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create release status=%d", h.name, releaseResp.StatusCode)
	}
	var release struct {
		Version string `json:"version"`
	}
	decodeJSONBody(t, releaseResp, &release)
	deployResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/releases/backend@2.0.0/deploys/", h.pat, map[string]any{
		"environment": "production",
		"name":        "deploy-1",
	})
	if deployResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create deploy status=%d", h.name, deployResp.StatusCode)
	}
	deployResp.Body.Close()
	commitResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/releases/backend@2.0.0/commits/", h.pat, map[string]any{
		"commitSha": "abc123def456",
		"message":   "Fix checkout crash",
		"files":     []string{"checkout.go"},
	})
	if commitResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create commit status=%d", h.name, commitResp.StatusCode)
	}
	commitResp.Body.Close()
	deploysResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/releases/backend@2.0.0/deploys/", h.pat, nil)
	if deploysResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list deploys status=%d", h.name, deploysResp.StatusCode)
	}
	var deploys []map[string]any
	decodeJSONBody(t, deploysResp, &deploys)
	commitsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/releases/backend@2.0.0/commits/", h.pat, nil)
	if commitsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list commits status=%d", h.name, commitsResp.StatusCode)
	}
	var commits []map[string]any
	decodeJSONBody(t, commitsResp, &commits)

	alertResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/alerts/", h.pat, map[string]any{
		"name": "High latency",
		"conditions": []map[string]any{{
			"id":    alert.ConditionSlowTransaction,
			"name":  "Slow transaction",
			"value": map[string]any{"threshold_ms": 250},
		}},
		"actions": []map[string]any{{
			"type":             alert.ActionTypeEmail,
			"targetIdentifier": "ops@example.com",
		}},
	})
	if alertResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create alert status=%d", h.name, alertResp.StatusCode)
	}
	var createdRule alert.Rule
	decodeJSONBody(t, alertResp, &createdRule)
	if err := h.control.Deliveries.RecordDelivery(context.Background(), &notify.DeliveryRecord{
		ID:        "delivery-" + h.name,
		ProjectID: harnessProjectID,
		RuleID:    createdRule.ID,
		Kind:      "email",
		Target:    "ops@example.com",
		Status:    notify.DeliveryStatusQueued,
		Attempts:  1,
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("%s seed alert delivery: %v", h.name, err)
	}
	alertsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/alerts/", h.pat, nil)
	if alertsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list alerts status=%d", h.name, alertsResp.StatusCode)
	}
	var alertRules []map[string]any
	decodeJSONBody(t, alertsResp, &alertRules)
	deliveriesResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/alerts/deliveries/", h.pat, nil)
	if deliveriesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list deliveries status=%d", h.name, deliveriesResp.StatusCode)
	}
	var deliveries []map[string]any
	decodeJSONBody(t, deliveriesResp, &deliveries)

	monitorResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/monitors/", h.pat, map[string]any{
		"slug":        "nightly-import",
		"status":      "active",
		"environment": "production",
		"config": map[string]any{
			"schedule": map[string]any{
				"type":  "interval",
				"value": 5,
				"unit":  "minute",
			},
			"checkInMargin": 1,
			"maxRuntime":    60,
			"timezone":      "UTC",
		},
	})
	if monitorResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s create monitor status=%d", h.name, monitorResp.StatusCode)
	}
	var monitor map[string]any
	decodeJSONBody(t, monitorResp, &monitor)
	if _, err := h.control.Monitors.SaveCheckIn(context.Background(), &sqlite.MonitorCheckIn{
		ID:           "checkin-" + h.name,
		ProjectID:    harnessProjectID,
		CheckInID:    "run-" + h.name,
		MonitorSlug:  "nightly-import",
		Status:       "ok",
		Environment:  "production",
		DateCreated:  time.Unix(1700000000, 0).UTC(),
		ScheduledFor: time.Unix(1700000000, 0).UTC(),
	}, &sqlite.MonitorConfig{
		Schedule: sqlite.MonitorSchedule{Type: "interval", Value: 5, Unit: "minute"},
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("%s save checkin: %v", h.name, err)
	}
	checkInsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/monitors/nightly-import/check-ins/", h.pat, nil)
	if checkInsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list checkins status=%d", h.name, checkInsResp.StatusCode)
	}
	var checkIns []map[string]any
	decodeJSONBody(t, checkInsResp, &checkIns)

	return apiSnapshot{
		ProjectName:          settings.Name,
		ReplaySampleRate:     settings.ReplayPolicy.SampleRate,
		ReplayMaxBytes:       settings.ReplayPolicy.MaxBytes,
		MemberCount:          len(members),
		PATCount:             len(pats) - len(beforePATs),
		AutomationTokenCount: len(autos),
		ProjectIssueCount:    len(projectIssues),
		OrgIssueCount:        len(orgIssues),
		DiscoverIssueCount:   len(discoverIssues.Issues),
		IssueTitle:           issueDetail.Title,
		IssueStatus:          issueResp.Status,
		IssueSubstatus:       issueResp.ResolutionSubstatus,
		IssueRelease:         issueResp.ResolvedInRelease,
		IssueCommentCount:    len(comments),
		IssueActivityCount:   len(activity),
		ReleaseVersion:       release.Version,
		ReleaseDeployCount:   len(deploys),
		ReleaseCommitCount:   len(commits),
		AlertRuleCount:       len(alertRules),
		AlertDeliveryCount:   len(deliveries),
		MonitorSlug:          stringValue(monitor["slug"]),
		MonitorStatus:        stringValue(monitor["status"]),
		MonitorCheckInCount:  len(checkIns),
	}
}

func runControlPlaneWebSuite(t *testing.T, h *controlPlaneHarness) webSnapshot {
	t.Helper()
	seedHarnessIssues(t, h)

	if _, err := h.control.Ownership.CreateRule(context.Background(), sharedstore.OwnershipRule{
		ProjectID: harnessProjectID,
		Name:      "Payments",
		Pattern:   "path:payments.go",
		Assignee:  "payments@team",
	}); err != nil {
		t.Fatalf("%s create ownership rule: %v", h.name, err)
	}

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	settingsForm := url.Values{
		"name":                      {"Harness Project"},
		"platform":                  {"ios"},
		"status":                    {"active"},
		"event_retention_days":      {"14"},
		"attachment_retention_days": {"7"},
		"debug_retention_days":      {"30"},
		"replay_sample_rate":        {"0.5"},
		"replay_max_bytes":          {"2048"},
	}
	settingsResp := sessionFormRequest(t, client, h.webServer.URL+"/settings/project", h.sessionToken, h.csrfToken, settingsForm)
	if settingsResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("%s web update settings status=%d", h.name, settingsResp.StatusCode)
	}
	settingsResp.Body.Close()

	updatedSettings, err := h.control.Catalog.GetProjectSettings(context.Background(), harnessOrgSlug, harnessProjectSlug)
	if err != nil {
		t.Fatalf("%s load updated settings: %v", h.name, err)
	}

	alertForm := url.Values{
		"project_id":    {harnessProjectID},
		"name":          {"Checkout latency"},
		"status":        {"active"},
		"trigger":       {"slow_transaction"},
		"threshold_ms":  {"250"},
		"email_targets": {"ops@example.com"},
	}
	alertResp := sessionFormRequest(t, client, h.webServer.URL+"/alerts/", h.sessionToken, h.csrfToken, alertForm)
	if alertResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("%s web create alert status=%d", h.name, alertResp.StatusCode)
	}
	alertResp.Body.Close()
	alertsPage := authenticatedGet(t, client, h.webServer.URL+"/alerts/", h.sessionToken)

	monitorForm := url.Values{
		"project_id":     {harnessProjectID},
		"slug":           {"nightly-import"},
		"status":         {"active"},
		"environment":    {"production"},
		"schedule_type":  {"interval"},
		"schedule_value": {"5"},
		"schedule_unit":  {"minute"},
		"checkin_margin": {"1"},
		"max_runtime":    {"60"},
		"timezone":       {"UTC"},
	}
	monitorResp := sessionFormRequest(t, client, h.webServer.URL+"/monitors/", h.sessionToken, h.csrfToken, monitorForm)
	if monitorResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("%s web create monitor status=%d", h.name, monitorResp.StatusCode)
	}
	monitorResp.Body.Close()
	monitorsPage := authenticatedGet(t, client, h.webServer.URL+"/monitors/", h.sessionToken)

	if _, err := h.control.Releases.CreateRelease(context.Background(), harnessOrgSlug, "backend@3.0.0"); err != nil {
		t.Fatalf("%s create web release: %v", h.name, err)
	}
	if _, err := h.control.Releases.AddDeploy(context.Background(), harnessOrgSlug, "backend@3.0.0", sharedstore.ReleaseDeploy{
		Environment: "production",
		Name:        "deploy-web-1",
	}); err != nil {
		t.Fatalf("%s create web deploy: %v", h.name, err)
	}
	if _, err := h.control.Releases.AddCommit(context.Background(), harnessOrgSlug, "backend@3.0.0", sharedstore.ReleaseCommit{
		CommitSHA: "abc123def456",
		Message:   "Fix checkout crash",
		Files:     []string{"checkout.go"},
	}); err != nil {
		t.Fatalf("%s create web commit: %v", h.name, err)
	}
	releasePage := authenticatedGet(t, client, h.webServer.URL+"/releases/backend@3.0.0/", h.sessionToken)
	discoverPage := authenticatedGet(t, client, h.webServer.URL+"/discover/?scope=issues&query=release:backend@2.0.0%20CheckoutError", h.sessionToken)

	return webSnapshot{
		SettingsUpdated: updatedSettings != nil && updatedSettings.Name == "Harness Project" && updatedSettings.ReplayPolicy.SampleRate == 0.5,
		AlertVisible:    strings.Contains(alertsPage, "Checkout latency"),
		MonitorVisible:  strings.Contains(monitorsPage, "nightly-import"),
		ReleaseVisible:  strings.Contains(releasePage, "deploy-web-1") && strings.Contains(releasePage, "abc123def456"),
		DiscoverVisible: strings.Contains(discoverPage, "CheckoutError"),
	}
}

func runControlPlanePreventSuite(t *testing.T, h *controlPlaneHarness) preventSnapshot {
	t.Helper()

	const owner = "sentry"
	const repository = "platform"
	basePath := "/api/0/organizations/" + harnessOrgSlug + "/prevent/owner/" + owner

	reposResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repositories/", h.pat, nil)
	if reposResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent repositories status=%d", h.name, reposResp.StatusCode)
	}
	var repositories struct {
		Results []struct {
			Name           string `json:"name"`
			UpdatedAt      string `json:"updatedAt"`
			LatestCommitAt string `json:"latestCommitAt"`
			DefaultBranch  string `json:"defaultBranch"`
		} `json:"results"`
		TotalCount int `json:"totalCount"`
	}
	decodeJSONBody(t, reposResp, &repositories)

	detailResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/", h.pat, nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("%s get Prevent repository status=%d", h.name, detailResp.StatusCode)
	}
	var detail struct {
		UploadToken          string `json:"uploadToken"`
		TestAnalyticsEnabled bool   `json:"testAnalyticsEnabled"`
	}
	decodeJSONBody(t, detailResp, &detail)

	tokensResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repositories/tokens/", h.pat, nil)
	if tokensResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent tokens status=%d", h.name, tokensResp.StatusCode)
	}
	var tokens struct {
		Results []struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		} `json:"results"`
		TotalCount int `json:"totalCount"`
	}
	decodeJSONBody(t, tokensResp, &tokens)

	syncResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repositories/sync/", h.pat, nil)
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("%s get Prevent sync status=%d", h.name, syncResp.StatusCode)
	}
	var syncStatus struct {
		IsSyncing bool `json:"isSyncing"`
	}
	decodeJSONBody(t, syncResp, &syncStatus)

	startSyncResp := jsonRequest(t, h.apiServer, http.MethodPost, basePath+"/repositories/sync/", h.pat, map[string]any{})
	if startSyncResp.StatusCode != http.StatusOK {
		t.Fatalf("%s start Prevent sync status=%d", h.name, startSyncResp.StatusCode)
	}
	var startedSync struct {
		IsSyncing bool `json:"isSyncing"`
	}
	decodeJSONBody(t, startSyncResp, &startedSync)

	branchesResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/branches/", h.pat, nil)
	if branchesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent branches status=%d", h.name, branchesResp.StatusCode)
	}
	var branches struct {
		DefaultBranch string `json:"defaultBranch"`
		Results       []struct {
			Name string `json:"name"`
		} `json:"results"`
		TotalCount int `json:"totalCount"`
	}
	decodeJSONBody(t, branchesResp, &branches)

	suitesResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/test-suites/", h.pat, nil)
	if suitesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent test suites status=%d", h.name, suitesResp.StatusCode)
	}
	var suites struct {
		TestSuites []string `json:"testSuites"`
	}
	decodeJSONBody(t, suitesResp, &suites)

	resultsResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/test-results/", h.pat, nil)
	if resultsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent test results status=%d", h.name, resultsResp.StatusCode)
	}
	var results struct {
		Results []struct {
			Name           string  `json:"name"`
			FailureRate    float64 `json:"failureRate"`
			TotalFailCount int     `json:"totalFailCount"`
		} `json:"results"`
		TotalCount int `json:"totalCount"`
	}
	decodeJSONBody(t, resultsResp, &results)

	flakyResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/test-results/?interval=INTERVAL_7_DAY&filterBy=FLAKY_TESTS", h.pat, nil)
	if flakyResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent flaky test results status=%d", h.name, flakyResp.StatusCode)
	}
	var flakyResults struct {
		Results []struct {
			Name                string  `json:"name"`
			FlakeRate           float64 `json:"flakeRate"`
			TotalFlakyFailCount int     `json:"totalFlakyFailCount"`
		} `json:"results"`
		TotalCount int `json:"totalCount"`
	}
	decodeJSONBody(t, flakyResp, &flakyResults)
	if flakyResults.TotalCount == 0 || len(flakyResults.Results) == 0 {
		t.Fatalf("%s flaky Prevent results were empty", h.name)
	}

	aggregatesResp := jsonRequest(t, h.apiServer, http.MethodGet, basePath+"/repository/"+repository+"/test-results-aggregates/", h.pat, nil)
	if aggregatesResp.StatusCode != http.StatusOK {
		t.Fatalf("%s list Prevent aggregates status=%d", h.name, aggregatesResp.StatusCode)
	}
	var aggregates struct {
		TotalFails     int     `json:"totalFails"`
		TotalSkips     int     `json:"totalSkips"`
		TotalSlowTests int     `json:"totalSlowTests"`
		FlakeCount     int     `json:"flakeCount"`
		FlakeRate      float64 `json:"flakeRate"`
	}
	decodeJSONBody(t, aggregatesResp, &aggregates)

	regenerateResp := jsonRequest(t, h.apiServer, http.MethodPost, basePath+"/repository/"+repository+"/token/regenerate/", h.pat, map[string]any{})
	if regenerateResp.StatusCode != http.StatusOK {
		t.Fatalf("%s regenerate Prevent token status=%d", h.name, regenerateResp.StatusCode)
	}
	var regenerated struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, regenerateResp, &regenerated)

	branchNames := make([]string, 0, len(branches.Results))
	for _, branch := range branches.Results {
		branchNames = append(branchNames, branch.Name)
	}

	snapshot := preventSnapshot{
		RepositoryCount:      repositories.TotalCount,
		DetailHasUploadToken: strings.TrimSpace(detail.UploadToken) != "",
		DetailAnalytics:      detail.TestAnalyticsEnabled,
		TokenCount:           tokens.TotalCount,
		SyncBefore:           syncStatus.IsSyncing,
		SyncAfter:            startedSync.IsSyncing,
		BranchCount:          branches.TotalCount,
		BranchDefault:        branches.DefaultBranch,
		BranchNames:          branchNames,
		Suites:               suites.TestSuites,
		TestResultCount:      results.TotalCount,
		FlakyResultCount:     flakyResults.TotalCount,
		AggregateTotalFails:  aggregates.TotalFails,
		AggregateTotalSkips:  aggregates.TotalSkips,
		AggregateSlowTests:   aggregates.TotalSlowTests,
		AggregateFlakeCount:  aggregates.FlakeCount,
		AggregateFlakeRate:   aggregates.FlakeRate,
		RegeneratedTokenValid: strings.HasPrefix(regenerated.Token, "gprevent_") &&
			strings.Count(regenerated.Token, "_") >= 2 &&
			regenerated.Token != detail.UploadToken,
	}
	if len(repositories.Results) > 0 {
		snapshot.RepositoryName = repositories.Results[0].Name
		snapshot.RepositoryDefault = repositories.Results[0].DefaultBranch
		snapshot.RepositoryUpdatedAt = repositories.Results[0].UpdatedAt
		snapshot.RepositoryLatestAt = repositories.Results[0].LatestCommitAt
	}
	if len(tokens.Results) > 0 {
		snapshot.TokenName = tokens.Results[0].Name
		snapshot.TokenValue = tokens.Results[0].Token
	}
	if len(results.Results) > 0 {
		snapshot.TestResultName = results.Results[0].Name
		snapshot.TestResultFailureRate = results.Results[0].FailureRate
		snapshot.TestResultFailCount = results.Results[0].TotalFailCount
	}
	if len(flakyResults.Results) > 0 {
		snapshot.FlakyResultName = flakyResults.Results[0].Name
		snapshot.FlakyResultRate = flakyResults.Results[0].FlakeRate
		snapshot.FlakyResultFailCount = flakyResults.Results[0].TotalFlakyFailCount
	}
	return snapshot
}

type harnessSentryApp struct {
	ID             string   `json:"id"`
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	WebhookURL     string   `json:"webhookUrl"`
	AllowedOrigins []string `json:"allowedOrigins"`
}

type harnessSentryAppInstallation struct {
	UUID   string           `json:"uuid"`
	App    harnessSentryApp `json:"app"`
	Status string           `json:"status"`
}

type harnessExternalIssueLink struct {
	ID          string `json:"id"`
	IssueID     string `json:"issueId"`
	ServiceType string `json:"serviceType"`
	DisplayName string `json:"displayName"`
	WebURL      string `json:"webUrl"`
}

type harnessPreprodArtifact struct {
	BuildID    string  `json:"buildId"`
	InstallURL *string `json:"installUrl"`
}

type harnessPreprodSizeAnalysis struct {
	State       string     `json:"state"`
	BaseBuildID *string    `json:"baseBuildId"`
	Comparisons []struct{} `json:"comparisons"`
}

type harnessPreprodLatest struct {
	LatestArtifact  *harnessPreprodArtifact `json:"latestArtifact"`
	CurrentArtifact *harnessPreprodArtifact `json:"currentArtifact"`
}

func runControlPlaneIntegrationSuite(t *testing.T, h *controlPlaneHarness) integrationParitySnapshot {
	t.Helper()

	seedHarnessIssues(t, h)

	updateResp := jsonRequest(t, h.apiServer, http.MethodPut, "/api/0/sentry-apps/webhook/", h.pat, map[string]any{
		"name":           "Webhook Plus",
		"scopes":         []string{"event:read", "event:write"},
		"author":         "urgentry labs",
		"overview":       "Updated webhook app",
		"events":         []string{"issue", "event.alert"},
		"allowedOrigins": []string{"https://example.com"},
		"isAlertable":    false,
		"verifyInstall":  false,
		"schema": map[string]any{
			"elements": []map[string]any{{"type": "text", "name": "url"}},
		},
		"redirectUrl": "https://example.com/install",
		"webhookUrl":  "https://example.com/webhook",
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("%s sentry app update status = %d", h.name, updateResp.StatusCode)
	}
	var app harnessSentryApp
	if err := json.NewDecoder(updateResp.Body).Decode(&app); err != nil {
		t.Fatalf("%s decode sentry app update: %v", h.name, err)
	}
	_ = updateResp.Body.Close()

	installResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/integrations/webhook/install", h.pat, map[string]any{
		"config": map[string]any{"url": "https://example.com/hook"},
	})
	if installResp.StatusCode != http.StatusCreated {
		t.Fatalf("%s integration install status = %d", h.name, installResp.StatusCode)
	}
	var installation integration.IntegrationConfig
	if err := json.NewDecoder(installResp.Body).Decode(&installation); err != nil {
		t.Fatalf("%s decode integration install: %v", h.name, err)
	}
	_ = installResp.Body.Close()

	installationsResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/sentry-app-installations/", h.pat, nil)
	if installationsResp.StatusCode != http.StatusOK {
		t.Fatalf("%s sentry app installations status = %d", h.name, installationsResp.StatusCode)
	}
	var installations []harnessSentryAppInstallation
	if err := json.NewDecoder(installationsResp.Body).Decode(&installations); err != nil {
		t.Fatalf("%s decode sentry app installations: %v", h.name, err)
	}
	_ = installationsResp.Body.Close()

	createExternalResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/", h.pat, map[string]any{
		"issueId":    "grp-harness-1",
		"webUrl":     "https://example.com/ExternalProj/issue-1",
		"project":    "ExternalProj",
		"identifier": "issue-1",
	})
	if createExternalResp.StatusCode != http.StatusOK {
		t.Fatalf("%s external issue create status = %d", h.name, createExternalResp.StatusCode)
	}
	var external harnessExternalIssueLink
	if err := json.NewDecoder(createExternalResp.Body).Decode(&external); err != nil {
		t.Fatalf("%s decode external issue create: %v", h.name, err)
	}
	_ = createExternalResp.Body.Close()

	updateExternalResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/", h.pat, map[string]any{
		"issueId":    "grp-harness-1",
		"webUrl":     "https://example.com/ExternalProj/issue-1-updated",
		"project":    "ExternalProj",
		"identifier": "issue-1",
	})
	if updateExternalResp.StatusCode != http.StatusOK {
		t.Fatalf("%s external issue update status = %d", h.name, updateExternalResp.StatusCode)
	}
	if err := json.NewDecoder(updateExternalResp.Body).Decode(&external); err != nil {
		t.Fatalf("%s decode external issue update: %v", h.name, err)
	}
	_ = updateExternalResp.Body.Close()

	listExternalResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/external-issues/", h.pat, nil)
	if listExternalResp.StatusCode != http.StatusOK {
		t.Fatalf("%s external issue list status = %d", h.name, listExternalResp.StatusCode)
	}
	var externalLinks []harnessExternalIssueLink
	if err := json.NewDecoder(listExternalResp.Body).Decode(&externalLinks); err != nil {
		t.Fatalf("%s decode external issue list: %v", h.name, err)
	}
	_ = listExternalResp.Body.Close()

	deleteExternalResp := jsonRequest(t, h.apiServer, http.MethodDelete, "/api/0/sentry-app-installations/"+installation.ID+"/external-issues/"+external.ID+"/", h.pat, nil)
	if deleteExternalResp.StatusCode != http.StatusNoContent {
		t.Fatalf("%s external issue delete status = %d", h.name, deleteExternalResp.StatusCode)
	}
	_ = deleteExternalResp.Body.Close()

	listAfterDeleteResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/external-issues/", h.pat, nil)
	if listAfterDeleteResp.StatusCode != http.StatusOK {
		t.Fatalf("%s external issue list after delete status = %d", h.name, listAfterDeleteResp.StatusCode)
	}
	var externalLinksAfterDelete []harnessExternalIssueLink
	if err := json.NewDecoder(listAfterDeleteResp.Body).Decode(&externalLinksAfterDelete); err != nil {
		t.Fatalf("%s decode external issue list after delete: %v", h.name, err)
	}
	_ = listAfterDeleteResp.Body.Close()

	deleteAppResp := jsonRequest(t, h.apiServer, http.MethodDelete, "/api/0/sentry-apps/webhook/", h.pat, nil)
	appGetAfterDeleteResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/sentry-apps/webhook/", h.pat, nil)
	_ = appGetAfterDeleteResp.Body.Close()

	snapshot := integrationParitySnapshot{
		SentryAppName:              app.Name,
		SentryAppWebhookURL:        app.WebhookURL,
		SentryAppAllowedOrigins:    app.AllowedOrigins,
		InstallationCount:          len(installations),
		ExternalIssueDisplayName:   external.DisplayName,
		ExternalIssueServiceType:   external.ServiceType,
		ExternalIssueWebURL:        external.WebURL,
		ExternalIssueCount:         len(externalLinks),
		ExternalIssueCountAfterDel: len(externalLinksAfterDelete),
		AppDeleteStatus:            deleteAppResp.StatusCode,
		AppGetAfterDeleteStatus:    appGetAfterDeleteResp.StatusCode,
	}
	if len(installations) > 0 {
		snapshot.InstallationAppName = installations[0].App.Name
	}
	_ = deleteAppResp.Body.Close()
	return snapshot
}

func runControlPlanePreprodArtifactSuite(t *testing.T, h *controlPlaneHarness) preprodArtifactSnapshot {
	t.Helper()

	installResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/preprodartifacts/artifact-head/install-details/", h.pat, nil)
	if installResp.StatusCode != http.StatusOK {
		t.Fatalf("%s install-details status = %d", h.name, installResp.StatusCode)
	}
	var install harnessPreprodArtifact
	if err := json.NewDecoder(installResp.Body).Decode(&install); err != nil {
		t.Fatalf("%s decode install-details: %v", h.name, err)
	}
	_ = installResp.Body.Close()

	sizeResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/preprodartifacts/artifact-head/size-analysis/", h.pat, nil)
	if sizeResp.StatusCode != http.StatusOK {
		t.Fatalf("%s size-analysis status = %d", h.name, sizeResp.StatusCode)
	}
	var size harnessPreprodSizeAnalysis
	if err := json.NewDecoder(sizeResp.Body).Decode(&size); err != nil {
		t.Fatalf("%s decode size-analysis: %v", h.name, err)
	}
	_ = sizeResp.Body.Close()

	latestResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/preprodartifacts/build-distribution/latest/?appId=com.example.app&platform=apple&buildVersion=1.0.0&buildNumber=41", h.pat, nil)
	if latestResp.StatusCode != http.StatusOK {
		t.Fatalf("%s latest build status = %d", h.name, latestResp.StatusCode)
	}
	var latest harnessPreprodLatest
	if err := json.NewDecoder(latestResp.Body).Decode(&latest); err != nil {
		t.Fatalf("%s decode latest build: %v", h.name, err)
	}
	_ = latestResp.Body.Close()

	invalidResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/projects/"+harnessOrgSlug+"/"+harnessProjectSlug+"/preprodartifacts/build-distribution/latest/?platform=apple", h.pat, nil)
	_ = invalidResp.Body.Close()

	snapshot := preprodArtifactSnapshot{
		SizeState:           size.State,
		SizeComparisonCount: len(size.Comparisons),
		InvalidQueryStatus:  invalidResp.StatusCode,
	}
	if install.InstallURL != nil {
		snapshot.InstallURL = *install.InstallURL
	}
	if size.BaseBuildID != nil {
		snapshot.SizeBaseBuildID = *size.BaseBuildID
	}
	snapshot.InstallBuildID = install.BuildID
	if latest.LatestArtifact != nil {
		snapshot.LatestBuildID = latest.LatestArtifact.BuildID
	}
	if latest.CurrentArtifact != nil {
		snapshot.CurrentBuildID = latest.CurrentArtifact.BuildID
	}
	return snapshot
}

func runControlPlaneAutofixSuite(t *testing.T, h *controlPlaneHarness) autofixSnapshot {
	t.Helper()

	seedHarnessIssues(t, h)

	initialResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/autofix/", h.pat, nil)
	if initialResp.StatusCode != http.StatusOK {
		t.Fatalf("%s initial autofix get status = %d", h.name, initialResp.StatusCode)
	}
	var initial map[string]any
	if err := json.NewDecoder(initialResp.Body).Decode(&initial); err != nil {
		t.Fatalf("%s decode initial autofix get: %v", h.name, err)
	}
	_ = initialResp.Body.Close()

	startResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/autofix/", h.pat, map[string]any{
		"instruction":          "Focus on checkout failures.",
		"pr_to_comment_on_url": "https://github.com/acme/backend/pull/42",
		"stopping_point":       "open_pr",
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s autofix start status = %d", h.name, startResp.StatusCode)
	}
	var started struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("%s decode autofix start: %v", h.name, err)
	}
	_ = startResp.Body.Close()

	getResp := jsonRequest(t, h.apiServer, http.MethodGet, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/autofix/", h.pat, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("%s autofix get status = %d", h.name, getResp.StatusCode)
	}
	var current map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&current); err != nil {
		t.Fatalf("%s decode autofix get: %v", h.name, err)
	}
	_ = getResp.Body.Close()

	invalidResp := jsonRequest(t, h.apiServer, http.MethodPost, "/api/0/organizations/"+harnessOrgSlug+"/issues/grp-harness-1/autofix/", h.pat, map[string]any{
		"event_id": "evt-does-not-belong",
	})
	_ = invalidResp.Body.Close()

	autofix, _ := current["autofix"].(map[string]any)
	steps, _ := autofix["steps"].([]any)
	pullRequestStatus := ""
	if pullRequest, ok := autofix["pull_request"].(map[string]any); ok {
		if status, ok := pullRequest["status"].(string); ok {
			pullRequestStatus = status
		}
	}
	status, _ := autofix["status"].(string)

	return autofixSnapshot{
		InitialNil:    initial["autofix"] == nil,
		RunID:         started.RunID,
		Status:        status,
		StepCount:     len(steps),
		PullRequest:   pullRequestStatus,
		InvalidStatus: invalidResp.StatusCode,
	}
}

func openHarnessSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedHarnessQueryPlane(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('` + harnessOrgID + `', '` + harnessOrgSlug + `', 'Test Org');
INSERT OR IGNORE INTO teams (id, organization_id, slug, name) VALUES ('` + harnessTeamID + `', '` + harnessOrgID + `', '` + harnessTeamSlug + `', 'Backend Team');
INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES ('` + harnessProjectID + `', '` + harnessOrgID + `', '` + harnessProjectSlug + `', 'Test Project', 'go', 'active', '` + now + `');
INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label) VALUES ('key-test', '` + harnessProjectID + `', 'test-public-key', 'active', 'Default');
`); err != nil {
		t.Fatalf("seed query plane: %v", err)
	}

	store := sqlite.NewPreprodArtifactStore(db)
	build41 := 41
	build42 := 42
	trueValue := true
	download100 := int64(100)
	install200 := int64(200)
	download120 := int64(120)
	install230 := int64(230)
	duration := 9.5
	baseAdded := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	headAdded := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	for _, artifact := range []*sqlite.PreprodArtifact{
		{
			ID:             "artifact-base",
			OrganizationID: harnessOrgID,
			ProjectID:      harnessProjectID,
			BuildID:        "build-41",
			State:          "PROCESSED",
			AppInfo: sqlite.PreprodArtifactAppInfo{
				AppID:        "com.example.app",
				Name:         "Example App",
				Version:      "1.0.0",
				BuildNumber:  &build41,
				ArtifactType: "XCARCHIVE",
				DateAdded:    baseAdded,
				DateBuilt:    baseAdded,
			},
			Platform:             "APPLE",
			BuildConfiguration:   "release",
			IsInstallable:        true,
			InstallURL:           "https://example.invalid/install/build-41.plist",
			DownloadCount:        2,
			InstallGroups:        []string{"qa"},
			IsCodeSignatureValid: &trueValue,
			ProfileName:          "iOS Team Provisioning Profile",
			CodesigningType:      "development",
			MainBinaryIdentifier: "macho-base",
			AnalysisState:        "COMPLETED",
			DownloadSize:         &download100,
			InstallSize:          &install200,
			AnalysisDuration:     &duration,
			AnalysisVersion:      "1.0.0",
		},
		{
			ID:             "artifact-head",
			OrganizationID: harnessOrgID,
			ProjectID:      harnessProjectID,
			BuildID:        "build-42",
			State:          "PROCESSED",
			AppInfo: sqlite.PreprodArtifactAppInfo{
				AppID:        "com.example.app",
				Name:         "Example App",
				Version:      "1.1.0",
				BuildNumber:  &build42,
				ArtifactType: "XCARCHIVE",
				DateAdded:    headAdded,
				DateBuilt:    headAdded,
			},
			Platform:              "APPLE",
			BuildConfiguration:    "release",
			IsInstallable:         true,
			InstallURL:            "https://example.invalid/install/build-42.plist",
			DownloadCount:         5,
			InstallGroups:         []string{"qa", "beta"},
			IsCodeSignatureValid:  &trueValue,
			ProfileName:           "iOS Team Provisioning Profile",
			CodesigningType:       "development",
			MainBinaryIdentifier:  "macho-head",
			DefaultBaseArtifactID: "artifact-base",
			AnalysisState:         "COMPLETED",
			DownloadSize:          &download120,
			InstallSize:           &install230,
			AnalysisDuration:      &duration,
			AnalysisVersion:       "1.0.0",
		},
	} {
		if err := store.Save(context.Background(), artifact); err != nil {
			t.Fatalf("seed query-plane preprod artifact %s: %v", artifact.ID, err)
		}
	}
}

func seedHarnessPostgresControlPlane(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(harnessPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("seed postgres password hash: %v", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO organizations (id, slug, name) VALUES ($1, $2, 'Test Org')`, []any{harnessOrgID, harnessOrgSlug}},
		{`INSERT INTO teams (id, organization_id, slug, name) VALUES ($1, $2, $3, 'Backend Team')`, []any{harnessTeamID, harnessOrgID, harnessTeamSlug}},
		{`INSERT INTO users (id, email, display_name, is_active) VALUES ($1, $2, 'Owner', TRUE)`, []any{harnessUserID, harnessEmail}},
		{`INSERT INTO user_password_credentials (user_id, password_hash, password_algo) VALUES ($1, $2, 'bcrypt')`, []any{harnessUserID, string(passwordHash)}},
		{`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('member-1', $1, $2, 'owner', now())`, []any{harnessOrgID, harnessUserID}},
		{`INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, created_at, updated_at) VALUES ($1, $2, $3, $4, 'Test Project', 'go', 'active', now(), now())`, []any{harnessProjectID, harnessOrgID, harnessTeamID, harnessProjectSlug}},
		{`INSERT INTO project_keys (id, project_id, public_key, status, label, created_at) VALUES ('key-test', $1, 'test-public-key', 'active', 'Default', now())`, []any{harnessProjectID}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed postgres control plane: %v", err)
		}
	}
}

func seedHarnessPreventControlPlane(t *testing.T, h *controlPlaneHarness, base time.Time) {
	t.Helper()

	switch h.name {
	case "postgres":
		seedHarnessPostgresPreventControlPlane(t, h.controlDB, base)
	default:
		seedHarnessSQLitePreventControlPlane(t, h.controlDB, base)
	}
}

func seedHarnessSQLitePreventControlPlane(t *testing.T, db *sql.DB, base time.Time) {
	t.Helper()

	lastSynced := base.Add(-30 * time.Minute)
	lastStarted := base.Add(-45 * time.Minute)
	previousRun := base.Add(-6 * 24 * time.Hour)

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO repositories (id, organization_id, owner_slug, name, provider, url, external_slug, status, default_branch, test_analytics_enabled, sync_status, last_synced_at, last_sync_started_at, last_sync_error, created_at)
VALUES ('repo-prevent-1', ?, 'sentry', 'platform', 'github', 'https://github.com/sentry/platform', 'sentry/platform', 'active', 'main', 1, 'synced', ?, ?, '', ?)`,
			args: []any{harnessOrgID, lastSynced.Format(time.RFC3339), lastStarted.Format(time.RFC3339), base.Format(time.RFC3339)},
		},
		{
			query: `INSERT INTO prevent_repository_branches (id, repository_id, name, is_default, status, last_synced_at, created_at) VALUES
	('branch-prevent-1', 'repo-prevent-1', 'main', 1, 'active', ?, ?),
	('branch-prevent-2', 'repo-prevent-1', 'release/1.0', 0, 'active', NULL, ?)`,
			args: []any{lastSynced.Format(time.RFC3339), base.Format(time.RFC3339), base.Format(time.RFC3339)},
		},
		{
			query: `INSERT INTO prevent_repository_tokens (id, repository_id, label, token_value, token_prefix, token_hash, status, created_at, last_used_at, revoked_at, rotated_at)
VALUES ('token-prevent-1', 'repo-prevent-1', 'CI', 'gprevent_old_full', 'gprevent_old', 'hash-old', 'active', ?, NULL, NULL, NULL)`,
			args: []any{base.Format(time.RFC3339)},
		},
		{
			query: `INSERT INTO prevent_repository_test_suites (id, repository_id, external_suite_id, name, status, last_run_at, created_at)
VALUES ('suite-prevent-1', 'repo-prevent-1', 'suite-ext-1', 'Unit', 'active', ?, ?)`,
			args: []any{base.Format(time.RFC3339), base.Format(time.RFC3339)},
		},
		{
			query: `INSERT INTO prevent_repository_test_results (id, repository_id, suite_id, suite_name, branch_name, commit_sha, status, duration_ms, test_count, failure_count, skipped_count, created_at) VALUES
	('result-prevent-current', 'repo-prevent-1', 'suite-prevent-1', 'Unit', 'main', 'abc123', 'failed', 2400, 120, 3, 2, ?),
	('result-prevent-previous', 'repo-prevent-1', 'suite-prevent-1', 'Unit', 'main', 'def456', 'passed', 1200, 120, 0, 1, ?)`,
			args: []any{base.Format(time.RFC3339), previousRun.Format(time.RFC3339)},
		},
		{
			query: `INSERT INTO prevent_repository_test_result_aggregates (id, repository_id, branch_name, total_runs, passing_runs, failing_runs, skipped_runs, avg_duration_ms, last_run_at, created_at)
VALUES ('agg-prevent-1', 'repo-prevent-1', 'main', 10, 7, 2, 1, 1800, ?, ?)`,
			args: []any{base.Format(time.RFC3339), base.Format(time.RFC3339)},
		},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed sqlite Prevent control plane: %v", err)
		}
	}
}

func seedHarnessPostgresPreventControlPlane(t *testing.T, db *sql.DB, base time.Time) {
	t.Helper()

	lastSynced := base.Add(-30 * time.Minute)
	lastStarted := base.Add(-45 * time.Minute)
	previousRun := base.Add(-6 * 24 * time.Hour)

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO repositories (id, organization_id, owner_slug, name, provider, url, external_slug, status, default_branch, test_analytics_enabled, sync_status, last_synced_at, last_sync_started_at, last_sync_error, created_at)
VALUES ('repo-prevent-1', $1, 'sentry', 'platform', 'github', 'https://github.com/sentry/platform', 'sentry/platform', 'active', 'main', TRUE, 'synced', $2, $3, '', $4)`,
			args: []any{harnessOrgID, lastSynced, lastStarted, base},
		},
		{
			query: `INSERT INTO prevent_repository_branches (id, repository_id, name, is_default, status, last_synced_at, created_at) VALUES
	('branch-prevent-1', 'repo-prevent-1', 'main', TRUE, 'active', $1, $2),
	('branch-prevent-2', 'repo-prevent-1', 'release/1.0', FALSE, 'active', NULL, $3)`,
			args: []any{lastSynced, base, base},
		},
		{
			query: `INSERT INTO prevent_repository_tokens (id, repository_id, label, token_value, token_prefix, token_hash, status, created_at, last_used_at, revoked_at, rotated_at)
VALUES ('token-prevent-1', 'repo-prevent-1', 'CI', 'gprevent_old_full', 'gprevent_old', 'hash-old', 'active', $1, NULL, NULL, NULL)`,
			args: []any{base},
		},
		{
			query: `INSERT INTO prevent_repository_test_suites (id, repository_id, external_suite_id, name, status, last_run_at, created_at)
VALUES ('suite-prevent-1', 'repo-prevent-1', 'suite-ext-1', 'Unit', 'active', $1, $2)`,
			args: []any{base, base},
		},
		{
			query: `INSERT INTO prevent_repository_test_results (id, repository_id, suite_id, suite_name, branch_name, commit_sha, status, duration_ms, test_count, failure_count, skipped_count, created_at) VALUES
	('result-prevent-current', 'repo-prevent-1', 'suite-prevent-1', 'Unit', 'main', 'abc123', 'failed', 2400, 120, 3, 2, $1),
	('result-prevent-previous', 'repo-prevent-1', 'suite-prevent-1', 'Unit', 'main', 'def456', 'passed', 1200, 120, 0, 1, $2)`,
			args: []any{base, previousRun},
		},
		{
			query: `INSERT INTO prevent_repository_test_result_aggregates (id, repository_id, branch_name, total_runs, passing_runs, failing_runs, skipped_runs, avg_duration_ms, last_run_at, created_at)
VALUES ('agg-prevent-1', 'repo-prevent-1', 'main', 10, 7, 2, 1, 1800, $1, $2)`,
			args: []any{base, base},
		},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed postgres Prevent control plane: %v", err)
		}
	}
}

func seedHarnessQueryMembership(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
INSERT OR IGNORE INTO users (id, email, display_name, is_active) VALUES ('` + harnessUserID + `', '` + harnessEmail + `', 'Owner', 1);
INSERT OR IGNORE INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('member-1', '` + harnessOrgID + `', '` + harnessUserID + `', 'owner', '` + now + `');
`); err != nil {
		t.Fatalf("seed query membership: %v", err)
	}
}

func seedHarnessIssues(t *testing.T, h *controlPlaneHarness) {
	t.Helper()
	insertHarnessQueryGroup(t, h.queryDB, "grp-harness-1", "CheckoutError", "checkout.go in handler", "unresolved")
	insertHarnessQueryEvent(t, h.queryDB, "evt-harness-1", "grp-harness-1", "CheckoutError", "backend@2.0.0")
	insertHarnessQueryGroup(t, h.queryDB, "grp-harness-merged", "CheckoutError duplicate", "checkout.go in handler", "unresolved")
	insertHarnessQueryEvent(t, h.queryDB, "evt-harness-merged", "grp-harness-merged", "CheckoutError duplicate", "backend@2.0.0")
	if h.name == "postgres" {
		groupStore := NewGroupStore(h.controlDB)
		now := time.Now().UTC()
		for _, item := range []issue.Group{
			{
				ID:              "grp-harness-1",
				ProjectID:       harnessProjectID,
				GroupingVersion: "urgentry-v1",
				GroupingKey:     "grp-harness-1",
				Title:           "CheckoutError",
				Culprit:         "checkout.go in handler",
				Level:           "error",
				Status:          "unresolved",
				FirstSeen:       now,
				LastSeen:        now,
				TimesSeen:       1,
				LastEventID:     "evt-harness-1",
			},
			{
				ID:              "grp-harness-merged",
				ProjectID:       harnessProjectID,
				GroupingVersion: "urgentry-v1",
				GroupingKey:     "grp-harness-merged",
				Title:           "CheckoutError duplicate",
				Culprit:         "checkout.go in handler",
				Level:           "error",
				Status:          "unresolved",
				FirstSeen:       now,
				LastSeen:        now,
				TimesSeen:       1,
				LastEventID:     "evt-harness-merged",
			},
		} {
			cloned := item
			if err := groupStore.UpsertGroup(context.Background(), &cloned); err != nil {
				t.Fatalf("%s seed control issue %s: %v", h.name, item.ID, err)
			}
		}
	}
}

func insertHarnessQueryGroup(t *testing.T, db *sql.DB, id, title, culprit, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen)
		 VALUES (?, ?, 'urgentry-v1', ?, ?, ?, 'error', ?, ?, ?, 1)`,
		id, harnessProjectID, id, title, culprit, status, now, now,
	); err != nil {
		t.Fatalf("insert query group %s: %v", id, err)
	}
}

func insertHarnessQueryEvent(t *testing.T, db *sql.DB, eventID, groupID, title, release string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, level, title, message, platform, culprit, occurred_at, release, environment, tags_json, payload_json)
		 VALUES (?, ?, ?, ?, 'error', ?, 'boom', 'go', 'main.go', ?, ?, 'production', '{}', '{}')`,
		eventID+"-internal", harnessProjectID, eventID, groupID, title, now, release,
	); err != nil {
		t.Fatalf("insert query event %s: %v", eventID, err)
	}
}

func jsonRequest(t *testing.T, ts *httptest.Server, method, path, token string, body any) *http.Response {
	t.Helper()
	var payload io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		payload = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, ts.URL+path, payload)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func sessionFormRequest(t *testing.T, client *http.Client, target, sessionToken, csrf string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new form request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("form request %s: %v", target, err)
	}
	return resp
}

func sessionJSONRequest(t *testing.T, client *http.Client, method, target, sessionToken, csrf string, body any) *http.Response {
	t.Helper()
	var payload io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal session request: %v", err)
		}
		payload = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, target, payload)
	if err != nil {
		t.Fatalf("new session request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	return resp
}

func authenticatedGet(t *testing.T, client *http.Client, target, sessionToken string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "urgentry_session", Value: sessionToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", target, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return string(body)
}

func decodeJSONBody(t *testing.T, resp *http.Response, dest any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func stringValue(v any) string {
	if value, ok := v.(string); ok {
		return value
	}
	return ""
}
