package sqlite

// migrationsFeatures contains schema for uptime monitors, sampling rules,
// and quota management.
var migrationsFeatures = []schemaMigration{
	{47, `
		CREATE TABLE IF NOT EXISTS uptime_monitors (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			interval_seconds INTEGER NOT NULL DEFAULT 60,
			timeout_seconds INTEGER NOT NULL DEFAULT 10,
			expected_status INTEGER NOT NULL DEFAULT 200,
			environment TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			last_check_at TEXT,
			last_status_code INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			last_latency_ms REAL NOT NULL DEFAULT 0,
			consecutive_fail INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_uptime_monitors_project
			ON uptime_monitors(project_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_uptime_monitors_due
			ON uptime_monitors(status, last_check_at);

		CREATE TABLE IF NOT EXISTS uptime_check_results (
			id TEXT PRIMARY KEY,
			uptime_monitor_id TEXT NOT NULL REFERENCES uptime_monitors(id),
			project_id TEXT NOT NULL REFERENCES projects(id),
			status_code INTEGER NOT NULL DEFAULT 0,
			latency_ms REAL NOT NULL DEFAULT 0,
			error TEXT,
			status TEXT NOT NULL DEFAULT 'ok',
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_uptime_check_results_monitor
			ON uptime_check_results(uptime_monitor_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_uptime_check_results_project
			ON uptime_check_results(project_id, created_at DESC);
	`},
	{48, `
		CREATE TABLE IF NOT EXISTS sampling_rules (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			sample_rate REAL NOT NULL DEFAULT 1.0,
			conditions_json TEXT NOT NULL DEFAULT '{}',
			active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_sampling_rules_project
			ON sampling_rules(project_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sampling_rules_active
			ON sampling_rules(project_id, active, created_at DESC);
	`},
	{49, `
		CREATE TABLE IF NOT EXISTS quota_rate_limits (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL UNIQUE REFERENCES projects(id),
			max_events_per_hour INTEGER NOT NULL DEFAULT 0,
			max_transactions_per_hour INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_quota_rate_limits_project
			ON quota_rate_limits(project_id);
	`},
	{60, `
		CREATE TABLE IF NOT EXISTS metric_buckets (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			value REAL NOT NULL,
			unit TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '{}',
			timestamp TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_metric_buckets_project_name
			ON metric_buckets(project_id, name, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_metric_buckets_project_type
			ON metric_buckets(project_id, type, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_metric_buckets_project_timestamp
			ON metric_buckets(project_id, timestamp DESC);
	`},
	{52, `
		CREATE TABLE IF NOT EXISTS notification_routing_rules (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			name TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			conditions_json TEXT NOT NULL DEFAULT '[]',
			actions_json TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_notification_routing_rules_org
			ON notification_routing_rules(organization_id, priority ASC, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_notification_routing_rules_enabled
			ON notification_routing_rules(organization_id, enabled, priority ASC);
	`},
	{53, `
		CREATE TABLE IF NOT EXISTS inbound_filters (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			type TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			pattern TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_inbound_filters_project
			ON inbound_filters(project_id, type);
	`},
	{54, `
		CREATE TABLE IF NOT EXISTS schema_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT DEFAULT (datetime('now'))
		);
		INSERT OR IGNORE INTO schema_metadata (key, value) VALUES ('schema_version', '54');
	`},
}
