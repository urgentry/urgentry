package telemetrycolumnar

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func CurrentVersion(ctx context.Context, db *sql.DB) (int, error) {
	if err := ensureMigrationTable(ctx, db); err != nil {
		return 0, err
	}
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(version) FROM _columnar_migrations FINAL`).Scan(&version); err != nil {
		return 0, fmt.Errorf("query columnar migration version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationTable(ctx, db); err != nil {
		return err
	}
	current, err := CurrentVersion(ctx, db)
	if err != nil {
		return err
	}
	for _, migration := range Migrations() {
		if migration.Version <= current {
			continue
		}
		for _, statement := range migration.Statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply columnar migration %d (%s): %w", migration.Version, migration.Name, err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO _columnar_migrations (version, name) VALUES (?, ?)`, migration.Version, migration.Name); err != nil {
			return fmt.Errorf("record columnar migration %d: %w", migration.Version, err)
		}
		current = migration.Version
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, strings.TrimSpace(`
CREATE TABLE IF NOT EXISTS _columnar_migrations (
	version UInt32,
	name String,
	applied_at DateTime DEFAULT now()
) ENGINE = ReplacingMergeTree(applied_at)
ORDER BY version
`))
	if err != nil {
		return fmt.Errorf("create columnar migrations table: %w", err)
	}
	return nil
}
