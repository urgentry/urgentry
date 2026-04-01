package sqlite

import (
	"database/sql"
	"errors"
	"time"

	"urgentry/internal/store"
)

var _ store.ProfileIngestStore = (*ProfileStore)(nil)
var _ store.ProfileReadStore = (*ProfileStore)(nil)

// ProfileStore owns canonical profile manifests, normalized graph rows, and
// raw profile payload retention.
type ProfileStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

var errProfilePayloadConflict = errors.New("profile payload conflict")

type profileReceiptHint struct {
	ProfileID   string
	EventID     string
	Transaction string
	TraceID     string
	Release     string
	Environment string
	Platform    string
	OccurredAt  time.Time
}

// NewProfileStore creates a SQLite-backed profile store.
func NewProfileStore(db *sql.DB, blobs store.BlobStore) *ProfileStore {
	return &ProfileStore{db: db, blobs: blobs}
}
