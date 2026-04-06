package synthetic

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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
)

type HarnessOptions struct {
	StartWorkers    bool   `yaml:"start_workers" json:"start_workers"`
	QueueSize       int    `yaml:"queue_size" json:"queue_size"`
	IngestRateLimit int    `yaml:"ingest_rate_limit" json:"ingest_rate_limit"`
	Email           string `yaml:"email,omitempty" json:"email,omitempty"`
	DisplayName     string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	PAT             string `yaml:"pat,omitempty" json:"pat,omitempty"`
	DataDir         string `yaml:"data_dir,omitempty" json:"data_dir,omitempty"`
}

type Harness struct {
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

type Request struct {
	Method      string
	Path        string
	Auth        string
	Body        []byte
	ContentType string
	Headers     map[string]string
	Query       map[string]string
}

type Response struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

type NativeDebugLookup struct {
	Store *sqlite.DebugFileStore
}

func (n NativeDebugLookup) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*nativesym.File, []byte, error) {
	if n.Store == nil {
		return nil, nil, nil
	}
	file, body, err := n.Store.LookupByDebugID(ctx, projectID, releaseVersion, kind, debugID)
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}

func (n NativeDebugLookup) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*nativesym.File, []byte, error) {
	if n.Store == nil {
		return nil, nil, nil
	}
	file, body, err := n.Store.LookupByCodeID(ctx, projectID, releaseVersion, kind, codeID)
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}

func NewHarness(opts HarnessOptions) (*Harness, error) {
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = mustTempDir("urgentry-synthetic-")
	}

	db, err := sqlite.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Open: %w", err)
	}

	keyStore := sqlite.NewKeyStore(db)
	authStore := sqlite.NewAuthStore(db)
	projectKey, err := sqlite.EnsureDefaultKey(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("EnsureDefaultKey: %w", err)
	}

	if opts.Email == "" {
		opts.Email = "synthetic-admin@example.com"
	}
	if opts.DisplayName == "" {
		opts.DisplayName = "Synthetic Admin"
	}
	if opts.PAT == "" {
		opts.PAT = "gpat_synthetic_admin_token"
	}

	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 opts.Email,
		DisplayName:           opts.DisplayName,
		Password:              "synthetic-password-123",
		PersonalAccessToken:   opts.PAT,
	})
	if err != nil {
		return nil, fmt.Errorf("EnsureBootstrapAccess: %w", err)
	}

	blobStore := store.NewMemoryBlobStore()
	feedbackStore := sqlite.NewFeedbackStore(db)
	attachmentStore := sqlite.NewAttachmentStore(db, blobStore)
	outcomeStore := sqlite.NewOutcomeStore(db)
	traceStore := sqlite.NewTraceStore(db)
	sourceMapStore := sqlite.NewSourceMapStore(db, blobStore)
	proGuardStore := sqlite.NewProGuardStore(db, blobStore)
	releaseStore := sqlite.NewReleaseStore(db)
	debugFiles := sqlite.NewDebugFileStore(db, blobStore)

	processor := &issue.Processor{
		Events:     sqlite.NewEventStore(db),
		Groups:     sqlite.NewGroupStore(db),
		Blobs:      blobStore,
		Releases:   releaseStore,
		SourceMaps: &sourcemap.Resolver{Store: sourceMapStore},
		ProGuard:   &proguard.Resolver{Store: proGuardStore},
		Native:     nativesym.NewResolver(NativeDebugLookup{Store: debugFiles}),
		Traces:     traceStore,
	}

	queueSize := opts.QueueSize
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
		Env:               "synthetic",
		HTTPAddr:          ":0",
		SessionCookieName: "urgentry_session",
		CSRFCookieName:    "urgentry_csrf",
		IngestRateLimit:   opts.IngestRateLimit,
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
	retention := sqlite.NewRetentionStore(db, blobStore)
	nativeControl := sqlite.NewNativeControlStore(db, blobStore, operatorAudits)
	importExport := sqlite.NewImportExportStore(db, attachmentStore, proGuardStore, sourceMapStore, blobStore)
	operatorStore := sqlite.NewOperatorStore(db, store.OperatorRuntime{Role: "synthetic", Env: "synthetic"}, sqlite.NewLifecycleStore(db), operatorAudits, func(context.Context) (int, error) {
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
	if opts.StartWorkers {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		backfillController := pipeline.NewBackfillController(sqlite.NewBackfillStore(db), debugFiles, "synthetic-native-backfill")
		pipe.Start(workerCtx)
		done = make(chan struct{})
		go func() {
			defer close(done)
			backfillController.Run(workerCtx)
		}()
		cancel = workerCancel
	}

	return &Harness{
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
	}, nil
}

func (h *Harness) Close() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.done != nil {
		<-h.done
	}
	if h.pipe != nil {
		h.pipe.Stop()
	}
	if h.server != nil {
		h.server.Close()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
}

func (h *Harness) BaseURL() string    { return h.server.URL }
func (h *Harness) ProjectKey() string { return h.projectKey }
func (h *Harness) PAT() string        { return h.pat }
func (h *Harness) Email() string      { return h.email }
func (h *Harness) Password() string   { return h.password }
func (h *Harness) DB() *sql.DB        { return h.db }

func (h *Harness) SentryAuthHeader() string {
	return "Sentry sentry_key=" + h.projectKey + ",sentry_version=7,sentry_client=synthetic/1.0"
}

func (h *Harness) DoRequest(ctx context.Context, request Request) (Response, error) {
	rawURL := h.server.URL + request.Path
	if len(request.Query) > 0 {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return Response{}, err
		}
		query := parsed.Query()
		for key, value := range request.Query {
			query.Set(key, value)
		}
		parsed.RawQuery = query.Encode()
		rawURL = parsed.String()
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, rawURL, bytes.NewReader(request.Body))
	if err != nil {
		return Response{}, err
	}
	if request.ContentType != "" {
		req.Header.Set("Content-Type", request.ContentType)
	}
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	switch request.Auth {
	case "", "none":
	case "project_key", "sentry_auth":
		req.Header.Set("X-Sentry-Auth", h.SentryAuthHeader())
	case "query_sentry_key":
		parsed, err := url.Parse(req.URL.String())
		if err != nil {
			return Response{}, err
		}
		query := parsed.Query()
		query.Set("sentry_key", h.projectKey)
		parsed.RawQuery = query.Encode()
		req.URL = parsed
	case "pat":
		req.Header.Set("Authorization", "Bearer "+h.pat)
	case "session":
		client, err := h.SessionClient()
		if err != nil {
			return Response{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return Response{}, err
		}
		return readResponse(resp)
	default:
		return Response{}, fmt.Errorf("unsupported auth mode %q", request.Auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	return readResponse(resp)
}

func readResponse(resp *http.Response) (Response, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	headers := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return Response{StatusCode: resp.StatusCode, Headers: headers, Body: body}, nil
}

func (h *Harness) SessionClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	form := url.Values{
		"email":    {h.email},
		"password": {h.password},
		"next":     {"/"},
	}
	resp, err := client.PostForm(h.server.URL+"/login/", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login status=%d body=%s", resp.StatusCode, body)
	}
	return client, nil
}

func (h *Harness) UploadSourceMap(release, name string, data []byte) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepathBase(name))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.WriteField("name", name); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	resp, err := h.DoRequest(context.Background(), Request{
		Method:      http.MethodPost,
		Path:        "/api/0/projects/urgentry-org/default/releases/" + release + "/files/",
		Auth:        "pat",
		Body:        body.Bytes(),
		ContentType: writer.FormDataContentType(),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("source map upload status=%d body=%s", resp.StatusCode, resp.Body)
	}
	return nil
}

func (h *Harness) UploadProGuard(release, uuid string, data []byte) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "mapping.txt")
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.WriteField("uuid", uuid); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	resp, err := h.DoRequest(context.Background(), Request{
		Method:      http.MethodPost,
		Path:        "/api/0/projects/urgentry-org/default/releases/" + release + "/proguard/",
		Auth:        "pat",
		Body:        body.Bytes(),
		ContentType: writer.FormDataContentType(),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("proguard upload status=%d body=%s", resp.StatusCode, resp.Body)
	}
	return nil
}

func (h *Harness) WaitForEvent(_ context.Context, eventID string) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitEvent, EventID: eventID})
}

func (h *Harness) WaitForProjectEventCount(_ context.Context, projectID string, want int) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitProjectEventCount, ProjectID: projectID, Count: want})
}

func (h *Harness) WaitForTransactionCount(_ context.Context, projectID string, want int) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitTransactionCount, ProjectID: projectID, Count: want})
}

func (h *Harness) WaitForTrace(_ context.Context, projectID, traceID string) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitTrace, ProjectID: projectID, TraceID: traceID})
}

func (h *Harness) WaitForSession(_ context.Context, projectID, release string) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitSessionRelease, ProjectID: projectID, Release: release})
}

func (h *Harness) WaitForFeedback(_ context.Context, projectID, name string) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitFeedbackName, ProjectID: projectID, Name: name})
}

func (h *Harness) WaitForCheckIn(_ context.Context, projectID, monitorSlug string) error {
	return WaitFor(h.db, WaitSpec{Condition: WaitCheckInMonitor, ProjectID: projectID, MonitorSlug: monitorSlug})
}

func (h *Harness) WaitForMetricBucket(_ context.Context, projectID, name string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := h.db.QueryRow(`SELECT COUNT(*) FROM metric_buckets WHERE project_id = ? AND metric_name = ?`, projectID, name).Scan(&count)
		if err == nil && count > 0 {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("metric bucket %s for project %s was not persisted", name, projectID)
}

func (h *Harness) WaitForNativeEventStatus(_ context.Context, projectID, eventID, status string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var current string
		err := h.db.QueryRow(`SELECT processing_status FROM events WHERE project_id = ? AND event_id = ?`, projectID, eventID).Scan(&current)
		if err == nil && current == status {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("event %s did not reach processing status %s", eventID, status)
}

func (h *Harness) WaitForBackfillRunStatus(_ context.Context, runID string, wants ...string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var current string
		err := h.db.QueryRow(`SELECT status FROM backfill_runs WHERE id = ?`, runID).Scan(&current)
		if err == nil {
			for _, want := range wants {
				if current == want {
					return nil
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("backfill run %s did not reach status in %v", runID, wants)
}

func atoiOrZero(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	out, _ := strconv.Atoi(strings.TrimSpace(value))
	return out
}

func mustTempDir(prefix string) string {
	dir, err := osMkdirTemp("", prefix)
	if err != nil {
		panic(err)
	}
	return dir
}
