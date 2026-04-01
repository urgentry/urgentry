package telemetrybridge

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"urgentry/internal/testpostgres"
)

var telemetryPostgres = testpostgres.NewProvider("urgentry-telemetrybridge")
var migratedTelemetryPostgres = telemetryPostgres.NewTemplate("urgentry-telemetrybridge-migrated", func(db *sql.DB) error {
	return Migrate(context.Background(), db, BackendPostgres)
})

func TestMigrateBootstrapsTelemetryBridgeDatabase(t *testing.T) {
	t.Parallel()

	db := openTelemetryTestDatabase(t)
	if err := Migrate(context.Background(), db, BackendPostgres); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	version, err := CurrentVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentVersion() error = %v", err)
	}
	if version != len(Migrations(BackendPostgres)) {
		t.Fatalf("CurrentVersion() = %d, want %d", version, len(Migrations(BackendPostgres)))
	}
}

func TestMigrateTelemetryBridgeIsIdempotent(t *testing.T) {
	t.Parallel()

	db := openTelemetryTestDatabase(t)
	ctx := context.Background()
	if err := Migrate(ctx, db, BackendPostgres); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(ctx, db, BackendPostgres); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
}

func TestCurrentVersionUsesRecordedMigrations(t *testing.T) {
	t.Parallel()

	db := openTelemetryTestDatabase(t)
	ctx := context.Background()
	if err := ensureMigrationTable(ctx, db); err != nil {
		t.Fatalf("ensureMigrationTable() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO _telemetry_migrations (version, name) VALUES (1, 'create-telemetry-schema')`); err != nil {
		t.Fatalf("seed telemetry migration: %v", err)
	}
	version, err := CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("CurrentVersion() error = %v", err)
	}
	if version != 1 {
		t.Fatalf("CurrentVersion() = %d, want 1", version)
	}
}

func openTelemetryTestDatabase(tb testing.TB) *sql.DB {
	tb.Helper()
	return telemetryPostgres.OpenDatabase(tb, "urgentry_telemetry")
}

func openMigratedTelemetryTestDatabase(tb testing.TB) *sql.DB {
	tb.Helper()
	return migratedTelemetryPostgres.OpenDatabase(tb, "urgentry_telemetry")
}

func TestMain(m *testing.M) {
	code := m.Run()
	telemetryPostgres.Close()
	os.Exit(code)
}
