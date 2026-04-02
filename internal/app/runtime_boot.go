package app

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/analyticsreport"
	ghttp "urgentry/internal/http"
	"urgentry/internal/pipeline"
	"urgentry/internal/runtimeasync"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

/*
Runtime lifecycle

	Run
	  -> newRuntimeState (shared assembly)
	  -> bootTiny | bootSeriousSelfHosted
	     -> API bootstrap + install-state sync
	     -> worker / scheduler startup
	  -> serve HTTP
	  -> shutdown HTTP
	  -> stop worker + backfill
	  -> wait scheduler
	  -> close runtime
*/

type runtimeBoot struct {
	backfillDone   chan struct{}
	projectionDone chan struct{}
	schedulerDone  chan struct{}
}

func (s *runtimeState) run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := s.newHTTPServer()
	if err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	boot, err := s.boot(ctx)
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	defer boot.waitScheduler()
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("http shutdown error")
		}
		boot.stopWorkers(s.pipeline, s.mode.runsWorker)
		log.Info().Msg("shutdown complete")
		return nil
	case err := <-errCh:
		stop()
		boot.stopWorkers(s.pipeline, s.mode.runsWorker)
		return fmt.Errorf("http server: %w", err)
	}
}

func (s *runtimeState) boot(ctx context.Context) (runtimeBoot, error) {
	switch s.deployment {
	case deploymentModeSeriousSelfHosted:
		return s.bootSeriousSelfHosted(ctx)
	default:
		return s.bootTiny(ctx)
	}
}

// Tiny and serious self-hosted currently share the same boot contract; keeping
// separate helpers prevents future mode-specific behavior from leaking back
// into Run.
func (s *runtimeState) bootTiny(ctx context.Context) (runtimeBoot, error) {
	return s.bootCommon(ctx)
}

func (s *runtimeState) bootSeriousSelfHosted(ctx context.Context) (runtimeBoot, error) {
	return s.bootCommon(ctx)
}

func (s *runtimeState) bootCommon(ctx context.Context) (runtimeBoot, error) {
	bootstrapCompleted, err := s.bootAPI(ctx)
	if err != nil {
		return runtimeBoot{}, fmt.Errorf("api bootstrap: %w", err)
	}
	if err := s.syncInstallState(ctx, bootstrapCompleted); err != nil {
		return runtimeBoot{}, fmt.Errorf("install state: %w", err)
	}
	if s.mode.mountsAPI {
		ensureDefaultAlertRule(s.control.alertStore)
	}
	return s.startBackground(ctx), nil
}

func (s *runtimeState) bootAPI(ctx context.Context) (*bool, error) {
	if !s.mode.mountsAPI {
		return nil, nil
	}

	defaultKey, err := s.control.defaultKey(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("could not ensure default key")
	} else {
		log.Info().Str("public_key", defaultKey).Msg("default project key ready — use in DSN")
	}

	bootstrap, err := s.control.bootstrap(ctx, s.cfg)
	if err != nil {
		return nil, fmt.Errorf("bootstrap account: %w", err)
	}

	completed := true
	if bootstrap != nil && bootstrap.Created {
		log.Info().
			Str("email", bootstrap.Email).
			Str("password_hint", maskSecret(bootstrap.Password)).
			Str("pat_hint", maskSecret(bootstrap.PAT)).
			Msg("bootstrap owner account created — retrieve credentials from your secret store, not from logs")
	}
	return &completed, nil
}

func (s *runtimeState) syncInstallState(ctx context.Context, bootstrapCompleted *bool) error {
	if s.control.lifecycle == nil {
		return nil
	}
	if _, err := s.control.lifecycle.SyncInstallState(ctx, store.InstallStateSync{
		Region:             s.cfg.Region,
		Environment:        s.cfg.Env,
		Version:            s.version,
		BootstrapCompleted: bootstrapCompleted,
		CapturedAt:         time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("sync install state: %w", err)
	}
	return nil
}

func (s *runtimeState) startBackground(ctx context.Context) runtimeBoot {
	var boot runtimeBoot

	if s.mode.runsWorker {
		s.pipeline.Start(ctx)
		log.Info().Int("queue_size", s.cfg.PipelineQueueSize).Int("workers", s.cfg.PipelineWorkers).Msg("pipeline worker started")
		if s.backfillController != nil {
			boot.backfillDone = make(chan struct{})
			go func() {
				defer close(boot.backfillDone)
				s.backfillController.Run(ctx)
			}()
			log.Info().Msg("backfill worker started")
		}
		if s.projectionWorker != nil {
			boot.projectionDone = make(chan struct{})
			go func() {
				defer close(boot.projectionDone)
				s.projectionWorker.Run(ctx)
			}()
			log.Info().Msg("bridge projection worker started")
		}
	}

	if s.mode.runsScheduler {
		scheduler := pipeline.NewScheduler(
			s.workerQueue,
			s.leaseStore,
			s.retentionStore,
			s.control.monitorStore,
			"scheduler-"+id.New()[:12],
			s.alertDeps(),
		)
		if enqueuer, ok := s.backfillQueue.(runtimeasync.KeyedEnqueuer); ok {
			scheduler.SetBackfillEnqueuer(enqueuer)
		}
		scheduler.SetReportRunner(&analyticsreport.Runner{
			Schedules: s.analytics.ReportSchedules,
			Freezer: &analyticsreport.Freezer{
				Analytics: s.analytics,
				Queries:   s.queryService,
			},
			Outbox:     s.control.outbox,
			Deliveries: s.control.deliveries,
			BaseURL:    s.cfg.BaseURL,
		})
		if s.uptimeMonitorStore != nil {
			scheduler.SetUptimePoller(pipeline.NewUptimePoller(s.uptimeMonitorStore, s.alertDeps()))
		}
		if s.control.services.MetricAlerts != nil {
			scheduler.SetMetricAlertRunner(&alert.MetricAlertEvaluator{
				Store:  s.control.services.MetricAlerts,
				Engine: sqlite.NewMetricQueryEngine(s.db),
			})
		}
		boot.schedulerDone = make(chan struct{})
		go func() {
			defer close(boot.schedulerDone)
			scheduler.Run(ctx)
		}()
		log.Info().Str("role", string(s.role)).Msg("scheduler started")
	}

	return boot
}

func (s *runtimeState) newHTTPServer() (*http.Server, error) {
	deps := newHTTPDeps(httpDepsInput{
		db:                  s.db,
		dataDir:             s.dataDir,
		keyStore:            s.keyStore,
		authStore:           s.authStore,
		rateLimiter:         s.rateLimiter,
		pipeline:            s.pipeline,
		lifecycle:           s.control.lifecycle,
		control:             s.control.services,
		queryGuard:          s.queryGuard,
		queryService:        s.queryService,
		blobStore:           s.blobStore,
		feedbackStore:       s.feedbackStore,
		hooks:               s.hookStore,
		attachmentStore:     s.attachmentStore,
		outcomeStore:        s.outcomeStore,
		proguardStore:       s.proguardStore,
		sourceMapStore:      s.sourceMapStore,
		releaseHealth:       s.releaseHealthStore,
		nativeCrashes:       s.nativeCrashStore,
		alertDeps:           s.alertDeps(),
		eventStore:          s.eventStore,
		replayStore:         s.replayStore,
		replayPolicies:      s.replayPolicies,
		profileStore:        s.profileStore,
		operatorStore:       s.operatorStore,
		operatorAudits:      s.control.operatorAudits,
		analytics:           s.analytics,
		principalShadows:    s.principalShadows,
		backfillStore:       s.backfillStore,
		auditStore:          s.auditStore,
		nativeControl:       s.nativeControlStore,
		debugFiles:          s.debugFileStore,
		retentionStore:      s.retentionStore,
		importExport:        s.importExportStore,
		integrationRegistry: s.integrationRegistry,
		integrationStore:    s.integrationConfigStore,
		sentryApps:          s.sentryAppStore,
		externalIssues:      s.externalIssueStore,
		samplingRules:       s.samplingRuleStore,
		uptimeMonitors:      s.uptimeMonitorStore,
		quota:               s.quotaStore,
		symbolSources:       s.symbolSourceStore,
		prevent:             s.control.preventStore,
		metrics:             s.metrics,
		version:             s.version,
	})
	if err := ghttp.ValidateDeps(string(s.role), s.cfg, deps); err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           ghttp.NewServer(string(s.role), s.cfg, deps),
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}, nil
}

func (b runtimeBoot) stopWorkers(pipe *pipeline.Pipeline, runsWorker bool) {
	if !runsWorker {
		return
	}
	pipe.Stop()
	if b.backfillDone != nil {
		<-b.backfillDone
	}
	if b.projectionDone != nil {
		<-b.projectionDone
	}
}

func (b runtimeBoot) waitScheduler() {
	if b.schedulerDone != nil {
		<-b.schedulerDone
	}
}

// maskSecret returns a redacted hint showing only the last 4 characters,
// or "***" when the value is too short to safely hint.
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "***"
	}
	return "***" + s[len(s)-4:]
}
