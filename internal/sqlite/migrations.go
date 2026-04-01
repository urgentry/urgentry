package sqlite

import (
	"database/sql"
	"fmt"
	"sort"
)

type schemaMigration struct {
	version int
	sql     string
}

// migrations collects all domain-specific migration sets and sorts them by
// version so they execute in the original order regardless of which file
// defines them.
var migrations = func() []schemaMigration {
	all := make([]schemaMigration, 0,
		len(migrationsCore)+
			len(migrationsEvents)+
			len(migrationsAnalytics)+
			len(migrationsOperator)+
			len(migrationsIntegration)+
			len(migrationsFeatures),
	)
	all = append(all, migrationsCore...)
	all = append(all, migrationsEvents...)
	all = append(all, migrationsAnalytics...)
	all = append(all, migrationsOperator...)
	all = append(all, migrationsIntegration...)
	all = append(all, migrationsFeatures...)
	sort.Slice(all, func(i, j int) bool {
		return all[i].version < all[j].version
	})
	return all
}()

func migrate(db *sql.DB) error {
	// Create migration tracking table.
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range migrations {
		var exists int
		if err := db.QueryRow("SELECT COUNT(*) FROM _migrations WHERE version = ?", m.version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if exists > 0 {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec("INSERT INTO _migrations (version) VALUES (?)", m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d record: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d commit: %w", m.version, err)
		}
	}
	return nil
}
