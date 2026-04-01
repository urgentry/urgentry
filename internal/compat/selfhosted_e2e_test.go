//go:build integration

package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/config"
	"urgentry/internal/controlplane"
	ghttp "urgentry/internal/http"
	"urgentry/internal/ingest"
	"urgentry/internal/issue"
	"urgentry/internal/nativesym"
	"urgentry/internal/pipeline"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/internal/web"
	"urgentry/pkg/dsn"
)

// newCompatServerWithDir is like newCompatServer but accepts an explicit data
// directory instead of using t.TempDir(). This allows two servers to share the
// same underlying database for persistence tests.
func newCompatServerWithDir(t *testing.T, dataDir string, opts compatOptions) *compatServer {
	t.Helper()

	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	projectKey, err := sqlite.EnsureDefaultKey(context.Background(), db)
	if err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}

	if opts.email == "" {
		opts.email = "admin@example.com"
	}
	if opts.displayName == "" {
		opts.displayName = "Admin"
	}
	if opts.pat == "" {
		opts.pat = "gpat_compat_admin_token"
	}

	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 opts.email,
		DisplayName:           opts.displayName,
		Password:              "test-password-123",
		PersonalAccessToken:   opts.pat,
	})
	if err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}

	blobStore := store.NewMemoryBlobStore()
	feedbackStore := sqlite.NewFeedbackStore(db)
	attachmentStore := sqlite.NewAttachmentStore(db, blobStore)
	outcomeStore := sqlite.NewOutcomeStore(db)
	traceStore := sqlite.NewTraceStore(db)
	sourceMapStore := sqlite.NewSourceMapStore(db, blobStore)
	proGuardStore := sqlite.NewProGuardStore(db, blobStore)
	releaseStore := sqlite.NewReleaseStore(db)
	processor := &issue.Processor{
		Events:     sqlite.NewEventStore(db),
		Groups:     sqlite.NewGroupStore(db),
		Blobs:      blobStore,
		Releases:   releaseStore,
		SourceMaps: &sourcemap.Resolver{Store: sourceMapStore},
		ProGuard:   &proguard.Resolver{Store: proGuardStore},
		Native:     nativesym.NewResolver(nativeCompatDebugLookup{store: sqlite.NewDebugFileStore(db, blobStore)}),
		Traces:     traceStore,
	}

	queueSize := opts.queueSize
	if queueSize <= 0 {
		queueSize = 100
	}
	jobStore := sqlite.NewJobStore(db)
	pipe := pipeline.NewDurable(processor, jobStore, queueSize, 1)
	nativeCrashes := sqlite.NewNativeCrashStore(db, blobStore, jobStore, queueSize)
	pipe.SetNativeJobProcessor(pipeline.NativeJobProcessorFunc(func(ctx context.Context, projectID string, payload []byte) error {
		return nativeCrashes.ProcessStackwalkJob(ctx, processor, projectID, payload)
	}))

	cfg := config.Config{
		Env:               "test",
		HTTPAddr:          ":0",
		SessionCookieName: "urgentry_session",
		CSRFCookieName:    "urgentry_csrf",
		IngestRateLimit:   opts.ingestRateLimit,
	}
	control := controlplane.SQLiteServices(db)
	operatorAudits := sqlite.NewOperatorAuditStore(db)
	queryGuard := sqlite.NewQueryGuardStore(db)
	queryService := telemetryquery.NewSQLiteService(db, blobStore)
	dashboards := sqlite.NewDashboardStore(db)
	snapshots := sqlite.NewAnalyticsSnapshotStore(db)
	reportSchedules := sqlite.NewAnalyticsReportScheduleStore(db)
	searches := sqlite.NewSearchStore(db)
	analytics := analyticsservice.Services{
		Dashboards:      dashboards,
		Snapshots:       snapshots,
		ReportSchedules: reportSchedules,
		Searches:        searches,
	}
	backfills := sqlite.NewBackfillStore(db)
	audits := sqlite.NewAuditStore(db)
	debugFiles := sqlite.NewDebugFileStore(db, blobStore)
	retention := sqlite.NewRetentionStore(db, blobStore)
	nativeControl := sqlite.NewNativeControlStore(db, blobStore, operatorAudits)
	importExport := sqlite.NewImportExportStore(db, attachmentStore, proGuardStore, sourceMapStore, blobStore)
	operatorStore := sqlite.NewOperatorStore(db, store.OperatorRuntime{Role: "test", Env: "test"}, sqlite.NewLifecycleStore(db), operatorAudits, func(context.Context) (int, error) {
		return pipe.Len(), nil
	})
	handler := ghttp.NewServer("all", cfg, ghttp.Deps{
		KeyStore:  keyStore,
		AuthStore: authStore,
		Pipeline:  pipe,
		DB:        db,
		Lifecycle: sqlite.NewLifecycleStore(db),
		Ingest: ingest.IngestDeps{
			Pipeline:        pipe,
			EventStore:      sqlite.NewEventStore(db),
			ReplayStore:     sqlite.NewReplayStore(db, blobStore),
			ReplayPolicies:  sqlite.NewReplayConfigStore(db),
			ProfileStore:    sqlite.NewProfileStore(db, blobStore),
			FeedbackStore:   feedbackStore,
			AttachmentStore: attachmentStore,
			BlobStore:       blobStore,
			DebugFiles:      debugFiles,
			NativeCrashes:   nativeCrashes,
			SessionStore:    sqlite.NewReleaseHealthStore(db),
			OutcomeStore:    outcomeStore,
			MonitorStore:    control.Monitors,
		},
		API: api.Dependencies{
			DB:               db,
			Control:          control,
			PrincipalShadows: sqlite.NewPrincipalShadowStore(db),
			QueryGuard:       queryGuard,
			Operators:        operatorStore,
			OperatorAudits:   operatorAudits,
			Analytics:        analytics,
			Backfills:        backfills,
			Audits:           audits,
			NativeControl:    nativeControl,
			ReleaseHealth:    sqlite.NewReleaseHealthStore(db),
			DebugFiles:       debugFiles,
			Outcomes:         outcomeStore,
			Retention:        retention,
			ImportExport:     importExport,
			Attachments:      attachmentStore,
			ProGuardStore:    proGuardStore,
			SourceMapStore:   sourceMapStore,
			BlobStore:        blobStore,
			Queries:          queryService,
		},
		Web: web.Dependencies{
			WebStore:       sqlite.NewWebStore(db),
			Replays:        queryService,
			Queries:        queryService,
			DB:             db,
			BlobStore:      blobStore,
			DataDir:        dataDir,
			Control:        control,
			Operators:      operatorStore,
			OperatorAudits: operatorAudits,
			QueryGuard:     queryGuard,
			NativeControl:  nativeControl,
			Analytics:      analytics,
		},
	})

	var cancel context.CancelFunc
	var done chan struct{}
	if opts.startWorkers {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		backfillController := pipeline.NewBackfillController(sqlite.NewBackfillStore(db), sqlite.NewDebugFileStore(db, blobStore), "compat-native-backfill")
		pipe.Start(workerCtx)
		done = make(chan struct{})
		go func() {
			defer close(done)
			backfillController.Run(workerCtx)
		}()
		cancel = workerCancel
	}

	return &compatServer{
		server:     httptest.NewServer(handler),
		db:         db,
		pipe:       pipe,
		projectKey: projectKey,
		pat:        bootstrap.PAT,
		email:      bootstrap.Email,
		password:   bootstrap.Password,
		blobStore:  blobStore,
		sourceMaps: sourceMapStore,
		proGuard:   proGuardStore,
		cancel:     cancel,
		done:       done,
	}
}

// ---------------------------------------------------------------------------
// 1. TestSelfHostedBootstrap
// ---------------------------------------------------------------------------

func TestSelfHostedBootstrap(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Verify admin user was created by checking the users table.
	var userCount int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount == 0 {
		t.Fatal("expected at least one user after bootstrap")
	}

	// Verify admin email exists.
	var email string
	if err := srv.db.QueryRow(`SELECT email FROM users LIMIT 1`).Scan(&email); err != nil {
		t.Fatalf("select admin email: %v", err)
	}
	if email != "admin@example.com" {
		t.Fatalf("expected admin email %q, got %q", "admin@example.com", email)
	}

	// Verify default organization exists.
	resp := apiGet(t, srv.server.URL+"/api/0/organizations/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var orgs []map[string]any
	readJSON(t, resp, &orgs)
	if len(orgs) == 0 {
		t.Fatal("expected at least one organization after bootstrap")
	}
	foundOrg := false
	for _, org := range orgs {
		if org["slug"] == "urgentry-org" {
			foundOrg = true
		}
	}
	if !foundOrg {
		t.Fatal("expected default organization slug 'urgentry-org'")
	}

	// Verify default project exists.
	resp = apiGet(t, srv.server.URL+"/api/0/projects/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var projects []map[string]any
	readJSON(t, resp, &projects)
	if len(projects) == 0 {
		t.Fatal("expected at least one project after bootstrap")
	}
	foundProj := false
	for _, proj := range projects {
		if proj["slug"] == "default" {
			foundProj = true
		}
	}
	if !foundProj {
		t.Fatal("expected default project slug 'default'")
	}

	// Verify PAT auth works.
	resp = apiGet(t, srv.server.URL+"/api/0/organizations/urgentry-org/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// 2. TestSelfHostedDSNGeneration
// ---------------------------------------------------------------------------

func TestSelfHostedDSNGeneration(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Fetch project keys via API.
	resp := apiGet(t, srv.server.URL+"/api/0/projects/urgentry-org/default/keys/", srv.pat)
	requireStatus(t, resp, http.StatusOK)

	var keys []dsnKeyResponse
	readJSON(t, resp, &keys)
	if len(keys) == 0 {
		t.Fatal("expected at least one project key")
	}

	key := keys[0]
	if !key.IsActive {
		t.Fatal("expected key to be active")
	}
	if key.DSN.Public == "" {
		t.Fatal("public DSN is empty")
	}

	// Parse the DSN and verify format: scheme://publickey@host/project_id
	parsed, err := dsn.Parse(key.DSN.Public)
	if err != nil {
		t.Fatalf("dsn.Parse(%q): %v", key.DSN.Public, err)
	}
	if parsed.PublicKey == "" {
		t.Fatal("DSN public key is empty")
	}
	if parsed.ProjectID == "" {
		t.Fatal("DSN project ID is empty")
	}
	if parsed.Scheme != "http" {
		t.Fatalf("DSN scheme = %q, want http (test server)", parsed.Scheme)
	}
	if parsed.PublicKey != key.Public {
		t.Fatalf("DSN public key = %q, want %q", parsed.PublicKey, key.Public)
	}

	// Verify the DSN host points to a valid location (the test server).
	if parsed.Host == "" {
		t.Fatal("DSN host is empty")
	}
}

// ---------------------------------------------------------------------------
// 3. TestSelfHostedHealthEndpoint
// ---------------------------------------------------------------------------

func TestSelfHostedHealthEndpoint(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	resp, err := http.Get(srv.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("decode health response: %v\nbody: %s", err, body)
	}

	// Verify required fields.
	if status, ok := health["status"].(string); !ok || status != "ok" {
		t.Fatalf("health status = %v, want 'ok'", health["status"])
	}
	if role, ok := health["role"].(string); !ok || role == "" {
		t.Fatalf("health role is empty or missing: %v", health["role"])
	}
	if env, ok := health["env"].(string); !ok || env == "" {
		t.Fatalf("health env is empty or missing: %v", health["env"])
	}
	if _, ok := health["now"].(string); !ok {
		t.Fatal("health response missing 'now' timestamp")
	}
}

// ---------------------------------------------------------------------------
// 4. TestSelfHostedVersionEndpoint
// ---------------------------------------------------------------------------

func TestSelfHostedVersionEndpoint(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// The /healthz endpoint includes version info in the response.
	resp, err := http.Get(srv.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("decode health response: %v\nbody: %s", err, body)
	}

	// Verify the response contains version/info fields.
	// The 'version' field has omitempty so it may be absent in test mode;
	// instead we verify the fields that always identify the running instance.
	if role, ok := health["role"].(string); !ok || role == "" {
		t.Fatal("health response missing 'role' field")
	}
	if env, ok := health["env"].(string); !ok || env == "" {
		t.Fatal("health response missing 'env' field")
	}
	if status, ok := health["status"].(string); !ok || status != "ok" {
		t.Fatalf("health status = %v, want 'ok'", health["status"])
	}
	// The 'now' field serves as a server timestamp, verifying the server is alive.
	nowStr, ok := health["now"].(string)
	if !ok || nowStr == "" {
		t.Fatal("health response missing or empty 'now' field")
	}
	// Verify the timestamp is parseable and recent.
	ts, err := time.Parse(time.RFC3339Nano, nowStr)
	if err != nil {
		t.Fatalf("parse 'now' timestamp: %v", err)
	}
	if time.Since(ts) > 30*time.Second {
		t.Fatalf("'now' timestamp is too old: %v", ts)
	}
}

// ---------------------------------------------------------------------------
// 5. TestSelfHostedStaticAssets
// ---------------------------------------------------------------------------

func TestSelfHostedStaticAssets(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	t.Run("CSS", func(t *testing.T) {
		resp, err := http.Get(srv.server.URL + "/static/style.css")
		if err != nil {
			t.Fatalf("GET /static/style.css: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /static/style.css status = %d, want 200", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(body) == 0 {
			t.Fatal("static/style.css is empty")
		}
		// Verify it looks like CSS content.
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "css") {
			t.Fatalf("Content-Type = %q, want something containing 'css'", ct)
		}
	})

	t.Run("JS", func(t *testing.T) {
		resp, err := http.Get(srv.server.URL + "/static/feedback-widget.js")
		if err != nil {
			t.Fatalf("GET /static/feedback-widget.js: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /static/feedback-widget.js status = %d, want 200", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(body) == 0 {
			t.Fatal("static/feedback-widget.js is empty")
		}
	})

	t.Run("NotFoundForMissing", func(t *testing.T) {
		resp, err := http.Get(srv.server.URL + "/static/nonexistent.xyz")
		if err != nil {
			t.Fatalf("GET /static/nonexistent.xyz: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /static/nonexistent.xyz status = %d, want 404", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. TestSelfHostedProjectCreation
// ---------------------------------------------------------------------------

func TestSelfHostedProjectCreation(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	orgSlug := "urgentry-org"
	teamSlug := "selfhosted-team"

	// Create a team first.
	resp := apiPost(t, srv.server.URL+"/api/0/organizations/"+orgSlug+"/teams/", srv.pat, map[string]string{
		"slug": teamSlug,
		"name": "Self-Hosted Team",
	})
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Create a second project.
	resp = apiPost(t, srv.server.URL+"/api/0/teams/"+orgSlug+"/"+teamSlug+"/projects/", srv.pat, map[string]string{
		"name":     "Second Project",
		"slug":     "second-project",
		"platform": "python",
	})
	requireStatus(t, resp, http.StatusCreated)
	var created map[string]any
	readJSON(t, resp, &created)
	if created["slug"] != "second-project" {
		t.Fatalf("expected slug %q, got %q", "second-project", created["slug"])
	}

	// Verify the project appears in the list.
	resp = apiGet(t, srv.server.URL+"/api/0/projects/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var projects []map[string]any
	readJSON(t, resp, &projects)

	found := false
	for _, p := range projects {
		if p["slug"] == "second-project" {
			found = true
		}
	}
	if !found {
		t.Fatal("second project not found in project list")
	}

	// Create a key for the new project.
	resp = apiPost(t, srv.server.URL+"/api/0/projects/"+orgSlug+"/second-project/keys/", srv.pat, map[string]string{
		"label": "Second Project Key",
	})
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Verify project has DSN keys.
	resp = apiGet(t, srv.server.URL+"/api/0/projects/"+orgSlug+"/second-project/keys/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var keys []map[string]any
	readJSON(t, resp, &keys)
	if len(keys) == 0 {
		t.Fatal("expected at least one key for the new project")
	}
	dsnObj, ok := keys[0]["dsn"].(map[string]any)
	if !ok {
		t.Fatal("expected dsn object in key")
	}
	if dsnObj["public"] == nil || dsnObj["public"] == "" {
		t.Fatal("expected public DSN in new project key")
	}
}

// ---------------------------------------------------------------------------
// 7. TestSelfHostedMultiUserSetup
// ---------------------------------------------------------------------------

func TestSelfHostedMultiUserSetup(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Create a second user directly in the DB (the same way bootstrap does).
	now := time.Now().UTC().Format(time.RFC3339)
	userID := "selfhosted-user-2"
	_, err := srv.db.Exec(
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		userID, "user2@example.com", "User Two", now, now,
	)
	if err != nil {
		t.Fatalf("insert second user: %v", err)
	}

	// Add the second user to the default organization.
	_, err = srv.db.Exec(
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, 'default-org', ?, 'member', ?)`,
		"selfhosted-membership-2", userID, now,
	)
	if err != nil {
		t.Fatalf("insert second user membership: %v", err)
	}

	// Create a PAT for the second user via the AuthStore.
	authStore := sqlite.NewAuthStore(srv.db)
	user2PAT, err := authStore.CreatePersonalAccessToken(
		context.Background(), userID, "User 2 Token",
		[]string{auth.ScopeOrgRead, auth.ScopeProjectRead}, nil, "gpat_user2_test_token",
	)
	if err != nil {
		t.Fatalf("create PAT for user2: %v", err)
	}

	// Verify the second user can authenticate via their PAT.
	resp := apiGet(t, srv.server.URL+"/api/0/projects/", user2PAT)
	requireStatus(t, resp, http.StatusOK)
	var projects []map[string]any
	readJSON(t, resp, &projects)
	if len(projects) == 0 {
		t.Fatal("user2 should see at least one project")
	}

	// Verify the second user can access the organization.
	resp = apiGet(t, srv.server.URL+"/api/0/organizations/urgentry-org/", user2PAT)
	requireStatus(t, resp, http.StatusOK)
	var org map[string]any
	readJSON(t, resp, &org)
	if org["slug"] != "urgentry-org" {
		t.Fatalf("expected org slug %q, got %q", "urgentry-org", org["slug"])
	}

	// Verify the original admin user still works.
	resp = apiGet(t, srv.server.URL+"/api/0/projects/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Verify both users show up in the members list.
	resp = apiGet(t, srv.server.URL+"/api/0/organizations/urgentry-org/members/", srv.pat)
	requireStatus(t, resp, http.StatusOK)
	var members []map[string]any
	readJSON(t, resp, &members)
	if len(members) < 2 {
		t.Fatalf("expected at least 2 members, got %d", len(members))
	}
}

// ---------------------------------------------------------------------------
// 8. TestSelfHostedDataPersistence
// ---------------------------------------------------------------------------

func TestSelfHostedDataPersistence(t *testing.T) {
	// Use a shared tempdir so two server instances can share the same DB.
	sharedDir, err := os.MkdirTemp("", "selfhosted-persist-*")
	if err != nil {
		t.Fatalf("create shared temp dir: %v", err)
	}
	defer os.RemoveAll(sharedDir)

	eventID := "e2e00000000000000000000persist01"

	// --- Phase 1: Boot first server, ingest an event. ---
	srv1 := newCompatServerWithDir(t, sharedDir, compatOptions{startWorkers: true, ingestRateLimit: 60})

	payload := fmt.Sprintf(`{
		"event_id": "%s",
		"message": "persistence test event",
		"level": "error",
		"platform": "go"
	}`, eventID)

	resp := doRequest(t, http.MethodPost, srv1.server.URL+"/api/default-project/store/",
		strings.NewReader(payload), map[string]string{
			"Content-Type":  "application/json",
			"X-Sentry-Auth": srv1.sentryAuthHeader(),
		})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST store status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	// Wait for the event to be persisted.
	waitForEvent(t, srv1.db, eventID)

	// Verify the event exists in the first server's API.
	apiResp := apiGet(t, srv1.server.URL+"/api/0/projects/urgentry-org/default/events/"+eventID+"/", srv1.pat)
	requireStatus(t, apiResp, http.StatusOK)
	var evt1 map[string]any
	readJSON(t, apiResp, &evt1)
	if got, ok := evt1["eventID"].(string); !ok || got != eventID {
		t.Fatalf("event1 eventID = %v, want %s", evt1["eventID"], eventID)
	}

	// Shut down the first server completely.
	srv1.pipe.Stop()
	srv1.server.Close()
	if srv1.cancel != nil {
		srv1.cancel()
	}
	if srv1.done != nil {
		<-srv1.done
	}
	srv1.db.Close()

	// --- Phase 2: Boot second server against the same data directory. ---
	srv2 := newCompatServerWithDir(t, sharedDir, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer func() {
		srv2.pipe.Stop()
		srv2.server.Close()
		if srv2.cancel != nil {
			srv2.cancel()
		}
		if srv2.done != nil {
			<-srv2.done
		}
		srv2.db.Close()
	}()

	// EnsureBootstrapAccess is idempotent: when users already exist it returns
	// an empty result. Use the known PAT that was persisted by the first server.
	pat := "gpat_compat_admin_token"

	// Verify the event still exists via the second server.
	var count int
	err = srv2.db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = ?`, eventID).Scan(&count)
	if err != nil {
		t.Fatalf("query events on srv2: %v", err)
	}
	if count == 0 {
		t.Fatal("event not found in second server's database - data was not persisted")
	}

	// Verify via API as well.
	apiResp2 := apiGet(t, srv2.server.URL+"/api/0/projects/urgentry-org/default/events/"+eventID+"/", pat)
	requireStatus(t, apiResp2, http.StatusOK)
	var evt2 map[string]any
	readJSON(t, apiResp2, &evt2)
	if got, ok := evt2["eventID"].(string); !ok || got != eventID {
		t.Fatalf("event2 eventID = %v, want %s", evt2["eventID"], eventID)
	}

	// Verify other bootstrap data survived the restart.
	orgResp := apiGet(t, srv2.server.URL+"/api/0/organizations/", pat)
	requireStatus(t, orgResp, http.StatusOK)
	var orgs []map[string]any
	readJSON(t, orgResp, &orgs)
	if len(orgs) == 0 {
		t.Fatal("no organizations found after restart")
	}

	projResp := apiGet(t, srv2.server.URL+"/api/0/projects/", pat)
	requireStatus(t, projResp, http.StatusOK)
	var projects []map[string]any
	readJSON(t, projResp, &projects)
	if len(projects) == 0 {
		t.Fatal("no projects found after restart")
	}
}
