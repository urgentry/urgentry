// Package sqlite provides a SQLite-backed persistence layer using modernc.org/sqlite
// (pure Go, no CGO). The database is stored at ~/.urgentry/urgentry.db by default,
// overridable via URGENTRY_DATA_DIR.
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens or creates a SQLite database in dataDir.
// If dataDir is empty, defaults to ~/.urgentry/.
// Creates the directory if needed and runs pending migrations.
func Open(dataDir string) (*sql.DB, error) {
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("user home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".urgentry")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "urgentry.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=30000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite doesn't support concurrent writes; one conn avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	// Enable WAL mode explicitly (the pragma in the DSN may not stick).
	if err := withBusyRetry(30*time.Second, func() error {
		_, err := db.Exec("PRAGMA journal_mode=WAL")
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if err := withBusyRetry(30*time.Second, func() error {
		_, err := db.Exec("PRAGMA foreign_keys=ON")
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := withBusyRetry(30*time.Second, func() error {
		return migrate(db)
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func withBusyRetry(timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if !isBusyError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}
