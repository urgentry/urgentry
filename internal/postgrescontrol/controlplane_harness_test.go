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

	"urgentry/internal/alert"
	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/issue"
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/internal/web"
	"golang.org/x/crypto/bcrypt"
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
	return startControlPlaneHarness(t, "sqlite", db, db, control, authStore, authz, sessionToken, principal.CSRFToken)
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
	return startControlPlaneHarness(t, "postgres", queryDB, controlDB, control, authStore, authz, sessionToken, principal.CSRFToken)
}

func startControlPlaneHarness(t *testing.T, name string, queryDB, controlDB *sql.DB, control controlplane.Services, tokenManager auth.TokenManager, authz *auth.Authorizer, sessionToken, csrf string) *controlPlaneHarness {
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
		DB:               queryDB,
		Auth:             authz,
		Control:          control,
		TokenManager:     tokenManager,
		PrincipalShadows: sqlite.NewPrincipalShadowStore(queryDB),
		QueryGuard:       queryGuard,
		Operators:        operatorStore,
		OperatorAudits:   operatorAudits,
		Analytics:        analytics,
		Backfills:        backfills,
		Audits:           audits,
		NativeControl:    nativeControl,
		ReleaseHealth:    releaseHealth,
		DebugFiles:       debugFiles,
		Outcomes:         outcomes,
		Retention:        retention,
		ImportExport:     importExport,
		BlobStore:        blobStore,
		Queries:          queryService,
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
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
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
			copy := item
			if err := groupStore.UpsertGroup(context.Background(), &copy); err != nil {
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
