package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"urgentry/internal/auth"
)

// KeyStore implements auth.KeyStore backed by SQLite.
type KeyStore struct {
	db *sql.DB
}

// NewKeyStore creates a SQLite-backed key store.
func NewKeyStore(db *sql.DB) *KeyStore {
	return &KeyStore{db: db}
}

// LookupKey looks up a project key by public key.
func (s *KeyStore) LookupKey(ctx context.Context, publicKey string) (*auth.ProjectKey, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT public_key, project_id, status, COALESCE(rate_limit, 0) FROM project_keys WHERE public_key = ?`,
		publicKey)

	var key auth.ProjectKey
	var status sql.NullString
	err := row.Scan(&key.PublicKey, &key.ProjectID, &status, &key.RateLimit)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrKeyNotFound
		}
		return nil, fmt.Errorf("lookup key: %w", err)
	}
	key.Status = nullStr(status)
	if key.Status == "" {
		key.Status = "active"
	}
	return &key, nil
}

// EnsureDefaultKey creates a default project key if none exists.
// Returns the public key string for logging.
func EnsureDefaultKey(ctx context.Context, db *sql.DB) (string, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_keys").Scan(&count); err != nil {
		return "", err
	}
	if count > 0 {
		// Keys already exist
		var key string
		if err := db.QueryRowContext(ctx, "SELECT public_key FROM project_keys LIMIT 1").Scan(&key); err != nil {
			return "", err
		}
		return key, nil
	}

	// Ensure a default org and project exist
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('default-org', 'urgentry-org', 'Urgentry')`)
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status)
		 VALUES ('default-project', 'default-org', 'default', 'Default Project', 'go', 'active')`)
	if err != nil {
		return "", err
	}

	// Create a default key
	keyID := generateID()
	publicKey := generateID()
	_, err = db.ExecContext(ctx,
		`INSERT INTO project_keys (id, project_id, public_key, status, label)
		 VALUES (?, 'default-project', ?, 'active', 'Default Key')`,
		keyID, publicKey)
	if err != nil {
		return "", err
	}
	return publicKey, nil
}
