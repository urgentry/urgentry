package sqlite

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/migration"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

func upsertArtifactImport(ctx context.Context, db execQuerier, blobs store.BlobStore, projectID string, a migration.ArtifactImport, blobKeys *[]string) error {
	body, err := decodeArtifactBody(a.BodyBase64)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(a.Kind)
	if kind == "" {
		return validationErrorf("artifact kind is required")
	}
	a, err = normalizeImportedArtifact(a, body)
	if err != nil {
		return err
	}
	switch kind {
	case "attachment":
		return importAttachmentArtifact(ctx, db, blobs, projectID, a, body, blobKeys)
	case "profile_raw":
		return importProfileRawArtifact(ctx, blobs, projectID, a, body, blobKeys)
	case "source_map":
		return importSourceMapArtifact(ctx, db, blobs, projectID, a, body, blobKeys)
	case "proguard":
		return importProGuardArtifact(ctx, db, blobs, projectID, a, body, blobKeys)
	default:
		return importDebugFileArtifact(ctx, db, blobs, projectID, a, body, blobKeys)
	}
}

func importAttachmentArtifact(ctx context.Context, db execQuerier, blobs store.BlobStore, projectID string, a migration.ArtifactImport, body []byte, blobKeys *[]string) error {
	if err := upsertAttachmentArtifact(ctx, db, projectID, a); err != nil {
		return err
	}
	if len(body) > 0 && blobs != nil {
		key := importedAttachmentObjectKey(projectID, a)
		if err := blobs.Put(ctx, key, body); err != nil {
			return err
		}
		*blobKeys = append(*blobKeys, key)
	}
	return nil
}

func importProfileRawArtifact(ctx context.Context, blobs store.BlobStore, projectID string, a migration.ArtifactImport, body []byte, blobKeys *[]string) error {
	if len(body) == 0 || blobs == nil {
		return nil
	}
	objectKey := strings.TrimSpace(a.ObjectKey)
	if objectKey == "" {
		objectKey = importedProfileRawObjectKey(projectID, a)
	}
	if err := blobs.Put(ctx, objectKey, body); err != nil {
		return err
	}
	*blobKeys = append(*blobKeys, objectKey)
	return nil
}

func importSourceMapArtifact(ctx context.Context, db execQuerier, blobs store.BlobStore, projectID string, a migration.ArtifactImport, body []byte, blobKeys *[]string) error {
	if err := upsertSourceMapArtifact(ctx, db, projectID, a); err != nil {
		return err
	}
	if len(body) > 0 && blobs != nil {
		key := importedSourceMapObjectKey(projectID, a)
		if err := blobs.Put(ctx, key, body); err != nil {
			return err
		}
		*blobKeys = append(*blobKeys, key)
	}
	return nil
}

func importProGuardArtifact(ctx context.Context, db execQuerier, blobs store.BlobStore, projectID string, a migration.ArtifactImport, body []byte, blobKeys *[]string) error {
	if err := upsertProGuardArtifact(ctx, db, projectID, a); err != nil {
		return err
	}
	if len(body) > 0 && blobs != nil {
		key := importedProGuardObjectKey(projectID, a)
		if err := blobs.Put(ctx, key, body); err != nil {
			return err
		}
		*blobKeys = append(*blobKeys, key)
	}
	return nil
}

func importDebugFileArtifact(ctx context.Context, db execQuerier, blobs store.BlobStore, projectID string, a migration.ArtifactImport, body []byte, blobKeys *[]string) error {
	if err := upsertDebugFileArtifact(ctx, db, projectID, a, body); err != nil {
		return err
	}
	if len(body) > 0 && blobs != nil {
		key := importedDebugFileObjectKey(projectID, a)
		if err := blobs.Put(ctx, key, body); err != nil {
			return err
		}
		*blobKeys = append(*blobKeys, key)
	}
	return nil
}

func upsertAttachmentArtifact(ctx context.Context, db execQuerier, projectID string, a migration.ArtifactImport) error {
	attachmentID := strings.TrimSpace(a.ID)
	if attachmentID == "" {
		attachmentID = id.New()
	}
	objectKey := importedAttachmentObjectKey(projectID, a)
	_, err := db.ExecContext(ctx,
		`INSERT INTO event_attachments
			(id, project_id, event_id, name, content_type, size_bytes, object_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
		 ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			event_id = excluded.event_id,
			name = excluded.name,
			content_type = excluded.content_type,
			size_bytes = excluded.size_bytes,
			object_key = excluded.object_key`,
		attachmentID, projectID, a.EventID, a.Name, a.ContentType, a.Size, objectKey, a.CreatedAt,
	)
	return err
}

func upsertSourceMapArtifact(ctx context.Context, db execQuerier, projectID string, a migration.ArtifactImport) error {
	artifactID := strings.TrimSpace(a.ID)
	if artifactID == "" {
		artifactID = id.New()
	}
	objectKey := importedSourceMapObjectKey(projectID, a)
	_, err := db.ExecContext(ctx,
		`INSERT INTO artifacts
			(id, project_id, release_version, name, object_key, size, checksum, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
		 ON CONFLICT(project_id, release_version, name) DO UPDATE SET
			object_key = excluded.object_key,
			size = excluded.size,
			checksum = excluded.checksum`,
		artifactID, projectID, a.ReleaseVersion, a.Name, objectKey, a.Size, a.Checksum, a.CreatedAt,
	)
	return err
}

func upsertProGuardArtifact(ctx context.Context, db execQuerier, projectID string, a migration.ArtifactImport) error {
	artifactID := strings.TrimSpace(a.ID)
	if artifactID == "" {
		artifactID = id.New()
	}
	objectKey := importedProGuardObjectKey(projectID, a)
	_, err := db.ExecContext(ctx,
		`INSERT INTO debug_files
			(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
		 ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			release_version = excluded.release_version,
			uuid = excluded.uuid,
			code_id = excluded.code_id,
			name = excluded.name,
			object_key = excluded.object_key,
			size_bytes = excluded.size_bytes,
			checksum = excluded.checksum`,
		artifactID, projectID, a.ReleaseVersion, a.UUID, a.CodeID, a.Name, objectKey, a.Size, a.Checksum, a.CreatedAt,
	)
	return err
}

func upsertDebugFileArtifact(ctx context.Context, db execQuerier, projectID string, a migration.ArtifactImport, body []byte) error {
	artifactID := strings.TrimSpace(a.ID)
	if artifactID == "" {
		artifactID = id.New()
	}
	objectKey := importedDebugFileObjectKey(projectID, a)
	_, err := db.ExecContext(ctx,
		`INSERT INTO debug_files
			(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at, kind, content_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''))
		 ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			release_version = excluded.release_version,
			uuid = excluded.uuid,
			code_id = excluded.code_id,
			name = excluded.name,
			object_key = excluded.object_key,
			size_bytes = excluded.size_bytes,
			checksum = excluded.checksum,
			kind = excluded.kind,
			content_type = excluded.content_type`,
		artifactID, projectID, a.ReleaseVersion, a.UUID, a.CodeID, a.Name, objectKey, a.Size, a.Checksum, a.CreatedAt, nullOrDefault(a.Kind, "native"), a.ContentType,
	)
	if err != nil {
		return err
	}
	item := &DebugFile{
		ID:           artifactID,
		ProjectID:    projectID,
		ReleaseID:    a.ReleaseVersion,
		Kind:         nullOrDefault(a.Kind, "native"),
		Name:         a.Name,
		UUID:         a.UUID,
		CodeID:       a.CodeID,
		BuildID:      a.BuildID,
		ModuleName:   a.ModuleName,
		Architecture: a.Architecture,
		Platform:     a.Platform,
		ContentType:  a.ContentType,
		ObjectKey:    objectKey,
		Size:         a.Size,
		Checksum:     a.Checksum,
		CreatedAt:    parseOptionalTimeString(a.CreatedAt),
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	return upsertNativeSymbolSources(ctx, db, item, body)
}

func exportBlob(ctx context.Context, blobs store.BlobStore, objectKey string) ([]byte, error) {
	if blobs == nil || strings.TrimSpace(objectKey) == "" {
		return nil, nil
	}
	body, err := blobs.Get(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	return body, nil
}

func decodeArtifactBody(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	body, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode artifact body: %w", err)
	}
	return body, nil
}

func normalizeImportedArtifact(a migration.ArtifactImport, body []byte) (migration.ArtifactImport, error) {
	if artifactRequiresBody(a.Kind) && len(body) == 0 {
		return a, validationErrorf("artifact %s is missing bodyBase64", a.Name)
	}
	if len(body) == 0 {
		return a, nil
	}
	if a.Size != 0 && a.Size != int64(len(body)) {
		return a, validationErrorf("artifact %s size mismatch", a.Name)
	}
	a.Size = int64(len(body))
	sum := checksumSHA1(body)
	if strings.TrimSpace(a.Checksum) != "" && !strings.EqualFold(strings.TrimSpace(a.Checksum), sum) {
		return a, validationErrorf("artifact %s checksum mismatch", a.Name)
	}
	a.Checksum = sum
	return a, nil
}

func artifactRequiresBody(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "attachment", "profile_raw", "source_map", "proguard":
		return true
	default:
		return strings.TrimSpace(kind) != ""
	}
}

type artifactExportPolicy struct {
	includeBody     bool
	populateSize    bool
	computeChecksum bool
}

var streamedArtifactExportPolicy = artifactExportPolicy{
	includeBody:     true,
	populateSize:    true,
	computeChecksum: true,
}

func materializeArtifactExport(ctx context.Context, blobs store.BlobStore, item migration.ArtifactImport, policy artifactExportPolicy) (migration.ArtifactImport, []byte, error) {
	if !policy.includeBody && !policy.populateSize && !policy.computeChecksum {
		return item, nil, nil
	}
	body, err := exportBlob(ctx, blobs, item.ObjectKey)
	if err != nil {
		return item, nil, fmt.Errorf("load artifact %q: %w", item.ObjectKey, err)
	}
	if len(body) == 0 {
		return item, nil, nil
	}
	if policy.populateSize && (item.Size == 0 || strings.TrimSpace(item.Kind) == "profile_raw") {
		item.Size = int64(len(body))
	}
	if policy.computeChecksum {
		item.Checksum = checksumSHA1(body)
	}
	return item, body, nil
}

func checksumSHA1(body []byte) string {
	sum := sha1.Sum(body)
	return hex.EncodeToString(sum[:])
}

func importedAttachmentObjectKey(projectID string, a migration.ArtifactImport) string {
	if strings.TrimSpace(a.ObjectKey) != "" {
		return strings.TrimSpace(a.ObjectKey)
	}
	return fmt.Sprintf("attachments/%s/%s/%s",
		sanitizeImportKeySegment(projectID),
		sanitizeImportKeySegment(a.EventID),
		sanitizeImportKeySegment(a.Name),
	)
}

func importedSourceMapObjectKey(projectID string, a migration.ArtifactImport) string {
	if strings.TrimSpace(a.ObjectKey) != "" {
		return strings.TrimSpace(a.ObjectKey)
	}
	return fmt.Sprintf("sourcemaps/%s/%s/%s",
		sanitizeImportKeySegment(projectID),
		sanitizeImportKeySegment(a.ReleaseVersion),
		sanitizeImportKeySegment(a.Name),
	)
}

func importedProGuardObjectKey(projectID string, a migration.ArtifactImport) string {
	if strings.TrimSpace(a.ObjectKey) != "" {
		return strings.TrimSpace(a.ObjectKey)
	}
	return fmt.Sprintf("proguard/%s/%s/%s/%s",
		sanitizeImportKeySegment(projectID),
		sanitizeImportKeySegment(a.ReleaseVersion),
		sanitizeImportKeySegment(a.UUID),
		sanitizeImportKeySegment(a.Name),
	)
}

func importedProfileRawObjectKey(projectID string, a migration.ArtifactImport) string {
	if strings.TrimSpace(a.ObjectKey) != "" {
		return strings.TrimSpace(a.ObjectKey)
	}
	profileID := strings.TrimSpace(a.EventID)
	if profileID == "" {
		profileID = a.Name
	}
	return fmt.Sprintf("profiles/%s/%s.json",
		sanitizeImportKeySegment(projectID),
		sanitizeImportKeySegment(profileID),
	)
}

func importedDebugFileObjectKey(projectID string, a migration.ArtifactImport) string {
	if strings.TrimSpace(a.ObjectKey) != "" {
		return strings.TrimSpace(a.ObjectKey)
	}
	fileID := strings.TrimSpace(a.ID)
	if fileID == "" {
		fileID = a.Name
	}
	return fmt.Sprintf("debug/%s/%s/%s/%s/%s",
		sanitizeImportKeySegment(nullOrDefault(a.Kind, "native")),
		sanitizeImportKeySegment(projectID),
		sanitizeImportKeySegment(a.ReleaseVersion),
		sanitizeImportKeySegment(fileID),
		sanitizeImportKeySegment(a.Name),
	)
}

func cleanupImportedBlobs(ctx context.Context, blobs store.BlobStore, keys []string) error {
	if blobs == nil {
		return nil
	}
	var cleanupErr error
	for i := len(keys) - 1; i >= 0; i-- {
		if err := blobs.Delete(ctx, keys[i]); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}
