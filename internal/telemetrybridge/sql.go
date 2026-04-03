package telemetrybridge

import (
	"context"
	"database/sql"
	"fmt"

	"urgentry/internal/sqlutil"
)

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sqlutil.OpenPostgres(dsn)
	if err != nil {
		return nil, fmt.Errorf("open telemetry bridge: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping telemetry bridge: %w", err)
	}
	return db, nil
}

func CurrentVersion(ctx context.Context, db *sql.DB) (int, error) {
	if err := ensureMigrationTable(ctx, db); err != nil {
		return 0, err
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM _telemetry_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("query telemetry migration version: %w", err)
	}
	return version, nil
}

func Migrate(ctx context.Context, db *sql.DB, backend Backend) error {
	if err := ensureMigrationTable(ctx, db); err != nil {
		return err
	}
	for _, migration := range Migrations(backend) {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM _telemetry_migrations WHERE version = $1`, migration.Version).Scan(&exists); err != nil {
			return fmt.Errorf("check telemetry migration %d: %w", migration.Version, err)
		}
		if exists > 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin telemetry migration %d: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply telemetry migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO _telemetry_migrations (version, name) VALUES ($1, $2)`, migration.Version, migration.Name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record telemetry migration %d: %w", migration.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit telemetry migration %d: %w", migration.Version, err)
		}
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS _telemetry_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("create telemetry migrations table: %w", err)
	}
	return nil
}
