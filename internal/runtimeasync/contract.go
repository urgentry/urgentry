package runtimeasync

// Stream names the durable stream family in the serious self-hosted runtime.
type Stream string

const (
	StreamIngest      Stream = "URGENTRY_INGEST"
	StreamProjectors  Stream = "URGENTRY_PROJECTORS"
	StreamWorkflow    Stream = "URGENTRY_WORKFLOW"
	StreamOperations  Stream = "URGENTRY_OPERATIONS"
	StreamDeadLetter  Stream = "URGENTRY_DLQ"
)

// Family groups related async work items.
type Family string

const (
	FamilyIngest     Family = "ingest"
	FamilyNormalize  Family = "normalize"
	FamilyIssues     Family = "issues"
	FamilyAlerts     Family = "alerts"
	FamilyArtifacts  Family = "artifacts"
	FamilyOperations Family = "operations"
	FamilyBridge     Family = "bridge"
)

// Subject names the published work subject.
type Subject string

const (
	SubjectIngestEnvelopeReceived Subject = "urgentry.ingest.envelope.received"
	SubjectIngestStoreReceived    Subject = "urgentry.ingest.store.received"
	SubjectIngestSecurityReceived Subject = "urgentry.ingest.security.received"
	SubjectIngestOTLPLogs         Subject = "urgentry.ingest.otlp.logs.received"
	SubjectIngestOTLPTraces       Subject = "urgentry.ingest.otlp.traces.received"

	SubjectNormalizeEvent       Subject = "urgentry.normalize.event"
	SubjectNormalizeReplay      Subject = "urgentry.normalize.replay"
	SubjectNormalizeProfile     Subject = "urgentry.normalize.profile"
	SubjectNormalizeTransaction Subject = "urgentry.normalize.transaction"
	SubjectNormalizeLog         Subject = "urgentry.normalize.log"
	SubjectNormalizeMonitor     Subject = "urgentry.normalize.monitor"
	SubjectNormalizeOutcome     Subject = "urgentry.normalize.outcome"

	SubjectIssuesGroup      Subject = "urgentry.issues.group"
	SubjectIssuesUpdate     Subject = "urgentry.issues.update"
	SubjectIssuesRegression Subject = "urgentry.issues.regression"

	SubjectAlertsEvaluate      Subject = "urgentry.alerts.evaluate"
	SubjectAlertsNotifyEmail   Subject = "urgentry.alerts.notify.email"
	SubjectAlertsNotifyWebhook Subject = "urgentry.alerts.notify.webhook"
	SubjectAlertsNotifySlack   Subject = "urgentry.alerts.notify.slack"

	SubjectArtifactsProcessSourceMap Subject = "urgentry.artifacts.process.sourcemap"
	SubjectArtifactsProcessProGuard  Subject = "urgentry.artifacts.process.proguard"
	SubjectArtifactsProcessDebugFile Subject = "urgentry.artifacts.process.debugfile"
	SubjectNativeReprocess           Subject = "urgentry.native.reprocess"

	SubjectMaintenanceRetention      Subject = "urgentry.maintenance.retention"
	SubjectMaintenanceBackfill       Subject = "urgentry.maintenance.backfill"
	SubjectMaintenanceRebuildReplay  Subject = "urgentry.maintenance.rebuild.replay"
	SubjectMaintenanceRebuildProfile Subject = "urgentry.maintenance.rebuild.profile"
	SubjectMaintenanceRebuildBridge  Subject = "urgentry.maintenance.rebuild.telemetry"
	SubjectBridgeProjection          Subject = "urgentry.bridge.projection"

	SubjectDLQIngest     Subject = "urgentry.dlq.ingest"
	SubjectDLQNormalize  Subject = "urgentry.dlq.normalize"
	SubjectDLQIssues     Subject = "urgentry.dlq.issues"
	SubjectDLQAlerts     Subject = "urgentry.dlq.alerts"
	SubjectDLQArtifacts  Subject = "urgentry.dlq.artifacts"
	SubjectDLQOperations Subject = "urgentry.dlq.operations"
	SubjectDLQBridge     Subject = "urgentry.dlq.bridge"
)

// Envelope is the canonical serious self-hosted runtime payload.
type Envelope struct {
	MessageID      string `json:"messageId"`
	Family         Family `json:"family"`
	Kind           string `json:"kind"`
	OrganizationID string `json:"organizationId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	ObjectRef      string `json:"objectRef,omitempty"`
	ControlRef     string `json:"controlRef,omitempty"`
	DedupeKey      string `json:"dedupeKey,omitempty"`
	Attempt        int    `json:"attempt"`
	EnqueuedAt     string `json:"enqueuedAt"`
	NotBefore      string `json:"notBefore,omitempty"`
	TraceID        string `json:"traceId,omitempty"`
	ReplayID       string `json:"replayId,omitempty"`
	ProfileID      string `json:"profileId,omitempty"`
	EventID        string `json:"eventId,omitempty"`
	GroupID        string `json:"groupId,omitempty"`
	ReleaseVersion string `json:"releaseVersion,omitempty"`
	Scope          string `json:"scope,omitempty"`
	PayloadJSON    []byte `json:"payloadJson,omitempty"`
}

type LeaseName string

const (
	LeaseScheduler LeaseName = "urgentry:lease:scheduler"
	LeaseRetention LeaseName = "urgentry:lease:retention"
)

type KeyFamily string

const (
	KeyFamilyProjectRateLimit KeyFamily = "urgentry:ratelimit:project:{project_key}:{window}"
	KeyFamilyOrgQueryQuota    KeyFamily = "urgentry:quota:org:{org_id}:{workload}:{window}"
	KeyFamilyEventDedupe      KeyFamily = "urgentry:idem:event:{project_id}:{event_id}"
	KeyFamilyReplayDedupe     KeyFamily = "urgentry:idem:replay:{project_id}:{replay_id}"
	KeyFamilyProfileDedupe    KeyFamily = "urgentry:idem:profile:{project_id}:{profile_id}"
	KeyFamilyProjectConfig    KeyFamily = "urgentry:cache:projectcfg:{project_id}"
	KeyFamilyQuerySummary     KeyFamily = "urgentry:cache:query-summary:{scope}:{key}"
)

var StreamSubjects = map[Stream][]Subject{
	StreamIngest: {
		SubjectIngestEnvelopeReceived,
		SubjectIngestStoreReceived,
		SubjectIngestSecurityReceived,
		SubjectIngestOTLPLogs,
		SubjectIngestOTLPTraces,
	},
	StreamProjectors: {
		SubjectNormalizeEvent,
		SubjectNormalizeReplay,
		SubjectNormalizeProfile,
		SubjectNormalizeTransaction,
		SubjectNormalizeLog,
		SubjectNormalizeMonitor,
		SubjectNormalizeOutcome,
		SubjectBridgeProjection,
	},
	StreamWorkflow: {
		SubjectIssuesGroup,
		SubjectIssuesUpdate,
		SubjectIssuesRegression,
		SubjectAlertsEvaluate,
		SubjectAlertsNotifyEmail,
		SubjectAlertsNotifyWebhook,
		SubjectAlertsNotifySlack,
	},
	StreamOperations: {
		SubjectArtifactsProcessSourceMap,
		SubjectArtifactsProcessProGuard,
		SubjectArtifactsProcessDebugFile,
		SubjectNativeReprocess,
		SubjectMaintenanceRetention,
		SubjectMaintenanceBackfill,
		SubjectMaintenanceRebuildReplay,
		SubjectMaintenanceRebuildProfile,
		SubjectMaintenanceRebuildBridge,
	},
	StreamDeadLetter: {
		SubjectDLQIngest,
		SubjectDLQNormalize,
		SubjectDLQIssues,
		SubjectDLQAlerts,
		SubjectDLQArtifacts,
		SubjectDLQOperations,
		SubjectDLQBridge,
	},
}

var DeadLetterSubjectByFamily = map[Family]Subject{
	FamilyIngest:     SubjectDLQIngest,
	FamilyNormalize:  SubjectDLQNormalize,
	FamilyIssues:     SubjectDLQIssues,
	FamilyAlerts:     SubjectDLQAlerts,
	FamilyArtifacts:  SubjectDLQArtifacts,
	FamilyOperations: SubjectDLQOperations,
	FamilyBridge:     SubjectDLQBridge,
}
