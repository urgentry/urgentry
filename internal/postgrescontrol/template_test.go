package postgrescontrol

import (
	"context"
	"database/sql"
	"testing"
)

var migratedPostgresControl = postgresControlPostgres.NewTemplate("urgentry-postgrescontrol-migrated", func(db *sql.DB) error {
	return Migrate(context.Background(), db)
})

func openMigratedTestDatabase(t testing.TB) *sql.DB {
	t.Helper()
	return migratedPostgresControl.OpenDatabase(t, "urgentry_test")
}
