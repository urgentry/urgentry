package sqlite

// migrationsCore contains schema for organizations, teams, projects, keys,
// users, auth, jobs, releases, alerts, feedback, notifications, and invites.
var migrationsCore = []schemaMigration{
	{1, `
		CREATE TABLE organizations (
			id TEXT PRIMARY KEY,
			slug TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE teams (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			slug TEXT NOT NULL,
			name TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(organization_id, slug)
		);
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			team_id TEXT REFERENCES teams(id),
			slug TEXT NOT NULL,
			name TEXT NOT NULL,
			platform TEXT,
			status TEXT DEFAULT 'active',
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(organization_id, slug)
		);
		CREATE TABLE project_keys (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			public_key TEXT UNIQUE NOT NULL,
			secret_key TEXT,
			status TEXT DEFAULT 'active',
			label TEXT DEFAULT '',
			rate_limit INTEGER,
			created_at TEXT DEFAULT (datetime('now'))
		);
	`},
	{3, `
		CREATE TABLE releases (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			version TEXT NOT NULL,
			date_released TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(organization_id, version)
		);
		CREATE TABLE alert_rules (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			config_json TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE user_feedback (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			event_id TEXT,
			group_id TEXT,
			name TEXT,
			email TEXT,
			comments TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
	`},
	{4, `
		CREATE TABLE IF NOT EXISTS alert_history (
			id TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			group_id TEXT,
			event_id TEXT,
			fired_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_alert_history_fired ON alert_history(fired_at DESC);
	`},
	{10, `
		ALTER TABLE project_keys ADD COLUMN last_used_at TEXT;
		ALTER TABLE saved_searches ADD COLUMN user_id TEXT;

		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			display_name TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE user_password_credentials (
			user_id TEXT PRIMARY KEY REFERENCES users(id),
			password_hash TEXT NOT NULL,
			password_algo TEXT NOT NULL DEFAULT 'bcrypt',
			password_updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE organization_members (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			user_id TEXT NOT NULL REFERENCES users(id),
			role TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(organization_id, user_id)
		);
		CREATE INDEX idx_org_members_user ON organization_members(user_id);
		CREATE TABLE user_sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			session_token_hash TEXT UNIQUE NOT NULL,
			csrf_secret TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			last_seen_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			revoked_at TEXT
		);
		CREATE INDEX idx_user_sessions_user ON user_sessions(user_id);
		CREATE TABLE personal_access_tokens (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			label TEXT NOT NULL,
			token_prefix TEXT UNIQUE NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			scopes_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT DEFAULT (datetime('now')),
			last_used_at TEXT,
			expires_at TEXT,
			revoked_at TEXT
		);
		CREATE INDEX idx_pats_user ON personal_access_tokens(user_id);
		CREATE TABLE project_automation_tokens (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			label TEXT NOT NULL,
			token_prefix TEXT UNIQUE NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			scopes_json TEXT NOT NULL DEFAULT '[]',
			created_by_user_id TEXT REFERENCES users(id),
			created_at TEXT DEFAULT (datetime('now')),
			last_used_at TEXT,
			expires_at TEXT,
			revoked_at TEXT
		);
		CREATE INDEX idx_project_automation_tokens_project ON project_automation_tokens(project_id);
		CREATE TABLE auth_audit_logs (
			id TEXT PRIMARY KEY,
			credential_type TEXT NOT NULL,
			credential_id TEXT,
			user_id TEXT,
			project_id TEXT,
			organization_id TEXT,
			action TEXT NOT NULL,
			request_path TEXT,
			request_method TEXT,
			ip_address TEXT,
			user_agent TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_auth_audit_logs_created ON auth_audit_logs(created_at DESC);
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			project_id TEXT NOT NULL REFERENCES projects(id),
			payload BLOB NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			available_at TEXT NOT NULL,
			lease_until TEXT,
			worker_id TEXT,
			last_error TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_jobs_ready ON jobs(status, available_at, created_at);
		CREATE INDEX idx_jobs_lease ON jobs(status, lease_until);
		CREATE TABLE runtime_leases (
			name TEXT PRIMARY KEY,
			holder_id TEXT NOT NULL,
			lease_until TEXT NOT NULL,
			updated_at TEXT DEFAULT (datetime('now'))
		);
	`},
	{13, `
		CREATE TABLE IF NOT EXISTS notification_outbox (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			group_id TEXT,
			event_id TEXT,
			recipient TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			transport TEXT NOT NULL DEFAULT 'tiny-outbox',
			status TEXT NOT NULL DEFAULT 'queued',
			error TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			sent_at TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_notification_outbox_created ON notification_outbox(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_notification_outbox_project ON notification_outbox(project_id, created_at DESC);
	`},
	{14, `
		CREATE TABLE IF NOT EXISTS team_members (
			id TEXT PRIMARY KEY,
			team_id TEXT NOT NULL REFERENCES teams(id),
			user_id TEXT NOT NULL REFERENCES users(id),
			role TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(team_id, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_team_members_team ON team_members(team_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_team_members_user ON team_members(user_id);
		CREATE TABLE IF NOT EXISTS member_invites (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			team_id TEXT REFERENCES teams(id),
			email TEXT NOT NULL,
			role TEXT NOT NULL,
			token_prefix TEXT UNIQUE NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			invited_by_user_id TEXT REFERENCES users(id),
			created_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT,
			accepted_at TEXT,
			accepted_by_user_id TEXT REFERENCES users(id)
		);
		CREATE INDEX IF NOT EXISTS idx_member_invites_org ON member_invites(organization_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_member_invites_email ON member_invites(email, status);
	`},
	{16, `
		CREATE TABLE IF NOT EXISTS notification_deliveries (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			rule_id TEXT,
			group_id TEXT,
			event_id TEXT,
			kind TEXT NOT NULL,
			target TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			attempts INTEGER NOT NULL DEFAULT 0,
			response_status INTEGER,
			error TEXT,
			payload_json TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			last_attempt_at TEXT,
			delivered_at TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_notification_deliveries_project_created ON notification_deliveries(project_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_notification_deliveries_status_created ON notification_deliveries(status, created_at DESC);
	`},
	{57, `
		CREATE TABLE IF NOT EXISTS project_memberships (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			user_id TEXT NOT NULL REFERENCES users(id),
			role TEXT NOT NULL DEFAULT 'member',
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_project_memberships_project ON project_memberships(project_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_project_memberships_user ON project_memberships(user_id);

		-- Default existing org members into all org projects as "member".
		INSERT OR IGNORE INTO project_memberships (id, project_id, user_id, role, created_at)
		SELECT
			lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))),
			p.id,
			om.user_id,
			'member',
			datetime('now')
		FROM organization_members om
		JOIN projects p ON p.organization_id = om.organization_id;
	`},
	{58, `
		ALTER TABLE users ADD COLUMN totp_secret TEXT DEFAULT '';
		ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;

		CREATE TABLE IF NOT EXISTS totp_recovery_codes (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			code_hash TEXT NOT NULL,
			used_at TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_totp_recovery_codes_user ON totp_recovery_codes(user_id);
	`},
	{59, `
		CREATE TABLE IF NOT EXISTS anomaly_events (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			metric TEXT NOT NULL,
			current_value REAL NOT NULL,
			mean_value REAL NOT NULL,
			stddev_value REAL NOT NULL,
			threshold_value REAL NOT NULL,
			detected_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_anomaly_events_project ON anomaly_events(project_id, detected_at DESC);
	`},
	{61, `
		ALTER TABLE releases ADD COLUMN ref TEXT DEFAULT '';
		ALTER TABLE releases ADD COLUMN url TEXT DEFAULT '';
	`},
	{62, `
		CREATE TABLE IF NOT EXISTS project_environments (
			project_id TEXT NOT NULL REFERENCES projects(id),
			name TEXT NOT NULL,
			is_hidden INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (project_id, name)
		);

		CREATE TABLE IF NOT EXISTS project_teams (
			project_id TEXT NOT NULL REFERENCES projects(id),
			team_id TEXT NOT NULL REFERENCES teams(id),
			created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (project_id, team_id)
		);
		CREATE INDEX IF NOT EXISTS idx_project_teams_team ON project_teams(team_id);
	`},
	{73, `
		CREATE TABLE IF NOT EXISTS repositories (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			name TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT 'manual',
			url TEXT DEFAULT '',
			external_slug TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_repositories_org ON repositories(organization_id);
	`},
	{74, `
		ALTER TABLE repositories ADD COLUMN owner_slug TEXT DEFAULT '';
		ALTER TABLE repositories ADD COLUMN default_branch TEXT DEFAULT '';
		ALTER TABLE repositories ADD COLUMN test_analytics_enabled INTEGER NOT NULL DEFAULT 1;
		ALTER TABLE repositories ADD COLUMN sync_status TEXT NOT NULL DEFAULT 'idle';
		ALTER TABLE repositories ADD COLUMN last_synced_at TEXT;
		ALTER TABLE repositories ADD COLUMN last_sync_started_at TEXT;
		ALTER TABLE repositories ADD COLUMN last_sync_error TEXT DEFAULT '';
		CREATE UNIQUE INDEX IF NOT EXISTS idx_repositories_org_owner_name ON repositories(organization_id, owner_slug, name);
		CREATE INDEX IF NOT EXISTS idx_repositories_owner ON repositories(owner_slug);

		CREATE TABLE IF NOT EXISTS prevent_repository_branches (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL REFERENCES repositories(id),
			name TEXT NOT NULL,
			is_default INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			last_synced_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(repository_id, name)
		);
		CREATE INDEX IF NOT EXISTS idx_prevent_repository_branches_repo ON prevent_repository_branches(repository_id, created_at ASC);

		CREATE TABLE IF NOT EXISTS prevent_repository_tokens (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL REFERENCES repositories(id),
			label TEXT NOT NULL,
			token_value TEXT NOT NULL DEFAULT '',
			token_prefix TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			rotated_at TEXT,
			last_used_at TEXT,
			revoked_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(repository_id, token_prefix)
		);
		CREATE INDEX IF NOT EXISTS idx_prevent_repository_tokens_repo ON prevent_repository_tokens(repository_id, created_at ASC);

		CREATE TABLE IF NOT EXISTS prevent_repository_test_suites (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL REFERENCES repositories(id),
			external_suite_id TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			last_run_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(repository_id, external_suite_id)
		);
		CREATE INDEX IF NOT EXISTS idx_prevent_repository_test_suites_repo ON prevent_repository_test_suites(repository_id, created_at ASC);

		CREATE TABLE IF NOT EXISTS prevent_repository_test_results (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL REFERENCES repositories(id),
			suite_id TEXT NOT NULL REFERENCES prevent_repository_test_suites(id),
			suite_name TEXT DEFAULT '',
			branch_name TEXT NOT NULL,
			commit_sha TEXT DEFAULT '',
			status TEXT NOT NULL,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			test_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			skipped_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_prevent_repository_test_results_repo ON prevent_repository_test_results(repository_id, created_at ASC);

		CREATE TABLE IF NOT EXISTS prevent_repository_test_result_aggregates (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL REFERENCES repositories(id),
			branch_name TEXT NOT NULL,
			total_runs INTEGER NOT NULL DEFAULT 0,
			passing_runs INTEGER NOT NULL DEFAULT 0,
			failing_runs INTEGER NOT NULL DEFAULT 0,
			skipped_runs INTEGER NOT NULL DEFAULT 0,
			avg_duration_ms INTEGER NOT NULL DEFAULT 0,
			last_run_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(repository_id, branch_name)
		);
		CREATE INDEX IF NOT EXISTS idx_prevent_repository_test_aggregates_repo ON prevent_repository_test_result_aggregates(repository_id, created_at ASC);
	`},
}
