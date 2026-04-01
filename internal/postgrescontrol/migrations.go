// Package postgrescontrol defines the serious self-hosted control-plane schema.
package postgrescontrol

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "tenancy-and-auth",
		SQL: `
CREATE TABLE IF NOT EXISTS organizations (
	id TEXT PRIMARY KEY,
	slug TEXT UNIQUE NOT NULL,
	name TEXT NOT NULL,
	plan TEXT NOT NULL DEFAULT 'tiny',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS teams (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	slug TEXT NOT NULL,
	name TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(organization_id, slug)
);

CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	email TEXT UNIQUE NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_password_credentials (
	user_id TEXT PRIMARY KEY REFERENCES users(id),
	password_hash TEXT NOT NULL,
	password_algo TEXT NOT NULL DEFAULT 'bcrypt',
	password_updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS organization_members (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	role TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(organization_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_org_members_user ON organization_members(user_id);

CREATE TABLE IF NOT EXISTS team_members (
	id TEXT PRIMARY KEY,
	team_id TEXT NOT NULL REFERENCES teams(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	role TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(team_id, user_id)
);

CREATE TABLE IF NOT EXISTS member_invites (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	team_id TEXT REFERENCES teams(id),
	email TEXT NOT NULL,
	role TEXT NOT NULL,
	token_prefix TEXT UNIQUE NOT NULL,
	token_hash TEXT UNIQUE NOT NULL,
	created_by_user_id TEXT REFERENCES users(id),
	expires_at TIMESTAMPTZ NOT NULL,
	accepted_at TIMESTAMPTZ,
	revoked_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS projects (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	team_id TEXT REFERENCES teams(id),
	slug TEXT NOT NULL,
	name TEXT NOT NULL,
	platform TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	default_environment TEXT DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(organization_id, slug)
);

CREATE TABLE IF NOT EXISTS project_keys (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	public_key TEXT UNIQUE NOT NULL,
	secret_key TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	label TEXT NOT NULL DEFAULT '',
	rate_limit_per_minute INTEGER,
	last_used_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_project_keys_project ON project_keys(project_id, created_at DESC);

CREATE TABLE IF NOT EXISTS project_automation_tokens (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	label TEXT NOT NULL,
	token_prefix TEXT UNIQUE NOT NULL,
	token_hash TEXT UNIQUE NOT NULL,
	scopes_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	created_by_user_id TEXT REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_used_at TIMESTAMPTZ,
	expires_at TIMESTAMPTZ,
	revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_project_automation_tokens_project ON project_automation_tokens(project_id, created_at DESC);

CREATE TABLE IF NOT EXISTS user_sessions (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id),
	session_token_hash TEXT UNIQUE NOT NULL,
	csrf_secret TEXT NOT NULL,
	ip_address TEXT DEFAULT '',
	user_agent TEXT DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL,
	revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS personal_access_tokens (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id),
	label TEXT NOT NULL,
	token_prefix TEXT UNIQUE NOT NULL,
	token_hash TEXT UNIQUE NOT NULL,
	scopes_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_used_at TIMESTAMPTZ,
	expires_at TIMESTAMPTZ,
	revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_personal_access_tokens_user ON personal_access_tokens(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS auth_audit_logs (
	id TEXT PRIMARY KEY,
	credential_type TEXT NOT NULL,
	credential_id TEXT DEFAULT '',
	user_id TEXT DEFAULT '',
	project_id TEXT DEFAULT '',
	organization_id TEXT DEFAULT '',
	action TEXT NOT NULL,
	request_path TEXT DEFAULT '',
	request_method TEXT DEFAULT '',
	ip_address TEXT DEFAULT '',
	user_agent TEXT DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_auth_audit_logs_created ON auth_audit_logs(created_at DESC);
`,
	},
	{
		Version: 2,
		Name:    "workflow-release-alerts-monitors",
		SQL: `
CREATE TABLE IF NOT EXISTS groups (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	grouping_version TEXT NOT NULL,
	grouping_key TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	culprit TEXT NOT NULL DEFAULT '',
	level TEXT NOT NULL DEFAULT 'error',
	status TEXT NOT NULL DEFAULT 'unresolved',
	substatus TEXT NOT NULL DEFAULT '',
	resolved_in_release TEXT NOT NULL DEFAULT '',
	merged_into_group_id TEXT NOT NULL DEFAULT '',
	first_seen TIMESTAMPTZ,
	last_seen TIMESTAMPTZ,
	times_seen BIGINT NOT NULL DEFAULT 0,
	last_event_id TEXT NOT NULL DEFAULT '',
	assignee_user_id TEXT DEFAULT '',
	assignee_team_id TEXT DEFAULT '',
	short_id BIGINT NOT NULL DEFAULT 0,
	priority INTEGER NOT NULL DEFAULT 2,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(project_id, grouping_version, grouping_key)
);
CREATE INDEX IF NOT EXISTS idx_groups_project_status ON groups(project_id, status, last_seen DESC);

CREATE TABLE IF NOT EXISTS group_states (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL UNIQUE REFERENCES groups(id),
	is_resolved BOOLEAN NOT NULL DEFAULT FALSE,
	is_ignored BOOLEAN NOT NULL DEFAULT FALSE,
	is_muted BOOLEAN NOT NULL DEFAULT FALSE,
	resolved_at TIMESTAMPTZ,
	resolved_by_user_id TEXT DEFAULT '',
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_occurrences (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL REFERENCES groups(id),
	event_id TEXT NOT NULL,
	occurred_at TIMESTAMPTZ NOT NULL,
	UNIQUE(group_id, event_id)
);
CREATE INDEX IF NOT EXISTS idx_group_occurrences_group_time ON group_occurrences(group_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS issue_comments (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL REFERENCES groups(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	body TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS issue_activity (
	id TEXT PRIMARY KEY,
	group_id TEXT NOT NULL REFERENCES groups(id),
	user_id TEXT REFERENCES users(id),
	kind TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_issue_activity_group ON issue_activity(group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS issue_bookmarks (
	group_id TEXT NOT NULL REFERENCES groups(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS issue_subscriptions (
	group_id TEXT NOT NULL REFERENCES groups(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS ownership_rules (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	name TEXT NOT NULL,
	pattern TEXT NOT NULL,
	assignee TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS releases (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	version TEXT NOT NULL,
	ref TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL DEFAULT '',
	date_released TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(organization_id, version)
);

CREATE TABLE IF NOT EXISTS release_projects (
	id TEXT PRIMARY KEY,
	release_id TEXT NOT NULL REFERENCES releases(id),
	project_id TEXT NOT NULL REFERENCES projects(id),
	new_groups INTEGER NOT NULL DEFAULT 0,
	resolved_groups INTEGER NOT NULL DEFAULT 0,
	UNIQUE(release_id, project_id)
);

CREATE TABLE IF NOT EXISTS release_deploys (
	id TEXT PRIMARY KEY,
	release_id TEXT NOT NULL REFERENCES releases(id),
	environment TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL DEFAULT '',
	date_started TIMESTAMPTZ,
	date_finished TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS release_commits (
	id TEXT PRIMARY KEY,
	release_id TEXT NOT NULL REFERENCES releases(id),
	commit_sha TEXT NOT NULL,
	repository TEXT NOT NULL DEFAULT '',
	author_name TEXT NOT NULL DEFAULT '',
	author_email TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	files_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(release_id, commit_sha)
);

CREATE TABLE IF NOT EXISTS release_sessions (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	release TEXT NOT NULL,
	environment TEXT NOT NULL,
	dist TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL,
	status TEXT NOT NULL,
	started_at TIMESTAMPTZ,
	duration_ms BIGINT NOT NULL DEFAULT 0,
	errors INTEGER NOT NULL DEFAULT 0,
	sequence INTEGER NOT NULL DEFAULT 0,
	timestamp TIMESTAMPTZ NOT NULL,
	user_identifier TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(project_id, release, environment, session_id, sequence)
);

CREATE TABLE IF NOT EXISTS alert_rules (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	name TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active',
	rule_type TEXT NOT NULL DEFAULT 'issue_first_seen',
	config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alert_history (
	id TEXT PRIMARY KEY,
	rule_id TEXT NOT NULL REFERENCES alert_rules(id),
	group_id TEXT DEFAULT '',
	event_id TEXT DEFAULT '',
	fired_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_alert_history_fired ON alert_history(fired_at DESC);

CREATE TABLE IF NOT EXISTS notification_outbox (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	rule_id TEXT NOT NULL REFERENCES alert_rules(id),
	group_id TEXT DEFAULT '',
	event_id TEXT DEFAULT '',
	recipient TEXT NOT NULL,
	subject TEXT NOT NULL,
	body TEXT NOT NULL,
	transport TEXT NOT NULL DEFAULT 'tiny-outbox',
	status TEXT NOT NULL DEFAULT 'queued',
	error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	sent_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	rule_id TEXT NOT NULL REFERENCES alert_rules(id),
	group_id TEXT DEFAULT '',
	event_id TEXT DEFAULT '',
	kind TEXT NOT NULL,
	target TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	response_status INTEGER,
	error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_attempt_at TIMESTAMPTZ,
	delivered_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS monitors (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	slug TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	schedule_type TEXT NOT NULL DEFAULT 'crontab',
	schedule_value TEXT NOT NULL DEFAULT '',
	checkin_margin_seconds INTEGER NOT NULL DEFAULT 0,
	max_runtime_seconds INTEGER NOT NULL DEFAULT 0,
	timezone TEXT NOT NULL DEFAULT 'UTC',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(project_id, slug)
);

CREATE TABLE IF NOT EXISTS monitor_checkins (
	id TEXT PRIMARY KEY,
	monitor_id TEXT NOT NULL REFERENCES monitors(id),
	project_id TEXT NOT NULL REFERENCES projects(id),
	checkin_id TEXT NOT NULL,
	status TEXT NOT NULL,
	duration_ms BIGINT NOT NULL DEFAULT 0,
	environment TEXT NOT NULL DEFAULT '',
	occurred_at TIMESTAMPTZ NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(monitor_id, checkin_id)
);
CREATE INDEX IF NOT EXISTS idx_monitor_checkins_monitor_time ON monitor_checkins(monitor_id, occurred_at DESC);
`,
	},
	{
		Version: 3,
		Name:    "settings-dashboards-backfills-audit",
		SQL: `
CREATE TABLE IF NOT EXISTS telemetry_retention_policies (
	project_id TEXT NOT NULL REFERENCES projects(id),
	surface TEXT NOT NULL,
	retention_days INTEGER NOT NULL,
	storage_tier TEXT NOT NULL DEFAULT 'hot',
	archive_retention_days INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY(project_id, surface)
);

CREATE TABLE IF NOT EXISTS query_guard_policies (
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	workload TEXT NOT NULL,
	max_cost_per_request INTEGER NOT NULL,
	max_requests_per_window INTEGER NOT NULL,
	max_cost_per_window INTEGER NOT NULL,
	window_seconds INTEGER NOT NULL DEFAULT 300,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY(organization_id, workload)
);

CREATE TABLE IF NOT EXISTS project_replay_configs (
	project_id TEXT PRIMARY KEY REFERENCES projects(id),
	sample_rate DOUBLE PRECISION NOT NULL DEFAULT 1.0,
	max_bytes BIGINT NOT NULL DEFAULT 10485760,
	scrub_fields_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	scrub_selectors_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS saved_searches (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	user_id TEXT REFERENCES users(id),
	name TEXT NOT NULL,
	filter TEXT NOT NULL DEFAULT 'all',
	sort TEXT NOT NULL DEFAULT 'last_seen',
	query_version INTEGER NOT NULL DEFAULT 1,
	query_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dashboards (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	owner_user_id TEXT NOT NULL REFERENCES users(id),
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	visibility TEXT NOT NULL DEFAULT 'private',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dashboard_widgets (
	id TEXT PRIMARY KEY,
	dashboard_id TEXT NOT NULL REFERENCES dashboards(id),
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	position INTEGER NOT NULL DEFAULT 0,
	width INTEGER NOT NULL DEFAULT 4,
	height INTEGER NOT NULL DEFAULT 3,
	saved_search_id TEXT NOT NULL DEFAULT '',
	query_version INTEGER NOT NULL DEFAULT 1,
	query_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS backfill_runs (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	project_id TEXT REFERENCES projects(id),
	release_version TEXT NOT NULL DEFAULT '',
	debug_file_id TEXT NOT NULL DEFAULT '',
	started_after TIMESTAMPTZ,
	ended_before TIMESTAMPTZ,
	cursor_checkpoint TEXT NOT NULL DEFAULT '',
	total_items INTEGER NOT NULL DEFAULT 0,
	processed_items INTEGER NOT NULL DEFAULT 0,
	updated_items INTEGER NOT NULL DEFAULT 0,
	failed_items INTEGER NOT NULL DEFAULT 0,
	requested_by_user_id TEXT REFERENCES users(id),
	requested_via TEXT NOT NULL DEFAULT '',
	fencing_token TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	started_at TIMESTAMPTZ,
	finished_at TIMESTAMPTZ,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_backfill_runs_org_created ON backfill_runs(organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS imports (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	source_kind TEXT NOT NULL,
	status TEXT NOT NULL,
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	archive_object_key TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS exports (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	status TEXT NOT NULL,
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	archive_object_key TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id),
	actor_user_id TEXT REFERENCES users(id),
	action TEXT NOT NULL,
	target_type TEXT NOT NULL,
	target_id TEXT NOT NULL,
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_org_created ON audit_logs(organization_id, created_at DESC);
`,
	},
	{
		Version: 4,
		Name:    "install-state",
		SQL: `
CREATE TABLE IF NOT EXISTS install_state (
	scope TEXT PRIMARY KEY,
	install_id TEXT NOT NULL DEFAULT '',
	region TEXT NOT NULL DEFAULT '',
	environment TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	bootstrap_completed BOOLEAN NOT NULL DEFAULT false,
	bootstrap_completed_at TIMESTAMPTZ,
	maintenance_mode BOOLEAN NOT NULL DEFAULT false,
	maintenance_reason TEXT NOT NULL DEFAULT '',
	maintenance_started_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`,
	},
	{
		Version: 5,
		Name:    "operator-audit-ledger",
		SQL: `
CREATE TABLE IF NOT EXISTS operator_audit_logs (
	id TEXT PRIMARY KEY,
	organization_id TEXT REFERENCES organizations(id),
	project_id TEXT REFERENCES projects(id),
	action TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'succeeded',
	source TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_created ON operator_audit_logs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_org_created ON operator_audit_logs(organization_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_project_created ON operator_audit_logs(project_id, created_at DESC, id DESC);
`,
	},
	{
		Version: 6,
		Name:    "metric-alert-rules",
		SQL: `
CREATE TABLE IF NOT EXISTS metric_alert_rules (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	name TEXT NOT NULL,
	metric TEXT NOT NULL DEFAULT 'error_count',
	threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
	threshold_type TEXT NOT NULL DEFAULT 'above',
	time_window_secs INTEGER NOT NULL DEFAULT 300,
	resolve_threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
	environment TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	trigger_actions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
	state TEXT NOT NULL DEFAULT 'ok',
	last_triggered_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_metric_alert_rules_project
	ON metric_alert_rules(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_metric_alert_rules_project_status
	ON metric_alert_rules(project_id, status, state);
`,
	},
	{
		Version: 7,
		Name:    "project-memberships",
		SQL: `
CREATE TABLE IF NOT EXISTS project_memberships (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id),
	user_id TEXT NOT NULL REFERENCES users(id),
	role TEXT NOT NULL DEFAULT 'member',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(project_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_project_memberships_project
	ON project_memberships(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_project_memberships_user
	ON project_memberships(user_id);

-- Default existing org members into all org projects as "member".
INSERT INTO project_memberships (id, project_id, user_id, role, created_at)
SELECT
	gen_random_uuid()::text,
	p.id,
	om.user_id,
	'member',
	now()
FROM organization_members om
JOIN projects p ON p.organization_id = om.organization_id
ON CONFLICT (project_id, user_id) DO NOTHING;
`,
	},
}

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres control plane: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres control plane: %w", err)
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func AllMigrations() []Migration {
	out := make([]Migration, len(migrations))
	copy(out, migrations)
	return out
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS _control_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("create control migrations table: %w", err)
	}

	for _, migration := range migrations {
		var exists int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM _control_migrations WHERE version = $1", migration.Version).Scan(&exists); err != nil {
			return fmt.Errorf("check control migration %d: %w", migration.Version, err)
		}
		if exists > 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin control migration %d: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply control migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO _control_migrations (version, name) VALUES ($1, $2)", migration.Version, migration.Name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record control migration %d: %w", migration.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit control migration %d: %w", migration.Version, err)
		}
	}
	return nil
}
