package sqlite

// migrationsOperator contains schema for telemetry retention, query guard,
// backfill runs, install state, and operator audit logs.
var migrationsOperator = []schemaMigration{
	{24, `
		CREATE TABLE IF NOT EXISTS telemetry_retention_policies (
			project_id TEXT NOT NULL REFERENCES projects(id),
			surface TEXT NOT NULL,
			retention_days INTEGER NOT NULL,
			storage_tier TEXT NOT NULL DEFAULT 'hot',
			archive_retention_days INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (project_id, surface)
		);
		CREATE INDEX IF NOT EXISTS idx_telemetry_retention_surface ON telemetry_retention_policies(surface, updated_at DESC);

		CREATE TABLE IF NOT EXISTS telemetry_archives (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			surface TEXT NOT NULL,
			record_type TEXT NOT NULL,
			record_id TEXT NOT NULL,
			archive_key TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			archived_at TEXT DEFAULT (datetime('now')),
			restored_at TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_telemetry_archives_project_surface ON telemetry_archives(project_id, surface, archived_at DESC);
		CREATE INDEX IF NOT EXISTS idx_telemetry_archives_record ON telemetry_archives(record_type, record_id, archived_at DESC);
	`},
	{25, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_telemetry_archives_active_record
			ON telemetry_archives(project_id, record_type, record_id)
			WHERE restored_at IS NULL;
	`},
	{26, `
		CREATE TABLE IF NOT EXISTS query_guard_policies (
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			workload TEXT NOT NULL,
			max_cost_per_request INTEGER NOT NULL,
			max_requests_per_window INTEGER NOT NULL,
			max_cost_per_window INTEGER NOT NULL,
			window_seconds INTEGER NOT NULL DEFAULT 300,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (organization_id, workload)
		);

		CREATE TABLE IF NOT EXISTS query_guard_usage (
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			workload TEXT NOT NULL,
			actor_key TEXT NOT NULL,
			window_start TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 0,
			cost_units INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (organization_id, workload, actor_key, window_start)
		);
		CREATE INDEX IF NOT EXISTS idx_query_guard_usage_window
			ON query_guard_usage(organization_id, workload, window_start DESC);
	`},
	{27, `
			CREATE TABLE IF NOT EXISTS backfill_runs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			project_id TEXT REFERENCES projects(id),
			release_version TEXT,
			started_after TEXT,
			ended_before TEXT,
			cursor_rowid INTEGER NOT NULL DEFAULT 0,
			total_items INTEGER NOT NULL DEFAULT 0,
			processed_items INTEGER NOT NULL DEFAULT 0,
			updated_items INTEGER NOT NULL DEFAULT 0,
			failed_items INTEGER NOT NULL DEFAULT 0,
			requested_by_user_id TEXT REFERENCES users(id),
			requested_via TEXT DEFAULT '',
			lease_until TEXT,
			worker_id TEXT,
			last_error TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			started_at TEXT,
			finished_at TEXT,
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_backfill_runs_org_created
			ON backfill_runs(organization_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_backfill_runs_status_lease
				ON backfill_runs(status, lease_until, created_at);
		`},
	{28, `
			ALTER TABLE backfill_runs ADD COLUMN debug_file_id TEXT;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_backfill_runs_active_unique
				ON backfill_runs(
					kind,
					organization_id,
					COALESCE(project_id, ''),
					COALESCE(release_version, ''),
					COALESCE(debug_file_id, ''),
					COALESCE(started_after, ''),
					COALESCE(ended_before, '')
				)
				WHERE status IN ('pending', 'running');
		`},
	{40, `
			CREATE TABLE IF NOT EXISTS install_state (
				scope TEXT PRIMARY KEY,
				install_id TEXT NOT NULL DEFAULT '',
				region TEXT NOT NULL DEFAULT '',
				environment TEXT NOT NULL DEFAULT '',
				version TEXT NOT NULL DEFAULT '',
				bootstrap_completed INTEGER NOT NULL DEFAULT 0,
				bootstrap_completed_at TEXT NOT NULL DEFAULT '',
				maintenance_mode INTEGER NOT NULL DEFAULT 0,
				maintenance_reason TEXT NOT NULL DEFAULT '',
				maintenance_started_at TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
		`},
	{41, `
			CREATE TABLE IF NOT EXISTS operator_audit_logs (
				id TEXT PRIMARY KEY,
				organization_id TEXT,
				project_id TEXT,
				action TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'succeeded',
				source TEXT NOT NULL DEFAULT '',
				actor TEXT NOT NULL DEFAULT '',
				detail TEXT NOT NULL DEFAULT '',
				metadata_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_created
				ON operator_audit_logs(created_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_org_created
				ON operator_audit_logs(organization_id, created_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_operator_audit_logs_project_created
				ON operator_audit_logs(project_id, created_at DESC, id DESC);
		`},
}
