package config

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

// SchemaVersion is the latest schema version this binary expects.
// Must match the highest migration version.
const SchemaVersion = 54

// MinCompatibleSchema is the oldest schema this binary can operate against
// without data corruption risk.
const MinCompatibleSchema = 1

// CompatResult describes the outcome of a version compatibility check.
type CompatResult struct {
	BinaryVersion   string `json:"binaryVersion"`
	SchemaVersion   int    `json:"schemaVersion"`
	DBSchemaVersion int    `json:"dbSchemaVersion"`
	Compatible      bool   `json:"compatible"`
	Warning         string `json:"warning,omitempty"`
}

// CheckSchemaCompat reads the schema version from the DB metadata table and
// compares it against the binary's expected version. It warns on skip-version
// upgrades and blocks on incompatible downgrades.
func CheckSchemaCompat(ctx context.Context, db *sql.DB) (*CompatResult, error) {
	result := &CompatResult{
		BinaryVersion: Version,
		SchemaVersion: SchemaVersion,
		Compatible:    true,
	}

	// Read the DB schema version from the metadata table.
	dbVersion, err := readDBSchemaVersion(ctx, db)
	if err != nil {
		// Table might not exist yet (fresh DB). That's fine.
		log.Debug().Err(err).Msg("schema_metadata not found, assuming fresh database")
		result.DBSchemaVersion = 0
		return result, nil
	}
	result.DBSchemaVersion = dbVersion

	// Case 1: DB is ahead of binary (downgrade).
	if dbVersion > SchemaVersion {
		result.Compatible = false
		result.Warning = fmt.Sprintf(
			"database schema version %d is newer than binary schema version %d; "+
				"this binary cannot safely run against a newer schema — upgrade the binary",
			dbVersion, SchemaVersion,
		)
		return result, fmt.Errorf("incompatible downgrade: %s", result.Warning)
	}

	// Case 2: DB is more than 10 versions behind (skip-version upgrade).
	gap := SchemaVersion - dbVersion
	if gap > 10 {
		result.Warning = fmt.Sprintf(
			"database schema version %d is %d versions behind binary (%d); "+
				"consider running intermediate releases to reduce migration risk",
			dbVersion, gap, SchemaVersion,
		)
		log.Warn().
			Int("db_version", dbVersion).
			Int("binary_version", SchemaVersion).
			Int("gap", gap).
			Msg("skip-version upgrade detected")
	}

	// Case 3: DB is below minimum compatible version.
	if dbVersion > 0 && dbVersion < MinCompatibleSchema {
		result.Compatible = false
		result.Warning = fmt.Sprintf(
			"database schema version %d is below minimum compatible version %d",
			dbVersion, MinCompatibleSchema,
		)
		return result, fmt.Errorf("schema too old: %s", result.Warning)
	}

	return result, nil
}

func readDBSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var value string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM schema_metadata WHERE key = 'schema_version'`,
	).Scan(&value)
	if err != nil {
		return 0, err
	}
	return parseSchemaVersion(value)
}

func parseSchemaVersion(s string) (int, error) {
	s = strings.TrimSpace(s)
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid schema version %q: %w", s, err)
	}
	return v, nil
}
