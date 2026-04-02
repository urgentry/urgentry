package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/analyticsservice"
	"urgentry/internal/attachment"
	"urgentry/internal/auth"
	"urgentry/internal/config"
	ghttp "urgentry/internal/http"
	"urgentry/internal/integration"
	"urgentry/internal/issue"
	"urgentry/internal/metrics"
	"urgentry/internal/nativesym"
	"urgentry/internal/notify"
	"urgentry/internal/pipeline"
	"urgentry/internal/proguard"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
	"urgentry/internal/telemetryquery"
	"urgentry/pkg/id"
)

type deploymentMode string

const (
	deploymentModeTiny              deploymentMode = "tiny"
	deploymentModeSeriousSelfHosted deploymentMode = "serious-self-hosted"
)

type roleMode struct {
	mountsAPI     bool
	runsWorker    bool
	runsScheduler bool
}

func newRoleMode(role Role) roleMode {
	return roleMode{
		mountsAPI:     role == RoleAll || role == RoleAPI,
		runsWorker:    role == RoleAll || role == RoleWorker,
		runsScheduler: role == RoleAll || role == RoleScheduler,
	}
}

func newDeploymentMode(cfg config.Config) deploymentMode {
	if strings.TrimSpace(cfg.ControlDSN) != "" || strings.TrimSpace(cfg.TelemetryDSN) != "" {
		return deploymentModeSeriousSelfHosted
	}
	return deploymentModeTiny
}

type runtimeState struct {
	cfg        config.Config
	role       Role
	version    string
	mode       roleMode
	deployment deploymentMode
	dataDir    string

	db          *sql.DB
	telemetryDB *sql.DB
	blobStore   store.BlobStore
	control     runtimeControlPlane

	runtimeClosers []func() error

	keyStore         auth.KeyStore
	authStore        auth.Store
	rateLimiter      auth.RateLimiter
	queryGuard       sqlite.QueryGuard
	workerQueue      runtimeasync.Queue
	backfillQueue    runtimeasync.Queue
	leaseStore       runtimeasync.LeaseStore
	eventStore       store.EventStore
	feedbackStore    *sqlite.FeedbackStore
	hookStore        *sqlite.HookStore
	backfillStore    *sqlite.BackfillStore
	jobStore         *sqlite.JobStore
	retentionStore   *sqlite.RetentionStore
	outcomeStore     *sqlite.OutcomeStore
	attachmentStore  attachment.Store
	debugFileStore   *sqlite.DebugFileStore
	principalShadows *sqlite.PrincipalShadowStore
	analytics        analyticsservice.Services
	auditStore       *sqlite.AuditStore

	nativeCrashStore       *sqlite.NativeCrashStore
	backfillController     *pipeline.BackfillController
	projectionQueue        runtimeasync.Queue
	projectionEnqueuer     *telemetrybridge.ProjectionEnqueuer
	projectionWorker       *telemetrybridge.ProjectionWorker
	proguardStore          proguard.Store
	sourceMapStore         sourcemap.Store
	releaseHealthStore     *sqlite.ReleaseHealthStore
	traceStore             *sqlite.TraceStore
	profileStore           *sqlite.ProfileStore
	replayStore            *sqlite.ReplayStore
	replayPolicies         *sqlite.ReplayConfigStore
	nativeControlStore     *sqlite.NativeControlStore
	importExportStore      *sqlite.ImportExportStore
	codeMappingStore       *sqlite.CodeMappingStore
	queryService           telemetryquery.Service
	evaluator              *alert.Evaluator
	notifier               *notify.Notifier
	releaseStore           *sqlite.ReleaseStore
	metrics                *metrics.Metrics
	pipeline               *pipeline.Pipeline
	operatorStore          store.OperatorStore
	integrationRegistry    *integration.Registry
	integrationConfigStore integration.Store
	samplingRuleStore      *sqlite.SamplingRuleStore
	uptimeMonitorStore     *sqlite.UptimeMonitorStore
	quotaStore             *sqlite.QuotaStore
	symbolSourceStore      *sqlite.SymbolSourceStore
}

func newRuntimeState(cfg config.Config, role Role, version string) (state *runtimeState, err error) {
	state = &runtimeState{
		cfg:        cfg,
		role:       role,
		version:    version,
		mode:       newRoleMode(role),
		deployment: newDeploymentMode(cfg),
	}
	defer func() {
		if err != nil && state != nil {
			state.close()
		}
	}()

	if err = state.openDatabases(); err != nil {
		return nil, err
	}
	if err = state.openSharedBackends(); err != nil {
		return nil, err
	}
	if err = state.buildCoreStores(); err != nil {
		return nil, err
	}
	if err = state.openAsyncRuntime(); err != nil {
		return nil, err
	}
	if err = state.buildRuntimeServices(); err != nil {
		return nil, err
	}

	return state, nil
}

func (s *runtimeState) openDatabases() error {
	s.dataDir = s.cfg.DataDir
	if s.dataDir == "" {
		home, _ := os.UserHomeDir()
		s.dataDir = filepath.Join(home, ".urgentry")
	}

	db, err := sqlite.Open(s.dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	s.db = db
	log.Info().Str("data_dir", s.dataDir).Msg("database ready")

	if strings.TrimSpace(s.cfg.TelemetryDSN) == "" {
		return nil
	}

	telemetryDB, err := telemetrybridge.Open(context.Background(), s.cfg.TelemetryDSN)
	if err != nil {
		return fmt.Errorf("open telemetry bridge: %w", err)
	}
	backend := telemetrybridge.BackendPostgres
	if strings.EqualFold(strings.TrimSpace(s.cfg.TelemetryBackend), string(telemetrybridge.BackendTimescale)) {
		backend = telemetrybridge.BackendTimescale
	}
	if err := telemetrybridge.Migrate(context.Background(), telemetryDB, backend); err != nil {
		_ = telemetryDB.Close()
		return fmt.Errorf("migrate telemetry bridge: %w", err)
	}
	s.telemetryDB = telemetryDB
	log.Info().Str("telemetry_backend", string(backend)).Msg("telemetry bridge ready")
	return nil
}

func (s *runtimeState) openSharedBackends() error {
	blobStore, err := openBlobStore(s.cfg, s.dataDir)
	if err != nil {
		return fmt.Errorf("create blob store: %w", err)
	}
	s.blobStore = blobStore

	control, err := openRuntimeControlPlane(context.Background(), s.cfg, s.db)
	if err != nil {
		return err
	}
	s.control = control
	s.keyStore = control.keyStore
	s.authStore = control.authStore
	return nil
}

func (s *runtimeState) buildCoreStores() error {
	s.eventStore = sqlite.NewEventStore(s.db)
	s.feedbackStore = sqlite.NewFeedbackStore(s.db)
	s.hookStore = sqlite.NewHookStore(s.db)
	s.backfillStore = sqlite.NewBackfillStore(s.db)
	s.jobStore = sqlite.NewJobStore(s.db)
	s.retentionStore = sqlite.NewRetentionStore(s.db, s.blobStore)
	s.outcomeStore = sqlite.NewOutcomeStore(s.db)
	s.attachmentStore = attachment.Store(sqlite.NewAttachmentStore(s.db, s.blobStore))
	s.debugFileStore = sqlite.NewDebugFileStore(s.db, s.blobStore)
	s.principalShadows = sqlite.NewPrincipalShadowStore(s.db)
	s.analytics = analyticsservice.SQLiteServices(s.db)
	s.auditStore = sqlite.NewAuditStore(s.db)
	s.integrationRegistry = integration.NewDefaultRegistry()
	s.integrationConfigStore = sqlite.NewIntegrationConfigStore(s.db)
	s.samplingRuleStore = sqlite.NewSamplingRuleStore(s.db)
	s.uptimeMonitorStore = sqlite.NewUptimeMonitorStore(s.db)
	s.quotaStore = sqlite.NewQuotaStore(s.db)
	s.symbolSourceStore = sqlite.NewSymbolSourceStore(s.db)
	return nil
}

func (s *runtimeState) openAsyncRuntime() error {
	s.workerQueue = s.jobStore
	s.leaseStore = s.jobStore

	var err error
	if strings.EqualFold(strings.TrimSpace(s.cfg.AsyncBackend), "jetstream") {
		s.workerQueue, err = runtimeasync.NewJetStreamQueue(s.cfg.NATSURL, "worker-"+id.New()[:8], sqlite.JobKindEvent, sqlite.JobKindNativeStackwalk)
		if err != nil {
			return fmt.Errorf("create jetstream worker queue: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.workerQueue.(*runtimeasync.JetStreamQueue).Close)

		s.backfillQueue, err = runtimeasync.NewJetStreamQueue(s.cfg.NATSURL, "backfill-"+id.New()[:8], sqlite.JobKindBackfill)
		if err != nil {
			return fmt.Errorf("create jetstream backfill queue: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.backfillQueue.(*runtimeasync.JetStreamQueue).Close)

		s.projectionQueue, err = runtimeasync.NewJetStreamQueue(s.cfg.NATSURL, "projection-"+id.New()[:8], sqlite.JobKindBridgeProjection)
		if err != nil {
			return fmt.Errorf("create jetstream projection queue: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.projectionQueue.(*runtimeasync.JetStreamQueue).Close)
	}
	if strings.EqualFold(strings.TrimSpace(s.cfg.CacheBackend), "valkey") {
		s.rateLimiter, err = auth.NewValkeyRateLimiter(s.cfg.ValkeyURL, time.Minute)
		if err != nil {
			return fmt.Errorf("create valkey rate limiter: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.rateLimiter.(*auth.ValkeyRateLimiter).Close)

		s.leaseStore, err = runtimeasync.NewValkeyLeaseStore(s.cfg.ValkeyURL)
		if err != nil {
			return fmt.Errorf("create valkey lease store: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.leaseStore.(*runtimeasync.ValkeyLeaseStore).Close)

		s.queryGuard, err = sqlite.NewValkeyQueryGuardStore(s.db, s.cfg.ValkeyURL)
		if err != nil {
			return fmt.Errorf("create valkey query guard: %w", err)
		}
		s.runtimeClosers = append(s.runtimeClosers, s.queryGuard.(*sqlite.ValkeyQueryGuardStore).Close)
	}
	if s.queryGuard == nil {
		s.queryGuard = sqlite.NewQueryGuardStore(s.db)
	}
	return nil
}

func (s *runtimeState) buildRuntimeServices() error {
	s.nativeCrashStore = sqlite.NewNativeCrashStore(s.db, s.blobStore, s.workerQueue, s.cfg.PipelineQueueSize)
	s.backfillController = pipeline.NewBackfillController(s.backfillStore, s.debugFileStore, "backfill-"+id.New()[:12])
	if s.telemetryDB != nil {
		s.backfillController.SetTelemetryRebuilder(telemetrybridge.NewProjector(s.db, s.telemetryDB))
	}
	if s.backfillQueue != nil {
		s.backfillController.SetQueue(s.backfillQueue)
		if enqueuer, ok := s.backfillQueue.(runtimeasync.KeyedEnqueuer); ok {
			s.backfillController.SetEnqueuer(enqueuer)
		}
	}

	s.proguardStore = sqlite.NewProGuardStore(s.db, s.blobStore)
	s.sourceMapStore = sqlite.NewSourceMapStore(s.db, s.blobStore)
	s.releaseHealthStore = sqlite.NewReleaseHealthStore(s.db)
	s.traceStore = sqlite.NewTraceStore(s.db)
	s.profileStore = sqlite.NewProfileStore(s.db, s.blobStore)
	s.replayStore = sqlite.NewReplayStore(s.db, s.blobStore)
	s.replayPolicies = sqlite.NewReplayConfigStore(s.db)
	s.nativeControlStore = sqlite.NewNativeControlStore(s.db, s.blobStore, s.control.operatorAudits)
	s.importExportStore = sqlite.NewImportExportStore(s.db, s.attachmentStore, s.proguardStore, s.sourceMapStore, s.blobStore)
	s.codeMappingStore = sqlite.NewCodeMappingStore(s.db)
	queryDeps := QueryServiceDeps{
		Blobs:       s.blobStore,
		IssueSearch: s.control.services.IssueReads,
		Web:         sqlite.NewWebStore(s.db),
		Discover:    sqlite.NewDiscoverEngine(s.db),
		Traces:      s.traceStore,
		Replays:     s.replayStore,
		Profiles:    s.profileStore,
	}
	if s.telemetryDB != nil {
		queryDeps.Profiles = telemetrybridge.NewProfileReadStore(s.telemetryDB, s.blobStore)
		queryDeps.Projector = telemetrybridge.NewProjector(s.db, s.telemetryDB)
	}
	s.queryService = newTelemetryQueryService(s.db, s.telemetryDB, queryDeps)

	s.evaluator = &alert.Evaluator{Rules: s.control.alertStore}
	s.notifier = notify.NewNotifier(s.control.outbox, s.control.deliveries)
	if s.cfg.SMTPHost != "" {
		s.notifier.SMTP = notify.SMTPConfig{
			Host: s.cfg.SMTPHost,
			Port: s.cfg.SMTPPort,
			From: s.cfg.SMTPFrom,
			User: s.cfg.SMTPUser,
			Pass: s.cfg.SMTPPass,
		}
	}
	s.releaseStore = sqlite.NewReleaseStore(s.db)

	smResolver := &sourcemap.Resolver{Store: s.sourceMapStore}
	pgResolver := &proguard.Resolver{Store: s.proguardStore}
	nativeResolver := nativesym.NewResolver(&nativeDebugFileStore{store: s.debugFileStore})
	proc := &issue.Processor{
		Events:     s.eventStore,
		Groups:     s.control.groupStore,
		Blobs:      s.blobStore,
		Releases:   s.releaseStore,
		SourceMaps: smResolver,
		ProGuard:   pgResolver,
		Native:     nativeResolver,
		Traces:     s.traceStore,
		Ownership:  s.control.ownershipStore,
	}

	s.metrics = metrics.New()
	s.metrics.MetricsToken = s.cfg.MetricsToken
	s.pipeline = pipeline.NewDurable(proc, s.workerQueue, s.cfg.PipelineQueueSize, s.cfg.PipelineWorkers)
	s.pipeline.SetMetrics(s.metrics)
	s.pipeline.SetNativeJobProcessor(pipeline.NativeJobProcessorFunc(func(ctx context.Context, projectID string, payload []byte) error {
		return s.nativeCrashStore.ProcessStackwalkJob(ctx, proc, projectID, payload)
	}))
	if s.telemetryDB != nil && s.projectionQueue != nil {
		s.projectionEnqueuer = telemetrybridge.NewProjectionEnqueuer(s.projectionQueue, s.cfg.PipelineQueueSize)
		s.projectionWorker = telemetrybridge.NewProjectionWorker(
			telemetrybridge.NewProjector(s.db, s.telemetryDB),
			s.projectionQueue,
			"projection-"+id.New()[:12],
		)
		s.pipeline.SetProjectionCallback(func(ctx context.Context, projectID, eventType string) {
			families := telemetrybridge.FamiliesForEventType(eventType)
			if err := s.projectionEnqueuer.EnqueueProjection(ctx, projectID, eventType, families...); err != nil {
				log.Warn().Err(err).
					Str("project_id", projectID).
					Str("event_type", eventType).
					Msg("bridge projection enqueue failed")
			}
		})
	} else if s.telemetryDB != nil {
		// Fallback: use the worker queue for projection when JetStream is not available.
		s.projectionEnqueuer = telemetrybridge.NewProjectionEnqueuer(s.workerQueue, s.cfg.PipelineQueueSize)
		s.pipeline.SetProjectionCallback(func(ctx context.Context, projectID, eventType string) {
			families := telemetrybridge.FamiliesForEventType(eventType)
			if err := s.projectionEnqueuer.EnqueueProjection(ctx, projectID, eventType, families...); err != nil {
				log.Warn().Err(err).
					Str("project_id", projectID).
					Str("event_type", eventType).
					Msg("bridge projection enqueue failed")
			}
		})
	}
	s.pipeline.SetAlertCallback(pipeline.NewAlertCallback(*s.alertDeps()))

	opStore := sqlite.NewOperatorStore(
		s.db,
		store.OperatorRuntime{
			Role:         string(s.role),
			Env:          s.cfg.Env,
			Version:      s.version,
			AsyncBackend: s.cfg.AsyncBackend,
			CacheBackend: s.cfg.CacheBackend,
			BlobBackend:  s.cfg.BlobBackend,
		},
		s.control.lifecycle,
		s.control.operatorAudits,
		func(ctx context.Context) (int, error) {
			if s.pipeline != nil {
				return s.pipeline.Len(), nil
			}
			return s.jobStore.Len(ctx)
		},
		ghttp.OperatorServiceChecks(s.db, s.cfg)...,
	)
	if s.telemetryDB != nil {
		projector := telemetrybridge.NewProjector(s.db, s.telemetryDB)
		opStore.SetBridgeFreshness(func(ctx context.Context) ([]store.OperatorBridgeFreshness, error) {
			items, err := projector.AssessFreshness(ctx, telemetrybridge.Scope{})
			if err != nil {
				return nil, err
			}
			out := make([]store.OperatorBridgeFreshness, 0, len(items))
			for _, item := range items {
				out = append(out, store.OperatorBridgeFreshness{
					Family:    string(item.Family),
					Pending:   item.Pending,
					Lag:       item.Lag,
					LastError: item.LastError,
				})
			}
			return out, nil
		})
	}
	s.operatorStore = opStore

	return nil
}

func (s *runtimeState) alertDeps() *pipeline.AlertDeps {
	return &pipeline.AlertDeps{
		Evaluator:        s.evaluator,
		Notifier:         s.notifier,
		HistoryStore:     s.control.alertHistory,
		Hooks:            s.hookStore,
		AlertStore:       s.control.alertStore,
		MetricAlertStore: s.control.services.MetricAlerts,
		Profiles:         s.profileStore,
		Metrics:          s.metrics,
	}
}

func (s *runtimeState) close() {
	for i := len(s.runtimeClosers) - 1; i >= 0; i-- {
		_ = s.runtimeClosers[i]()
	}
	if s.control.close != nil {
		_ = s.control.close()
	}
	if s.telemetryDB != nil {
		_ = s.telemetryDB.Close()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}
