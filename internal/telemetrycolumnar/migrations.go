package telemetrycolumnar

type Backend string

const BackendClickHouse Backend = "clickhouse"

type Migration struct {
	Version    int
	Name       string
	Statements []string
}

func Migrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "create-log-facts",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS _columnar_migrations (
					version UInt32,
					name String,
					applied_at DateTime DEFAULT now()
				) ENGINE = ReplacingMergeTree(applied_at)
				ORDER BY version`,
				`CREATE TABLE IF NOT EXISTS telemetry_log_facts (
					id String,
					organization_id String,
					project_id String,
					event_id String,
					trace_id String,
					span_id String,
					release String,
					environment String,
					platform String,
					level String,
					logger String,
					message String,
					search_text String,
					timestamp DateTime64(3, 'UTC'),
					attributes_json String,
					version UInt64
				) ENGINE = ReplacingMergeTree(version)
				ORDER BY (organization_id, project_id, timestamp, id)`,
			},
		},
	}
}
