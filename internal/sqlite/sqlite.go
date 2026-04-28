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

const queueDBFileName = "urgentry-queue.db"

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
	dbPath := filepath.Join(dataDir, "urgentry.db")
	return openSQLiteFile(dataDir, dbPath, migrate)
}

// OpenQueue opens or creates a queue-only SQLite database with just the
// durable job and runtime-lease schema needed for Tiny-mode async processing.
func OpenQueue(dataDir string) (*sql.DB, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("queue data dir is required")
	}
	dbPath := filepath.Join(dataDir, queueDBFileName)
	return openSQLiteFile(dataDir, dbPath, migrateQueueOnly)
}

func openSQLiteFile(dataDir, dbPath string, migrateFn func(*sql.DB) error) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	_ = os.Chmod(dataDir, 0o700)

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
	secureSQLiteFiles(dbPath)

	if err := withBusyRetry(30*time.Second, func() error {
		return migrateFn(db)
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	secureSQLiteFiles(dbPath)

	return db, nil
}

func secureSQLiteFiles(dbPath string) {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Chmod(path, 0o600)
	}
}

func migrateQueueOnly(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			project_id TEXT NOT NULL,
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
		CREATE INDEX IF NOT EXISTS idx_jobs_ready ON jobs(status, available_at, created_at);
		CREATE INDEX IF NOT EXISTS idx_jobs_lease ON jobs(status, lease_until);
		CREATE TABLE IF NOT EXISTS runtime_leases (
			name TEXT PRIMARY KEY,
			holder_id TEXT NOT NULL,
			lease_until TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS ingest_log (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_ingest_log_project_seq ON ingest_log(project_id, seq);
		CREATE TABLE IF NOT EXISTS ingest_consumers (
			consumer TEXT PRIMARY KEY,
			last_seq INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS ingest_retries (
			seq INTEGER PRIMARY KEY,
			attempts INTEGER NOT NULL DEFAULT 0,
			available_at TEXT NOT NULL,
			last_error TEXT,
			updated_at TEXT NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate queue db: %w", err)
	}
	return nil
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
