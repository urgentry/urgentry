package postgrescontrol

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"urgentry/internal/testpostgres"
)

var postgresControlPostgres = testpostgres.NewProvider("urgentry-postgrescontrol")

func TestMigrateBootstrapsEmptyDatabase(t *testing.T) {
	t.Parallel()

	db := openTestDatabase(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _control_migrations`).Scan(&count); err != nil {
		t.Fatalf("count control migrations: %v", err)
	}
	if count != len(AllMigrations()) {
		t.Fatalf("expected %d control migrations, got %d", len(AllMigrations()), count)
	}

	if _, err := db.Exec(`
INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme');
INSERT INTO teams (id, organization_id, slug, name) VALUES ('team-1', 'org-1', 'backend', 'Backend');
INSERT INTO users (id, email, display_name) VALUES ('user-1', 'owner@example.com', 'Owner');
INSERT INTO organization_members (id, organization_id, user_id, role) VALUES ('m-1', 'org-1', 'user-1', 'owner');
INSERT INTO projects (id, organization_id, team_id, slug, name, platform) VALUES ('proj-1', 'org-1', 'team-1', 'default', 'Default', 'go');
INSERT INTO personal_access_tokens (id, user_id, label, token_prefix, token_hash) VALUES ('pat-1', 'user-1', 'bootstrap', 'gpat', 'hash');
INSERT INTO project_replay_configs (project_id) VALUES ('proj-1');
`); err != nil {
		t.Fatalf("bootstrap inserts failed: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	db := openTestDatabase(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
}

func TestMigrateAppliesMissingTailOnly(t *testing.T) {
	t.Parallel()

	db := openTestDatabase(t)
	ctx := context.Background()
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS _control_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		t.Fatalf("create migration table: %v", err)
	}

	first := AllMigrations()[0]
	if _, err := db.Exec(first.SQL); err != nil {
		t.Fatalf("seed first migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO _control_migrations (version, name) VALUES ($1, $2)`, first.Version, first.Name); err != nil {
		t.Fatalf("record first migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed org row: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM organizations WHERE id = 'org-1'`).Scan(&name); err != nil {
		t.Fatalf("load seeded org row: %v", err)
	}
	if name != "Acme" {
		t.Fatalf("organization name = %q, want Acme", name)
	}
}

func TestMigrationsAreAdditive(t *testing.T) {
	t.Parallel()

	for _, migration := range AllMigrations() {
		sql := strings.ToUpper(migration.SQL)
		if strings.Contains(sql, "DROP TABLE") || strings.Contains(sql, "DROP COLUMN") || strings.Contains(sql, "TRUNCATE ") {
			t.Fatalf("migration %d contains destructive DDL", migration.Version)
		}
	}
}

func TestMigrationVersionsAreSequential(t *testing.T) {
	t.Parallel()

	for i, migration := range AllMigrations() {
		want := i + 1
		if migration.Version != want {
			t.Fatalf("migration version = %d, want %d", migration.Version, want)
		}
	}
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	return postgresControlPostgres.OpenDatabase(t, "urgentry_test")
}

func TestMain(m *testing.M) {
	code := m.Run()
	postgresControlPostgres.Close()
	os.Exit(code)
}
