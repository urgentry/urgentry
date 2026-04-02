package app

import (
	"database/sql"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/attachment"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	ghttp "urgentry/internal/http"
	"urgentry/internal/ingest"
	"urgentry/internal/integration"
	"urgentry/internal/metrics"
	"urgentry/internal/pipeline"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/internal/web"
)

type httpDepsInput struct {
	db                  *sql.DB
	dataDir             string
	keyStore            auth.KeyStore
	authStore           auth.Store
	rateLimiter         auth.RateLimiter
	pipeline            *pipeline.Pipeline
	lifecycle           store.LifecycleStore
	control             controlplane.Services
	queryGuard          sqlite.QueryGuard
	queryService        telemetryquery.Service
	blobStore           store.BlobStore
	feedbackStore       *sqlite.FeedbackStore
	hooks               *sqlite.HookStore
	attachmentStore     attachment.Store
	outcomeStore        *sqlite.OutcomeStore
	proguardStore       proguard.Store
	sourceMapStore      sourcemap.Store
	releaseHealth       *sqlite.ReleaseHealthStore
	nativeCrashes       *sqlite.NativeCrashStore
	alertDeps           *pipeline.AlertDeps
	eventStore          store.EventStore
	replayStore         store.ReplayIngestStore
	replayPolicies      store.ReplayPolicyStore
	profileStore        store.ProfileIngestStore
	operatorStore       store.OperatorStore
	operatorAudits      store.OperatorAuditStore
	analytics           analyticsservice.Services
	principalShadows    *sqlite.PrincipalShadowStore
	backfillStore       *sqlite.BackfillStore
	auditStore          *sqlite.AuditStore
	nativeControl       *sqlite.NativeControlStore
	debugFiles          *sqlite.DebugFileStore
	retentionStore      *sqlite.RetentionStore
	importExport        *sqlite.ImportExportStore
	integrationRegistry *integration.Registry
	integrationStore    integration.Store
	sentryApps          integration.AppStore
	externalIssues      integration.ExternalIssueStore
	samplingRules       *sqlite.SamplingRuleStore
	uptimeMonitors      *sqlite.UptimeMonitorStore
	quota               *sqlite.QuotaStore
	symbolSources       *sqlite.SymbolSourceStore
	prevent             store.PreventStore
	metrics             *metrics.Metrics
	version             string
}

func newHTTPDeps(input httpDepsInput) ghttp.Deps {
	return ghttp.Deps{
		KeyStore:    input.keyStore,
		AuthStore:   input.authStore,
		RateLimiter: input.rateLimiter,
		Pipeline:    input.pipeline,
		DB:          input.db,
		Lifecycle:   input.lifecycle,
		Ingest: ingest.IngestDeps{
			Pipeline:        input.pipeline,
			AlertDeps:       input.alertDeps,
			EventStore:      input.eventStore,
			ReplayStore:     input.replayStore,
			ReplayPolicies:  input.replayPolicies,
			ProfileStore:    input.profileStore,
			FeedbackStore:   input.feedbackStore,
			AttachmentStore: input.attachmentStore,
			BlobStore:       input.blobStore,
			DebugFiles:      input.debugFiles,
			NativeCrashes:   input.nativeCrashes,
			SessionStore:    input.releaseHealth,
			OutcomeStore:    input.outcomeStore,
			MonitorStore:    input.control.Monitors,
			SamplingRules:   input.samplingRules,
			SpikeThrottle:   pipeline.NewSpikeThrottle(input.db),
			Metrics:         input.metrics,
		},
		API: api.Dependencies{
			DB:                  input.db,
			Control:             input.control,
			PrincipalShadows:    input.principalShadows,
			QueryGuard:          input.queryGuard,
			Operators:           input.operatorStore,
			OperatorAudits:      input.operatorAudits,
			Analytics:           input.analytics,
			Backfills:           input.backfillStore,
			Audits:              input.auditStore,
			NativeControl:       input.nativeControl,
			ReleaseHealth:       input.releaseHealth,
			DebugFiles:          input.debugFiles,
			Outcomes:            input.outcomeStore,
			Retention:           input.retentionStore,
			ImportExport:        input.importExport,
			Attachments:         input.attachmentStore,
			ProGuardStore:       input.proguardStore,
			SourceMapStore:      input.sourceMapStore,
			BlobStore:           input.blobStore,
			Queries:             input.queryService,
			IntegrationRegistry: input.integrationRegistry,
			IntegrationStore:    input.integrationStore,
			SentryAppStore:      input.sentryApps,
			ExternalIssues:      input.externalIssues,
			SamplingRules:       input.samplingRules,
			UptimeMonitors:      input.uptimeMonitors,
			Quota:               input.quota,
			SymbolSources:       input.symbolSources,
			Hooks:               input.hooks,
			NotificationActions: sqlite.NewNotificationActionStore(input.db),
			Detectors:           sqlite.NewDetectorStore(input.db),
			Workflows:           sqlite.NewWorkflowStore(input.db),
			ExternalUsers:       sqlite.NewExternalUserStore(input.db),
			ExternalTeams:       sqlite.NewExternalTeamStore(input.db),
			OrgForwarders:       sqlite.NewOrgForwarderStore(input.db),
			Prevent:             input.prevent,
		},
		Web: web.Dependencies{
			WebStore:       sqlite.NewWebStore(input.db),
			Replays:        input.queryService,
			Queries:        input.queryService,
			DB:             input.db,
			BlobStore:      input.blobStore,
			DataDir:        input.dataDir,
			Control:        input.control,
			Operators:      input.operatorStore,
			OperatorAudits: input.operatorAudits,
			QueryGuard:     input.queryGuard,
			NativeControl:  input.nativeControl,
			Analytics:      input.analytics,
			QuotaStore:     input.quota,
		},
		Metrics: input.metrics,
		Version: input.version,
	}
}
