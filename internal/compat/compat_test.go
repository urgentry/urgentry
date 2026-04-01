//go:build integration

package compat

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
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

type compatOptions struct {
	startWorkers    bool
	queueSize       int
	ingestRateLimit int
	email           string
	displayName     string
	pat             string
}

type compatServer struct {
	server     *httptest.Server
	db         *sql.DB
	pipe       *pipeline.Pipeline
	projectKey string
	pat        string
	email      string
	password   string
	blobStore  *store.MemoryBlobStore
	sourceMaps *sqlite.SourceMapStore
	proGuard   *sqlite.ProGuardStore
	cancel     context.CancelFunc
	done       chan struct{}
}

type nativeCompatDebugLookup struct {
	store *sqlite.DebugFileStore
}

func (n nativeCompatDebugLookup) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*nativesym.File, []byte, error) {
	if n.store == nil {
		return nil, nil, nil
	}
	file, body, err := n.store.LookupByDebugID(ctx, projectID, releaseVersion, kind, debugID)
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}

func (n nativeCompatDebugLookup) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*nativesym.File, []byte, error) {
	if n.store == nil {
		return nil, nil, nil
	}
	file, body, err := n.store.LookupByCodeID(ctx, projectID, releaseVersion, kind, codeID)
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}

func newCompatServer(t *testing.T, opts compatOptions) *compatServer {
	t.Helper()

	dataDir := t.TempDir()
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
			MetricBuckets:   sqlite.NewMetricBucketStore(db),
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
		t.Cleanup(func() {
			workerCancel()
			<-done
		})
	}

	t.Cleanup(func() {
		pipe.Stop()
		db.Close()
	})

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

func (s *compatServer) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	s.server.Close()
}

func (s *compatServer) sentryAuthHeader() string {
	return "Sentry sentry_key=" + s.projectKey + ",sentry_version=7,sentry_client=compat/1.0"
}

func fixtureBytes(t *testing.T, parts ...string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	pathParts := []string{filepath.Dir(file), "..", "..", "..", "..", "eval", "fixtures"}
	pathParts = append(pathParts, parts...)
	path := filepath.Join(pathParts...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func writeHarnessResult(t *testing.T, dimension, name string, score float64, detail string) {
	t.Helper()

	resultsDir := strings.TrimSpace(os.Getenv("RESULTS_DIR"))
	if resultsDir == "" {
		return
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("mkdir results dir: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"name":   dimension + "." + name,
		"score":  score,
		"detail": detail,
	})
	if err != nil {
		t.Fatalf("marshal harness result: %v", err)
	}
	path := filepath.Join(resultsDir, dimension+"."+name+".json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write harness result: %v", err)
	}
}

func runHarnessCheck(t *testing.T, dimension, name string, fn func(t *testing.T)) bool {
	t.Helper()
	ok := t.Run(name, fn)
	if ok {
		writeHarnessResult(t, dimension, name, 1.0, "compatibility check passes")
		return true
	}
	writeHarnessResult(t, dimension, name, 0.0, "compatibility check failed")
	return false
}

func checkDSNParsing(t *testing.T) {
	t.Helper()

	tests := []struct {
		raw        string
		projectID  string
		publicKey  string
		secretKey  string
		expectFail bool
	}{
		{raw: "https://abc123@o1.ingest.example.com/42", projectID: "42", publicKey: "abc123"},
		{raw: "https://abc123:def456@example.com/99", projectID: "99", publicKey: "abc123", secretKey: "def456"},
		{raw: "https://abc123@example.com/sentry/42", projectID: "42", publicKey: "abc123"},
		{raw: "https://abc123@example.com/42/", projectID: "42", publicKey: "abc123"},
		{raw: "https://example.com/42", expectFail: true},
		{raw: "https://abc123@example.com/", expectFail: true},
		{raw: "", expectFail: true},
	}

	for _, test := range tests {
		parsed, err := dsn.Parse(test.raw)
		if test.expectFail {
			if err == nil {
				t.Fatalf("expected %q to fail", test.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parse %q: %v", test.raw, err)
		}
		if parsed.ProjectID != test.projectID || parsed.PublicKey != test.publicKey || parsed.SecretKey != test.secretKey {
			t.Fatalf("unexpected parse for %q: %+v", test.raw, parsed)
		}
	}
}

func doRequest(t *testing.T, method, rawURL string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	return resp
}

func apiRequest(t *testing.T, method, rawURL, pat string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	headers := map[string]string{
		"Authorization": "Bearer " + pat,
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	return doRequest(t, method, rawURL, body, headers)
}

func loginClient(t *testing.T, srv *compatServer) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}

	form := url.Values{
		"email":    {srv.email},
		"password": {srv.password},
		"next":     {"/feedback/"},
	}
	resp, err := client.PostForm(srv.server.URL+"/login/", form)
	if err != nil {
		t.Fatalf("POST /login/: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login/ status = %d, want 303", resp.StatusCode)
	}
	return client
}

func waitForEvent(t *testing.T, db *sql.DB, eventID string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = ?`, eventID).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("event %s was not persisted", eventID)
}

func waitForProjectEventCount(t *testing.T, db *sql.DB, projectID string, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ?`, projectID).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("project %s did not reach %d stored events", projectID, want)
}

func waitForTransactionCount(t *testing.T, db *sql.DB, projectID string, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE project_id = ?`, projectID).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	var (
		jobCount   int
		eventCount int
		status     sql.NullString
		lastError  sql.NullString
	)
	_ = db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ?`, projectID).Scan(&eventCount)
	_ = db.QueryRow(`SELECT status, COALESCE(last_error, '') FROM jobs ORDER BY created_at DESC LIMIT 1`).Scan(&status, &lastError)
	t.Fatalf("transactions were not persisted (project_id=%s jobs=%d events=%d last_status=%q last_error=%q)", projectID, jobCount, eventCount, status.String, lastError.String)
}

func waitForTrace(t *testing.T, db *sql.DB, projectID, traceID string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE project_id = ? AND trace_id = ?`, projectID, traceID).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("trace %s was not persisted", traceID)
}

func createRelease(t *testing.T, srv *compatServer, version string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		t.Fatalf("marshal release body: %v", err)
	}
	resp := apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/organizations/urgentry-org/releases/", srv.pat, bytes.NewReader(body), "application/json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create release status = %d, body=%s", resp.StatusCode, b)
	}
}

func uploadSourceMap(t *testing.T, srv *compatServer, release, name string, data []byte) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(name))
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write source map file: %v", err)
	}
	if err := writer.WriteField("name", name); err != nil {
		t.Fatalf("WriteField name: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	resp := apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/projects/urgentry-org/default/releases/"+release+"/files/", srv.pat, &body, writer.FormDataContentType())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("source map upload status = %d, body=%s", resp.StatusCode, b)
	}
}

func uploadProGuard(t *testing.T, srv *compatServer, release, uuid string, data []byte) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "mapping.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write proguard file: %v", err)
	}
	if err := writer.WriteField("uuid", uuid); err != nil {
		t.Fatalf("WriteField uuid: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	resp := apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/projects/urgentry-org/default/releases/"+release+"/proguard/", srv.pat, &body, writer.FormDataContentType())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("proguard upload status = %d, body=%s", resp.StatusCode, b)
	}
}

func TestLegacyStoreAccept(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("store status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var decoded map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode store response: %v", err)
	}
	if decoded["id"] == "" {
		t.Fatal("store response missing id")
	}
	waitForEvent(t, srv.db, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
}

func TestEnvelopeAccept(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "envelopes", "single_error.envelope")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("envelope status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	waitForEvent(t, srv.db, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")
}

func TestAuthReject(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{name: "missing auth", headers: map[string]string{"Content-Type": "application/json"}},
		{name: "invalid project key", headers: map[string]string{"Content-Type": "application/json", "X-Sentry-Auth": "Sentry sentry_key=bad,sentry_version=7"}},
		{name: "bearer token", headers: map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + srv.pat}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), test.headers)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestCompressedPayload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	tests := []struct {
		name     string
		encoding string
		body     func([]byte) []byte
	}{
		{
			name:     "gzip",
			encoding: "gzip",
			body: func(raw []byte) []byte {
				var buf bytes.Buffer
				writer := gzip.NewWriter(&buf)
				_, _ = writer.Write(raw)
				_ = writer.Close()
				return buf.Bytes()
			},
		},
		{
			name:     "deflate",
			encoding: "deflate",
			body: func(raw []byte) []byte {
				var buf bytes.Buffer
				writer, err := flate.NewWriter(&buf, flate.DefaultCompression)
				if err != nil {
					t.Fatalf("flate.NewWriter: %v", err)
				}
				_, _ = writer.Write(raw)
				_ = writer.Close()
				return buf.Bytes()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(test.body(payload)), map[string]string{
				"Content-Type":     "application/json",
				"Content-Encoding": test.encoding,
				"X-Sentry-Auth":    srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
			}
		})
	}
}

func TestResponseCodes(t *testing.T) {
	malformed := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer malformed.close()

	resp := doRequest(t, http.MethodPost, malformed.server.URL+"/api/default-project/store/", strings.NewReader("{bad json"), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": malformed.sentryAuthHeader(),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, want 400", resp.StatusCode)
	}
	if resp.Header.Get("X-Sentry-Error") == "" {
		t.Fatal("missing X-Sentry-Error header")
	}
	resp.Body.Close()

	oversized := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer oversized.close()

	resp = doRequest(t, http.MethodPost, oversized.server.URL+"/api/default-project/store/", bytes.NewReader(bytes.Repeat([]byte("a"), (1<<20)+1)), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": oversized.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", resp.StatusCode)
	}

	queueFull := newCompatServer(t, compatOptions{startWorkers: false, queueSize: 1, ingestRateLimit: 60})
	defer queueFull.close()
	payload := fixtureBytes(t, "store", "basic_error.json")
	for i := 0; i < 2; i++ {
		resp = doRequest(t, http.MethodPost, queueFull.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
			"Content-Type":  "application/json",
			"X-Sentry-Auth": queueFull.sentryAuthHeader(),
		})
		if i == 0 {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("first enqueue status = %d, want 200", resp.StatusCode)
			}
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("queue full status = %d, want 503", resp.StatusCode)
		}
	}
}

func TestRateLimitHeaders(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 1})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	first := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	second := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	if second.Header.Get("X-Sentry-Rate-Limits") == "" {
		t.Fatal("missing X-Sentry-Rate-Limits header")
	}
}

func TestCORSBrowserIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodOptions, srv.server.URL+"/api/default-project/store/", nil, map[string]string{
		"Origin":                        "https://browser.example",
		"Access-Control-Request-Method": "POST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("missing Access-Control-Allow-Origin header")
	}
}

func TestClientReportAccounting(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(fixtureBytes(t, "envelopes", "with_client_report.envelope")), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope status = %d, want 200", resp.StatusCode)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/outcomes/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outcomes status = %d, want 200", resp.StatusCode)
	}
	var items []struct {
		Category string `json:"category"`
		Quantity int    `json:"quantity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode outcomes: %v", err)
	}
	if len(items) != 1 || items[0].Category != "error" || items[0].Quantity != 5 {
		t.Fatalf("unexpected outcomes: %+v", items)
	}
}

func TestMonitorCheckInIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(fixtureBytes(t, "envelopes", "check_in.envelope")), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope status = %d, want 200", resp.StatusCode)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/monitors/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitors status = %d, want 200", resp.StatusCode)
	}
	var monitors []struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode monitors: %v", err)
	}
	_ = resp.Body.Close()
	if len(monitors) != 1 || monitors[0].Slug != "nightly-import" {
		t.Fatalf("unexpected monitors: %+v", monitors)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/monitors/nightly-import/check-ins/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check-ins status = %d, want 200", resp.StatusCode)
	}
	var checkIns []struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&checkIns); err != nil {
		t.Fatalf("decode check-ins: %v", err)
	}
	if len(checkIns) != 1 || checkIns[0].Status != "ok" {
		t.Fatalf("unexpected check-ins: %+v", checkIns)
	}
}

func TestSecurityReportIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := []byte(`{"csp-report":{"document-uri":"https://app.example.com/checkout","effective-directive":"script-src-elem","blocked-uri":"https://cdn.bad.test/app.js","disposition":"enforce"}}`)
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/security/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/csp-report",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("security report status = %d, want 200", resp.StatusCode)
	}
	waitForProjectEventCount(t, srv.db, "default-project", 1)

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/events/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp.StatusCode)
	}
	var items []struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	_ = resp.Body.Close()
	if len(items) == 0 || !strings.Contains(items[0].Title, "CSP") {
		t.Fatalf("unexpected security events: %+v", items)
	}
}

func TestTransactionEnvelopeIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	eventID := "abcdabcdabcdabcdabcdabcdabcdabcd"
	payload := fmt.Sprintf("{\"type\":\"transaction\",\"event_id\":\"%s\",\"platform\":\"javascript\",\"transaction\":\"GET /checkout\",\"start_timestamp\":\"2026-03-27T12:00:00Z\",\"timestamp\":\"2026-03-27T12:00:01Z\",\"contexts\":{\"trace\":{\"trace_id\":\"trace-compat-1\",\"span_id\":\"root-compat-1\",\"op\":\"http.server\",\"status\":\"ok\"}}}", eventID)
	body := []byte(fmt.Sprintf(
		"{\"event_id\":\"%s\",\"dsn\":\"https://abc123@o1.ingest.example.com/1\"}\n"+
			"{\"type\":\"transaction\",\"length\":%d}\n%s",
		eventID,
		len(payload),
		payload,
	))
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transaction envelope status = %d, want 200", resp.StatusCode)
	}
	waitForTransactionCount(t, srv.db, "default-project", 1)

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/transactions/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions status = %d, want 200", resp.StatusCode)
	}
	var items []struct {
		EventID string `json:"eventId"`
		TraceID string `json:"traceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode transactions: %v", err)
	}
	if len(items) != 1 || items[0].TraceID != "trace-compat-1" {
		t.Fatalf("unexpected transactions: %+v", items)
	}
}

func TestOTLPTraceIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	traceID := "0102030405060708090a0b0c0d0e0f10"
	rootID := "1111111111111111"
	childID := "2222222222222222"
	persistedTraceID := traceID
	body := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"traceId":"` + traceID + `","spanId":"` + rootID + `","name":"GET /checkout","kind":2,"startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}}],"status":{"code":1}},{"traceId":"` + traceID + `","spanId":"` + childID + `","parentSpanId":"` + rootID + `","name":"SELECT orders","kind":3,"startTimeUnixNano":"1743076800100000000","endTimeUnixNano":"1743076800200000000","attributes":[{"key":"db.system","value":{"stringValue":"sqlite"}}],"status":{"code":1}}]}]}]}`)

	for i := 0; i < 2; i++ {
		resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/traces/", bytes.NewReader(body), map[string]string{
			"Content-Type":  "application/json",
			"X-Sentry-Auth": srv.sentryAuthHeader(),
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("otlp status = %d, want 200", resp.StatusCode)
		}
	}
	waitForTrace(t, srv.db, "default-project", persistedTraceID)

	resp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/transactions/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions status = %d, want 200", resp.StatusCode)
	}
	var items []struct {
		TraceID string `json:"traceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode transactions: %v", err)
	}
	_ = resp.Body.Close()
	if len(items) != 1 || items[0].TraceID == "" {
		t.Fatalf("unexpected transactions: %+v", items)
	}
}

func TestOTLPLogIngest(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeLogs":[{"scope":{"name":"checkout.logger"},"logRecords":[{"timeUnixNano":"1743076800000000000","severityText":"INFO","body":{"stringValue":"cache miss"}}]}]}]}`)
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/logs/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("otlp log status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := srv.db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = 'default-project' AND event_type = 'log'`).Scan(&count); err != nil {
			t.Fatalf("count log events: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("log event was not persisted")
}

func TestProtocolHarness(t *testing.T) {
	checks := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{name: "dsn_parsing", fn: checkDSNParsing},
		{name: "legacy_store_accept", fn: TestLegacyStoreAccept},
		{name: "envelope_accept", fn: TestEnvelopeAccept},
		{name: "auth_reject", fn: TestAuthReject},
		{name: "compressed_payload", fn: TestCompressedPayload},
		{name: "response_codes", fn: TestResponseCodes},
		{name: "rate_limit_headers", fn: TestRateLimitHeaders},
		{name: "cors_browser_ingest", fn: TestCORSBrowserIngest},
		{name: "client_report_accounting", fn: TestClientReportAccounting},
		{name: "monitor_check_in", fn: TestMonitorCheckInIngest},
		{name: "transaction_envelope", fn: TestTransactionEnvelopeIngest},
		{name: "native_minidump_apple", fn: TestNativeMinidumpAppleCompat},
		{name: "native_minidump_linux", fn: TestNativeMinidumpLinuxCompat},
		{name: "native_minidump_fallback", fn: TestNativeMinidumpFallbackCompat},
		{name: "otlp_traces", fn: TestOTLPTraceIngest},
		{name: "otlp_logs", fn: TestOTLPLogIngest},
		{name: "search_language", fn: TestSearchLanguageCompatibility},
		{name: "security_report_ingest", fn: TestSecurityReportIngest},
		{name: "negative_payloads", fn: TestNegativePayloads},
	}

	failed := false
	for _, check := range checks {
		if !runHarnessCheck(t, "protocol", check.name, check.fn) {
			failed = true
		}
	}
	if !runScoredHarnessCheck(t, "protocol", "sdk_matrix_core", 1.0, checkSDKMatrixCore) {
		failed = true
	}
	if !runScoredHarnessCheck(t, "protocol", "sdk_matrix_extended", 0.85, checkSDKMatrixExtended) {
		failed = true
	}
	if failed {
		t.FailNow()
	}
}

func TestMigrationHarness(t *testing.T) {
	checks := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{name: "dsn_swap", fn: TestDSNSwap},
		{name: "project_import", fn: TestProjectImport},
		{name: "release_import", fn: TestReleaseImport},
		{name: "source_map_upload", fn: TestSourceMapUpload},
		{name: "proguard_upload", fn: TestProguardUpload},
		{name: "attachment_roundtrip", fn: TestAttachmentRoundtrip},
		{name: "attachment_standalone_upload", fn: TestStandaloneAttachmentUpload},
		{name: "native_reprocess_roundtrip", fn: TestNativeReprocessRoundtrip},
		{name: "feedback_roundtrip", fn: TestFeedbackRoundtrip},
	}

	failed := false
	for _, check := range checks {
		if !runHarnessCheck(t, "migration", check.name, check.fn) {
			failed = true
		}
	}
	if failed {
		t.FailNow()
	}
}

func TestNegativePayloads(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	tests := []struct {
		name   string
		body   []byte
		status int
	}{
		{name: "malformed json", body: fixtureBytes(t, "negative", "malformed_json.json"), status: http.StatusBadRequest},
		{name: "binary garbage", body: fixtureBytes(t, "negative", "binary_garbage.bin"), status: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(test.body), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			resp.Body.Close()
			if resp.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, test.status)
			}
		})
	}
}

func TestDSNSwap(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "store", "basic_error.json")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/?sentry_key="+srv.projectKey, bytes.NewReader(payload), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("dsn swap status = %d, want 200, body=%s", resp.StatusCode, body)
	}
}

func TestProjectImport(t *testing.T) {
	source := newCompatServer(t, compatOptions{
		startWorkers: true,
		email:        "source-owner@example.com",
		displayName:  "Source Owner",
		pat:          "gpat_source_project_import",
	})
	defer source.close()

	resp := apiRequest(t, http.MethodGet, source.server.URL+"/api/0/organizations/urgentry-org/export/", source.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	resp.Body.Close()

	target := newCompatServer(t, compatOptions{
		startWorkers: true,
		email:        "importer@example.com",
		displayName:  "Importer",
		pat:          "gpat_target_project_import",
	})
	defer target.close()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}
	resp = apiRequest(t, http.MethodPost, target.server.URL+"/api/0/organizations/urgentry-org/import/", target.pat, bytes.NewReader(body), "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, want 200", resp.StatusCode)
	}

	resp = apiRequest(t, http.MethodGet, target.server.URL+"/api/0/projects/urgentry-org/default/", target.pat, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("project lookup status = %d, want 200", resp.StatusCode)
	}
}

func TestReleaseImport(t *testing.T) {
	source := newCompatServer(t, compatOptions{
		startWorkers: true,
		email:        "source-release@example.com",
		displayName:  "Source Release",
		pat:          "gpat_source_release_import",
	})
	defer source.close()
	createRelease(t, source, "compat@1.2.3")

	resp := apiRequest(t, http.MethodGet, source.server.URL+"/api/0/organizations/urgentry-org/export/", source.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	resp.Body.Close()

	target := newCompatServer(t, compatOptions{
		startWorkers: true,
		email:        "importer-release@example.com",
		displayName:  "Importer Release",
		pat:          "gpat_target_release_import",
	})
	defer target.close()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}
	resp = apiRequest(t, http.MethodPost, target.server.URL+"/api/0/organizations/urgentry-org/import/", target.pat, bytes.NewReader(body), "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, want 200", resp.StatusCode)
	}

	resp = apiRequest(t, http.MethodGet, target.server.URL+"/api/0/organizations/urgentry-org/releases/", target.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release list status = %d, want 200", resp.StatusCode)
	}
	var releases []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		t.Fatalf("decode release list: %v", err)
	}
	found := false
	for _, release := range releases {
		if release["version"] == "compat@1.2.3" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("imported release not found")
	}
}

func TestSourceMapUpload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()
	createRelease(t, srv, "frontend@1.0.0")

	uploadSourceMap(t, srv, "frontend@1.0.0", "app.min.js.map", []byte(`{"version":3,"file":"app.min.js","sources":["app.ts"],"names":[],"mappings":"AAAA"}`))

	artifact, data, err := srv.sourceMaps.LookupByName(context.Background(), "default-project", "frontend@1.0.0", "app.min.js.map")
	if err != nil {
		t.Fatalf("lookup source map: %v", err)
	}
	if artifact == nil || len(data) == 0 {
		t.Fatal("uploaded source map not found")
	}
}

func TestProguardUpload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()
	createRelease(t, srv, "android@2.0.0")

	uploadProGuard(t, srv, "android@2.0.0", "UUID-2", []byte("com.example.Foo -> a:"))

	resp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/releases/android@2.0.0/proguard/UUID-2/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proguard lookup status = %d, want 200", resp.StatusCode)
	}
}

func TestAttachmentRoundtrip(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "envelopes", "error_with_attachment.envelope")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attachment envelope status = %d, want 200", resp.StatusCode)
	}
	waitForEvent(t, srv.db, "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2")

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/events/b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2/attachments/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list attachments status = %d, want 200", resp.StatusCode)
	}
	var attachments []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&attachments); err != nil {
		t.Fatalf("decode attachment list: %v", err)
	}
	resp.Body.Close()
	if len(attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(attachments))
	}
	attachmentID, _ := attachments[0]["id"].(string)
	if attachmentID == "" {
		t.Fatal("attachment id missing")
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/events/b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2/attachments/"+attachmentID+"/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attachment download status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if string(body) != "this is a log file" {
		t.Fatalf("attachment body = %q", string(body))
	}
}

func TestStandaloneAttachmentUpload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "envelopes", "single_error.envelope")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope status = %d, want 200", resp.StatusCode)
	}
	waitForEvent(t, srv.db, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "standalone.log")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("standalone attachment")); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	if err := writer.WriteField("event_id", "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"); err != nil {
		t.Fatalf("WriteField event_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	resp = apiRequest(t, http.MethodPost, srv.server.URL+"/api/0/projects/urgentry-org/default/attachments/", srv.pat, &body, writer.FormDataContentType())
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("standalone attachment upload status = %d, want 201, body=%s", resp.StatusCode, b)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created attachment: %v", err)
	}
	resp.Body.Close()
	attachmentID, _ := created["id"].(string)
	if attachmentID == "" {
		t.Fatalf("attachment id missing: %+v", created)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/events/a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1/attachments/"+attachmentID+"/", srv.pat, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("standalone attachment download status = %d, want 200", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read standalone attachment: %v", err)
	}
	if string(data) != "standalone attachment" {
		t.Fatalf("standalone attachment body = %q", string(data))
	}
}

func TestSearchLanguageCompatibility(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	errorPayload := []byte(`{
		"event_id":"f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1",
		"release":"backend@1.2.3",
		"platform":"python",
		"level":"error",
		"message":"ValueError: bad input",
		"exception":{"values":[{"type":"ValueError","value":"bad input"}]}
	}`)
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/store/", bytes.NewReader(errorPayload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("store status = %d, want 200", resp.StatusCode)
	}
	waitForEvent(t, srv.db, "f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1")

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/issues/", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list issues status = %d, want 200", resp.StatusCode)
	}
	var issues []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		t.Fatalf("decode issues: %v", err)
	}
	resp.Body.Close()

	issueID := ""
	for _, item := range issues {
		if title, _ := item["title"].(string); strings.Contains(title, "ValueError") {
			issueID, _ = item["id"].(string)
			break
		}
	}
	if issueID == "" {
		t.Fatalf("ValueError issue not found: %+v", issues)
	}

	resp = apiRequest(t, http.MethodPut, srv.server.URL+"/api/0/issues/"+issueID+"/", srv.pat, bytes.NewReader([]byte(`{"status":"resolved"}`)), "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve issue status = %d, want 200", resp.StatusCode)
	}

	logPayload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeLogs":[{"scope":{"name":"checkout.logger"},"logRecords":[{"timeUnixNano":"1743076800000000000","severityText":"INFO","body":{"stringValue":"cache miss"}}]}]}]}`)
	resp = doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/logs/", bytes.NewReader(logPayload), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("otlp log status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := srv.db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = 'default-project' AND event_type = 'log'`).Scan(&count); err != nil {
			t.Fatalf("count log events: %v", err)
		}
		if count > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/issues/?query=is:resolved%20release:backend@1.2.3%20ValueError", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("typed issue search status = %d, want 200", resp.StatusCode)
	}
	issues = nil
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		t.Fatalf("decode typed issue search: %v", err)
	}
	resp.Body.Close()
	if len(issues) != 1 {
		t.Fatalf("typed issue search count = %d, want 1; issues=%+v", len(issues), issues)
	}

	resp = apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/issues/?query=event.type:log", srv.pat, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event.type issue search status = %d, want 200", resp.StatusCode)
	}
	issues = nil
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		t.Fatalf("decode event.type issue search: %v", err)
	}
	resp.Body.Close()
	if len(issues) == 0 {
		t.Fatal("expected at least one log issue result")
	}
}

func TestFeedbackRoundtrip(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	payload := fixtureBytes(t, "envelopes", "user_feedback.envelope")
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/envelope/", bytes.NewReader(payload), map[string]string{
		"Content-Type":  "application/x-sentry-envelope",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback envelope status = %d, want 200", resp.StatusCode)
	}

	client := loginClient(t, srv)
	resp, err := client.Get(srv.server.URL + "/feedback/")
	if err != nil {
		t.Fatalf("GET /feedback/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback page status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read feedback page: %v", err)
	}
	if !strings.Contains(string(body), "The page crashed when I clicked submit") {
		t.Fatal("feedback comment not rendered")
	}
}

// --- envelope helpers (shared across conformance tests) ---

// buildEnvelope constructs a raw Sentry envelope from header + items.
// Each item is a pair of (itemHeader, payload).
func buildEnvelope(header map[string]any, items ...envelopeItem) []byte {
	var buf bytes.Buffer
	headerJSON, _ := json.Marshal(header)
	buf.Write(headerJSON)
	buf.WriteByte('\n')
	for _, item := range items {
		payloadBytes := item.payload
		ih := map[string]any{"type": item.typ}
		if item.length > 0 {
			ih["length"] = item.length
		} else {
			ih["length"] = len(payloadBytes)
		}
		if item.filename != "" {
			ih["filename"] = item.filename
		}
		if item.contentType != "" {
			ih["content_type"] = item.contentType
		}
		ihJSON, _ := json.Marshal(ih)
		buf.Write(ihJSON)
		buf.WriteByte('\n')
		buf.Write(payloadBytes)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

type envelopeItem struct {
	typ         string
	payload     []byte
	length      int
	filename    string
	contentType string
}

func jsonPayload(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// --- API CRUD helpers (shared across e2e tests) ---

// apiGet is a shorthand for apiRequest(t, "GET", url, pat, nil, "").
func apiGet(t *testing.T, url, pat string) *http.Response {
	t.Helper()
	return apiRequest(t, "GET", url, pat, nil, "")
}

// apiPost is a shorthand for apiRequest with a JSON body.
func apiPost(t *testing.T, url, pat string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return apiRequest(t, "POST", url, pat, bytes.NewReader(data), "application/json")
}

// apiPut is a shorthand for apiRequest with a JSON body.
func apiPut(t *testing.T, url, pat string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return apiRequest(t, "PUT", url, pat, bytes.NewReader(data), "application/json")
}

// apiDelete is a shorthand for apiRequest(t, "DELETE", url, pat, nil, "").
func apiDelete(t *testing.T, url, pat string) *http.Response {
	t.Helper()
	return apiRequest(t, "DELETE", url, pat, nil, "")
}

// readJSON reads the response body and decodes it as JSON into dest.
func readJSON(t *testing.T, resp *http.Response, dest any) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		t.Fatalf("unmarshal JSON: %v\nbody: %s", err, body)
	}
}

// requireStatus checks that the response has the expected status code.
func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("want status %d, got %d; body: %s", want, resp.StatusCode, body)
	}
}

// crudTestServer creates a compatServer and a team + project for CRUD tests.
// Returns the server, org slug, team slug, project slug.
func crudTestServer(t *testing.T) (srv *compatServer, orgSlug, teamSlug, projSlug string) {
	t.Helper()
	srv = newCompatServer(t, compatOptions{})
	t.Cleanup(srv.close)

	orgSlug = "urgentry-org"
	teamSlug = "test-team"
	projSlug = "test-project"

	// Create a team.
	resp := apiPost(t, srv.server.URL+"/api/0/organizations/"+orgSlug+"/teams/", srv.pat, map[string]string{
		"slug": teamSlug,
		"name": "Test Team",
	})
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Create a project under the team.
	resp = apiPost(t, srv.server.URL+"/api/0/teams/"+orgSlug+"/"+teamSlug+"/projects/", srv.pat, map[string]string{
		"name":     "Test Project",
		"slug":     projSlug,
		"platform": "go",
	})
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	return srv, orgSlug, teamSlug, projSlug
}
