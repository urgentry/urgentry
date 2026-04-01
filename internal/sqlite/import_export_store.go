package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"urgentry/internal/attachment"
	"urgentry/internal/migration"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/store"
)

/*
Import / export flow

	export request
	  -> prepare metadata snapshot
	  -> stream JSON payload
	  -> materialize artifact blobs only when body output is required

	import / validate request
	  -> begin transaction
	  -> validate + apply catalog, issues, artifacts
	  -> commit or rollback
	  -> clean up imported blobs on failure
*/

// ImportExportStore owns SQLite-backed org import/export flows.
type ImportExportStore struct {
	db          *sql.DB
	attachments attachment.Store
	pgStore     proguard.Store
	smStore     sourcemap.Store
	blobs       store.BlobStore
}

// NewImportExportStore creates a SQLite-backed import/export store.
func NewImportExportStore(db *sql.DB, attachments attachment.Store, pgStore proguard.Store, smStore sourcemap.Store, blobs store.BlobStore) *ImportExportStore {
	return &ImportExportStore{
		db:          db,
		attachments: attachments,
		pgStore:     pgStore,
		smStore:     smStore,
		blobs:       blobs,
	}
}

// ExportOrganizationPayload returns a full cutover payload for one organization.
func (s *ImportExportStore) ExportOrganizationPayload(ctx context.Context, orgSlug string) (*migration.ImportPayload, error) {
	return exportOrganizationPayload(ctx, s.db, orgSlug)
}

// WriteOrganizationPayloadJSON streams a full cutover payload for one organization.
func (s *ImportExportStore) WriteOrganizationPayloadJSON(ctx context.Context, orgSlug string, w io.Writer) error {
	export, err := prepareOrganizationPayloadExport(ctx, s.db, orgSlug)
	if err != nil {
		return err
	}
	return export.writeJSON(ctx, w, s.blobs, streamedArtifactExportPolicy)
}

// ImportOrganizationPayload applies a cutover payload to one organization.
func (s *ImportExportStore) ImportOrganizationPayload(ctx context.Context, orgID, orgSlug string, payload migration.ImportPayload) (*migration.ImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	blobKeys := make([]string, 0, len(payload.Artifacts))
	result, err := importOrganizationPayload(ctx, tx, s.blobs, orgID, orgSlug, payload, &blobKeys)
	if err != nil {
		rollbackErr := tx.Rollback()
		cleanupErr := cleanupImportedBlobs(ctx, s.blobs, blobKeys)
		if rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			err = errors.Join(err, rollbackErr)
		}
		return nil, errors.Join(err, cleanupErr)
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.Join(err, cleanupImportedBlobs(ctx, s.blobs, blobKeys))
	}
	return result, nil
}

// ValidateOrganizationPayload runs an import in a rollback-only transaction.
func (s *ImportExportStore) ValidateOrganizationPayload(ctx context.Context, orgID, orgSlug string, payload migration.ImportPayload) (*migration.ImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	blobKeys := make([]string, 0, len(payload.Artifacts))
	result, err := importOrganizationPayload(ctx, tx, s.blobs, orgID, orgSlug, payload, &blobKeys)
	rollbackErr := tx.Rollback()
	cleanupErr := cleanupImportedBlobs(ctx, s.blobs, blobKeys)
	if err != nil {
		return nil, errors.Join(err, cleanupErr)
	}
	if rollbackErr != nil && rollbackErr != sql.ErrTxDone {
		return nil, errors.Join(rollbackErr, cleanupErr)
	}
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	result.DryRun = true
	return result, nil
}
