// Package telemetrybridge defines the serious self-hosted telemetry bridge schema.
package telemetrybridge

type Backend string

const (
	BackendPostgres  Backend = "postgres"
	BackendTimescale Backend = "timescale"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

var baseMigrations = []Migration{
	{
		Version: 1,
		Name:    "create-telemetry-schema",
		SQL: `
CREATE SCHEMA IF NOT EXISTS telemetry;

CREATE TABLE IF NOT EXISTS telemetry.event_facts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	group_id TEXT,
	event_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	release TEXT,
	environment TEXT,
	platform TEXT,
	level TEXT,
	title TEXT,
	culprit TEXT,
	occurred_at TIMESTAMPTZ NOT NULL,
	ingested_at TIMESTAMPTZ NOT NULL,
	search_text TEXT,
	tags_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	dimensions_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	payload_object_key TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_event_facts_project_event ON telemetry.event_facts(project_id, event_id);
CREATE INDEX IF NOT EXISTS idx_event_facts_project_time ON telemetry.event_facts(project_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_facts_org_time ON telemetry.event_facts(organization_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_facts_project_ingested_event ON telemetry.event_facts(project_id, ingested_at DESC, event_id DESC);

CREATE TABLE IF NOT EXISTS telemetry.log_facts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	trace_id TEXT,
	span_id TEXT,
	release TEXT,
	environment TEXT,
	platform TEXT,
	level TEXT,
	logger TEXT,
	message TEXT,
	search_text TEXT,
	timestamp TIMESTAMPTZ NOT NULL,
	attributes_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_log_facts_project_time ON telemetry.log_facts(project_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_log_facts_org_time ON telemetry.log_facts(organization_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_log_facts_trace ON telemetry.log_facts(trace_id, timestamp DESC);

CREATE TABLE IF NOT EXISTS telemetry.transaction_facts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	trace_id TEXT NOT NULL,
	span_id TEXT NOT NULL,
	parent_span_id TEXT,
	transaction_name TEXT NOT NULL,
	op TEXT,
	status TEXT,
	release TEXT,
	environment TEXT,
	started_at TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ NOT NULL,
	duration_ms DOUBLE PRECISION NOT NULL,
	measurements_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	tags_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_transaction_facts_project_event ON telemetry.transaction_facts(project_id, event_id);
CREATE INDEX IF NOT EXISTS idx_transaction_facts_trace ON telemetry.transaction_facts(trace_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_transaction_facts_project_time ON telemetry.transaction_facts(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_transaction_facts_org_time ON telemetry.transaction_facts(organization_id, started_at DESC);

CREATE TABLE IF NOT EXISTS telemetry.span_facts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	transaction_event_id TEXT NOT NULL,
	trace_id TEXT NOT NULL,
	span_id TEXT NOT NULL,
	parent_span_id TEXT,
	op TEXT,
	description TEXT,
	status TEXT,
	started_at TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ NOT NULL,
	duration_ms DOUBLE PRECISION NOT NULL,
	tags_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	data_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_span_facts_trace ON telemetry.span_facts(trace_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_span_facts_project_time ON telemetry.span_facts(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_span_facts_transaction ON telemetry.span_facts(transaction_event_id, started_at DESC);

CREATE TABLE IF NOT EXISTS telemetry.outcome_facts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	outcome TEXT NOT NULL,
	reason TEXT,
	category TEXT NOT NULL,
	quantity BIGINT NOT NULL,
	recorded_at TIMESTAMPTZ NOT NULL,
	source TEXT NOT NULL DEFAULT 'client_report'
);
CREATE INDEX IF NOT EXISTS idx_outcome_facts_project_time ON telemetry.outcome_facts(project_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_outcome_facts_org_time ON telemetry.outcome_facts(organization_id, recorded_at DESC);

CREATE TABLE IF NOT EXISTS telemetry.replay_manifests (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	replay_id TEXT NOT NULL,
	event_id TEXT,
	trace_id TEXT,
	release TEXT,
	environment TEXT,
	started_at TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ,
	duration_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
	segment_count INTEGER NOT NULL DEFAULT 0,
	error_count INTEGER NOT NULL DEFAULT 0,
	click_count INTEGER NOT NULL DEFAULT 0,
	payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_replay_manifests_project_replay ON telemetry.replay_manifests(project_id, replay_id);
CREATE INDEX IF NOT EXISTS idx_replay_manifests_project_time ON telemetry.replay_manifests(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_replay_manifests_org_time ON telemetry.replay_manifests(organization_id, started_at DESC);

CREATE TABLE IF NOT EXISTS telemetry.replay_timeline_items (
	id TEXT PRIMARY KEY,
	replay_manifest_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	replay_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	timestamp TIMESTAMPTZ NOT NULL,
	offset_ms INTEGER NOT NULL,
	lane TEXT,
	payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_replay_timeline_replay_time ON telemetry.replay_timeline_items(replay_id, timestamp ASC);
CREATE INDEX IF NOT EXISTS idx_replay_timeline_project_time ON telemetry.replay_timeline_items(project_id, timestamp DESC);

CREATE TABLE IF NOT EXISTS telemetry.profile_manifests (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	profile_id TEXT NOT NULL,
	event_id TEXT,
	trace_id TEXT,
	transaction_name TEXT,
	release TEXT,
	environment TEXT,
	platform TEXT,
	started_at TIMESTAMPTZ NOT NULL,
	duration_ns BIGINT NOT NULL,
	sample_count INTEGER NOT NULL DEFAULT 0,
	thread_count INTEGER NOT NULL DEFAULT 0,
	payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_manifests_project_profile ON telemetry.profile_manifests(project_id, profile_id);
CREATE INDEX IF NOT EXISTS idx_profile_manifests_project_time ON telemetry.profile_manifests(project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_profile_manifests_org_time ON telemetry.profile_manifests(organization_id, started_at DESC);

CREATE TABLE IF NOT EXISTS telemetry.profile_samples (
	id TEXT PRIMARY KEY,
	profile_manifest_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	profile_id TEXT NOT NULL,
	thread_id TEXT NOT NULL,
	stack_id TEXT,
	elapsed_ns BIGINT NOT NULL,
	leaf_frame TEXT,
	is_active INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_profile_samples_profile_elapsed ON telemetry.profile_samples(profile_id, elapsed_ns ASC);

CREATE TABLE IF NOT EXISTS telemetry.projector_cursors (
	name TEXT PRIMARY KEY,
	cursor_family TEXT NOT NULL,
	scope_kind TEXT NOT NULL,
	scope_id TEXT NOT NULL,
	checkpoint TEXT NOT NULL,
	last_event_at TIMESTAMPTZ,
	last_error TEXT,
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_projector_cursors_family_scope ON telemetry.projector_cursors(cursor_family, scope_kind, scope_id);
`,
	},
	{
		Version: 2,
		Name:    "expand-profile-manifests",
		SQL: `
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS profile_kind TEXT;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS frame_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS function_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS stack_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS processing_status TEXT NOT NULL DEFAULT 'completed';
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS ingest_error TEXT;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS raw_blob_key TEXT;
ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
`,
	},
}

var timescaleMigrations = []Migration{
	{
		Version: 3,
		Name:    "enable-timescale-and-promote-hypertables",
		SQL: `
CREATE EXTENSION IF NOT EXISTS timescaledb;

SELECT create_hypertable('telemetry.event_facts', 'occurred_at', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.log_facts', 'timestamp', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.transaction_facts', 'started_at', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.span_facts', 'started_at', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.outcome_facts', 'recorded_at', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.replay_manifests', 'started_at', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.replay_timeline_items', 'timestamp', if_not_exists => TRUE);
SELECT create_hypertable('telemetry.profile_manifests', 'started_at', if_not_exists => TRUE);
`,
	},
}

func Migrations(backend Backend) []Migration {
	migrations := make([]Migration, 0, len(baseMigrations)+len(timescaleMigrations))
	migrations = append(migrations, baseMigrations...)
	if backend == BackendTimescale {
		migrations = append(migrations, timescaleMigrations...)
	}
	return migrations
}
