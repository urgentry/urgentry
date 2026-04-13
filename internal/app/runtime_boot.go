package app

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
	"urgentry/internal/analyticsreport"
	"urgentry/internal/auth"
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
	boot, err := s.bootCommon(ctx)
	if err != nil {
		return runtimeBoot{}, err
	}
	if s.mode.mountsAPI {
		ensureDefaultAlertRule(s.control.alertStore)
	}
	return boot, nil
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
		// Show full credentials when auto-generated (no env vars set) so the
		// user can actually log in on first run. When credentials come from
		// env vars the user already knows them, so we mask.
		userSupplied := s.cfg.BootstrapPassword != "" || s.cfg.BootstrapPAT != ""
		if userSupplied {
			log.Info().
				Str("email", bootstrap.Email).
				Msg("bootstrap owner account created")
		} else {
			log.Info().
				Str("email", bootstrap.Email).
				Str("password", bootstrap.Password).
				Str("pat", bootstrap.PAT).
				Msg("bootstrap owner account created — save these credentials, they are shown only once")
		}
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
		pipelineWorkers := effectivePipelineWorkers(s.cfg, s.deployment)
		log.Info().Int("queue_size", s.cfg.PipelineQueueSize).Int("workers", pipelineWorkers).Msg("pipeline worker started")
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
	var samlConfigs auth.SAMLConfigStore
	var samlUsers auth.SAMLUserProvisioner
	var relayStore auth.TrustedRelayStore
	if strings.TrimSpace(s.cfg.TrustedRelayOrgSlug) != "" && strings.TrimSpace(s.cfg.TrustedRelayID) != "" && strings.TrimSpace(s.cfg.TrustedRelaySecret) != "" {
		if org, err := s.authStore.ResolveOrganizationBySlug(context.Background(), strings.TrimSpace(s.cfg.TrustedRelayOrgSlug)); err == nil && org != nil {
			store := auth.NewMemoryTrustedRelayStore()
			if err := store.UpsertTrustedRelay(context.Background(), &auth.TrustedRelay{
				OrganizationID:   org.ID,
				OrganizationSlug: org.Slug,
				RelayID:          strings.TrimSpace(s.cfg.TrustedRelayID),
				Secret:           strings.TrimSpace(s.cfg.TrustedRelaySecret),
			}); err == nil {
				relayStore = store
			}
		}
	}
	if strings.TrimSpace(s.cfg.SAMLOrgSlug) != "" && strings.TrimSpace(s.cfg.SAMLIdPEntityID) != "" && strings.TrimSpace(s.cfg.SAMLSSOURL) != "" && strings.TrimSpace(s.cfg.SAMLCertificatePEM) != "" {
		if org, err := s.authStore.ResolveOrganizationBySlug(context.Background(), strings.TrimSpace(s.cfg.SAMLOrgSlug)); err == nil && org != nil {
			if provisioner, ok := s.control.services.Admin.(auth.SAMLUserProvisioner); ok {
				store := auth.NewMemorySAMLConfigStore()
				metadataURL := strings.TrimRight(s.cfg.BaseURL, "/") + "/auth/saml/" + strings.TrimSpace(s.cfg.SAMLOrgSlug) + "/metadata"
				acsURL := strings.TrimSpace(s.cfg.SAMLACSURL)
				if acsURL == "" {
					acsURL = strings.TrimRight(s.cfg.BaseURL, "/") + "/auth/saml/" + strings.TrimSpace(s.cfg.SAMLOrgSlug) + "/acs"
				}
				spEntityID := strings.TrimSpace(s.cfg.SAMLSPEntityID)
				if spEntityID == "" {
					spEntityID = metadataURL
				}
				if err := store.UpsertSAMLConfig(context.Background(), &auth.SAMLConfig{
					OrganizationID: org.ID,
					EntityID:       strings.TrimSpace(s.cfg.SAMLIdPEntityID),
					SSOURL:         strings.TrimSpace(s.cfg.SAMLSSOURL),
					Certificate:    strings.TrimSpace(s.cfg.SAMLCertificatePEM),
					SPEntityID:     spEntityID,
					ACSURL:         acsURL,
				}); err == nil {
					samlConfigs = store
					samlUsers = provisioner
				}
			}
		}
	}

	deps := newHTTPDeps(httpDepsInput{
		db:                  s.db,
		queueDB:             s.queueDB,
		dataDir:             s.dataDir,
		keyStore:            s.keyStore,
		authStore:           s.authStore,
		relayStore:          relayStore,
		samlConfigs:         samlConfigs,
		samlUsers:           samlUsers,
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
		preprodArtifacts:    s.preprodArtifacts,
		autofix:             s.autofixStore,
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
	handler, err := ghttp.BuildServer(string(s.role), s.cfg, deps)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           handler,
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
