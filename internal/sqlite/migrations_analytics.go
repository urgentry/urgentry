package sqlite

// migrationsAnalytics contains schema for saved searches, dashboards,
// dashboard widgets, analytics snapshots, and report schedules.
var migrationsAnalytics = []schemaMigration{
	{7, `
		CREATE TABLE IF NOT EXISTS saved_searches (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			query TEXT DEFAULT '',
			filter TEXT DEFAULT 'all',
			environment TEXT DEFAULT '',
			sort TEXT DEFAULT 'last_seen',
			created_at TEXT DEFAULT (datetime('now'))
		);
	`},
	{32, `
			ALTER TABLE saved_searches ADD COLUMN query_version INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE saved_searches ADD COLUMN query_json TEXT NOT NULL DEFAULT '';
		`},
	{33, `
			CREATE TABLE IF NOT EXISTS dashboards (
				id TEXT PRIMARY KEY,
				organization_id TEXT NOT NULL REFERENCES organizations(id),
				owner_user_id TEXT NOT NULL REFERENCES users(id),
				title TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				visibility TEXT NOT NULL DEFAULT 'private',
				created_at TEXT DEFAULT (datetime('now')),
				updated_at TEXT DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_dashboards_org_visibility
				ON dashboards(organization_id, visibility, updated_at DESC, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_dashboards_org_owner
				ON dashboards(organization_id, owner_user_id, updated_at DESC, created_at DESC);

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
				query_version INTEGER NOT NULL DEFAULT 0,
				query_json TEXT NOT NULL DEFAULT '',
				config_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT DEFAULT (datetime('now')),
				updated_at TEXT DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_dashboard_widgets_dashboard_position
				ON dashboard_widgets(dashboard_id, position ASC, created_at ASC);
		`},
	{36, `
			ALTER TABLE saved_searches ADD COLUMN organization_slug TEXT NOT NULL DEFAULT '';
			ALTER TABLE saved_searches ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private';
			UPDATE saved_searches
			SET organization_slug = COALESCE(
				NULLIF(organization_slug, ''),
				(SELECT COALESCE(
					(SELECT slug FROM organizations ORDER BY COALESCE(created_at, ''), slug ASC LIMIT 1),
					'default-org'
				))
			)
			WHERE organization_slug = '';
			CREATE INDEX IF NOT EXISTS idx_saved_searches_owner_created
				ON saved_searches(user_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_saved_searches_org_visibility_created
				ON saved_searches(organization_slug, visibility, created_at DESC);
		`},
	{37, `
			ALTER TABLE saved_searches ADD COLUMN description TEXT NOT NULL DEFAULT '';
			ALTER TABLE saved_searches ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
			UPDATE saved_searches
			SET updated_at = COALESCE(NULLIF(updated_at, ''), created_at)
			WHERE updated_at = '';
			CREATE TABLE IF NOT EXISTS saved_search_favorites (
				saved_search_id TEXT NOT NULL REFERENCES saved_searches(id) ON DELETE CASCADE,
				user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				PRIMARY KEY (saved_search_id, user_id)
			);
			CREATE INDEX IF NOT EXISTS idx_saved_search_favorites_user_created
				ON saved_search_favorites(user_id, created_at DESC);
		`},
	{38, `
			CREATE TABLE IF NOT EXISTS saved_search_tags (
				saved_search_id TEXT NOT NULL REFERENCES saved_searches(id) ON DELETE CASCADE,
				tag TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				PRIMARY KEY (saved_search_id, tag)
			);
			CREATE INDEX IF NOT EXISTS idx_saved_search_tags_saved_search
				ON saved_search_tags(saved_search_id, tag);
		`},
	{39, `
			CREATE TABLE IF NOT EXISTS analytics_snapshots (
				id TEXT PRIMARY KEY,
				organization_slug TEXT NOT NULL DEFAULT '',
				source_type TEXT NOT NULL DEFAULT '',
				source_id TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				share_token TEXT NOT NULL UNIQUE,
				payload_json TEXT NOT NULL DEFAULT '{}',
				created_by_user_id TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				expires_at TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX IF NOT EXISTS idx_analytics_snapshots_share_token
				ON analytics_snapshots(share_token);
			CREATE INDEX IF NOT EXISTS idx_analytics_snapshots_expires_at
				ON analytics_snapshots(expires_at);
			CREATE INDEX IF NOT EXISTS idx_analytics_snapshots_org_source
				ON analytics_snapshots(organization_slug, source_type, source_id, created_at DESC);
		`},
	{42, `
			ALTER TABLE dashboards ADD COLUMN config_json TEXT NOT NULL DEFAULT '{}';
			UPDATE dashboards
			SET config_json = COALESCE(NULLIF(config_json, ''), '{}')
			WHERE config_json = '';
		`},
	{43, `
			CREATE TABLE IF NOT EXISTS analytics_report_schedules (
				id TEXT PRIMARY KEY,
				organization_slug TEXT NOT NULL DEFAULT '',
				source_type TEXT NOT NULL DEFAULT '',
				source_id TEXT NOT NULL DEFAULT '',
				created_by_user_id TEXT NOT NULL DEFAULT '',
				recipient TEXT NOT NULL DEFAULT '',
				cadence TEXT NOT NULL DEFAULT 'daily',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				last_attempt_at TEXT NOT NULL DEFAULT '',
				last_run_at TEXT NOT NULL DEFAULT '',
				next_run_at TEXT NOT NULL DEFAULT '',
				last_snapshot_token TEXT NOT NULL DEFAULT '',
				last_error TEXT NOT NULL DEFAULT ''
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_analytics_report_schedules_unique
				ON analytics_report_schedules(organization_slug, source_type, source_id, created_by_user_id, recipient, cadence);
			CREATE INDEX IF NOT EXISTS idx_analytics_report_schedules_next_run
				ON analytics_report_schedules(next_run_at, created_at);
			CREATE INDEX IF NOT EXISTS idx_analytics_report_schedules_source
				ON analytics_report_schedules(organization_slug, source_type, source_id, created_at DESC);
		`},
	{44, `
			CREATE TABLE IF NOT EXISTS metric_alert_rules (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL,
				name TEXT NOT NULL,
				metric TEXT NOT NULL DEFAULT 'error_count',
				threshold REAL NOT NULL DEFAULT 0,
				threshold_type TEXT NOT NULL DEFAULT 'above',
				time_window_secs INTEGER NOT NULL DEFAULT 300,
				resolve_threshold REAL NOT NULL DEFAULT 0,
				environment TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'active',
				trigger_actions_json TEXT NOT NULL DEFAULT '[]',
				state TEXT NOT NULL DEFAULT 'ok',
				last_triggered_at TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_metric_alert_rules_project
				ON metric_alert_rules(project_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_metric_alert_rules_project_status
				ON metric_alert_rules(project_id, status, state);
		`},
}
