package sqlite

// migrationsIntegration contains schema for the integration plugin framework.
var migrationsIntegration = []schemaMigration{
	{45, `
		CREATE TABLE IF NOT EXISTS integration_configs (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			integration_id TEXT NOT NULL,
			project_id TEXT NOT NULL DEFAULT '',
			config_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_integration_configs_org
			ON integration_configs(organization_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_integration_configs_org_integration
			ON integration_configs(organization_id, integration_id, created_at DESC);
	`},
	{55, `
		CREATE TABLE IF NOT EXISTS code_mappings (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			stack_root TEXT NOT NULL DEFAULT '',
			source_root TEXT NOT NULL DEFAULT '',
			default_branch TEXT NOT NULL DEFAULT 'main',
			repo_url TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_code_mappings_project
			ON code_mappings(project_id, created_at DESC);
	`},
	{56, `
		CREATE TABLE IF NOT EXISTS data_forwarding_configs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'webhook',
			url TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_data_forwarding_configs_project
			ON data_forwarding_configs(project_id, created_at DESC);
	`},
}
