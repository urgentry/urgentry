package config

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createMetadataTable(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO schema_metadata (key, value) VALUES ('schema_version', ?)`,
		version)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckSchemaCompat_FreshDB(t *testing.T) {
	db := setupTestDB(t)
	result, err := CheckSchemaCompat(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compatible {
		t.Error("fresh DB should be compatible")
	}
	if result.DBSchemaVersion != 0 {
		t.Errorf("dbSchemaVersion=%d, want 0", result.DBSchemaVersion)
	}
}

func TestCheckSchemaCompat_CurrentVersion(t *testing.T) {
	db := setupTestDB(t)
	createMetadataTable(t, db, SchemaVersion)

	result, err := CheckSchemaCompat(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compatible {
		t.Error("current version should be compatible")
	}
	if result.Warning != "" {
		t.Errorf("unexpected warning: %s", result.Warning)
	}
}

func TestCheckSchemaCompat_IncompatibleDowngrade(t *testing.T) {
	db := setupTestDB(t)
	createMetadataTable(t, db, SchemaVersion+5)

	result, err := CheckSchemaCompat(context.Background(), db)
	if err == nil {
		t.Fatal("expected error for downgrade")
	}
	if result.Compatible {
		t.Error("downgrade should not be compatible")
	}
}

func TestCheckSchemaCompat_SkipVersionWarning(t *testing.T) {
	db := setupTestDB(t)
	createMetadataTable(t, db, SchemaVersion-15)

	result, err := CheckSchemaCompat(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compatible {
		t.Error("skip-version should still be compatible")
	}
	if result.Warning == "" {
		t.Error("expected warning for skip-version upgrade")
	}
}

func TestCheckSchemaCompat_SlightlyBehind(t *testing.T) {
	db := setupTestDB(t)
	createMetadataTable(t, db, SchemaVersion-3)

	result, err := CheckSchemaCompat(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compatible {
		t.Error("slightly behind should be compatible")
	}
	if result.Warning != "" {
		t.Errorf("no warning expected for small gap, got: %s", result.Warning)
	}
}

func TestParseSchemaVersion(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"51", 51, false},
		{" 42 ", 42, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseSchemaVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSchemaVersion(%q) error=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseSchemaVersion(%q)=%d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
