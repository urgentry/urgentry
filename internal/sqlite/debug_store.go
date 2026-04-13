package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/nativesym"
	"urgentry/internal/normalize"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

// DebugFile represents a generic native/mobile debug artifact.
type DebugFile struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	ReleaseID    string    `json:"releaseId"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	UUID         string    `json:"debugId,omitempty"`
	CodeID       string    `json:"codeId,omitempty"`
	BuildID      string    `json:"buildId,omitempty"`
	ModuleName   string    `json:"moduleName,omitempty"`
	Architecture string    `json:"architecture,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	ContentType  string    `json:"contentType,omitempty"`
	ObjectKey    string    `json:"-"`
	Size         int64     `json:"size"`
	Checksum     string    `json:"sha1,omitempty"`
	CreatedAt    time.Time `json:"dateCreated"`
}

// DebugFileStore persists generic debug files in SQLite and blob storage.
type DebugFileStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

// NewDebugFileStore creates a generic debug-file store.
func NewDebugFileStore(db *sql.DB, blobs store.BlobStore) *DebugFileStore {
	return &DebugFileStore{db: db, blobs: blobs}
}

// Save stores a debug file and its content.
func (s *DebugFileStore) Save(ctx context.Context, file *DebugFile, data []byte) error {
	if file == nil {
		return errors.New("debug file is nil")
	}
	if s.blobs == nil {
		return errors.New("debug blob store is not configured")
	}
	if file.ProjectID == "" || file.ReleaseID == "" {
		return errors.New("debug file project_id and release_id are required")
	}
	if file.ID == "" {
		file.ID = id.New()
	}
	file.Kind = strings.ToLower(strings.TrimSpace(file.Kind))
	if file.Kind == "" {
		file.Kind = "native"
	}
	if file.Name == "" {
		file.Name = "symbols.bin"
	}
	if file.CreatedAt.IsZero() {
		file.CreatedAt = time.Now().UTC()
	}
	if err := ensureReleaseForOwner(ctx, s.db, file.ProjectID, file.ReleaseID); err != nil {
		return err
	}
	file.Size = int64(len(data))
	file.ObjectKey = debugObjectKey(file.Kind, file.ProjectID, file.ReleaseID, file.ID, file.Name)

	if err := s.blobs.Put(ctx, file.ObjectKey, data); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO debug_files
			(id, project_id, release_version, uuid, code_id, name, object_key, size_bytes, checksum, created_at, kind, content_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		file.ID, file.ProjectID, file.ReleaseID, file.UUID, nullIfEmpty(file.CodeID), file.Name, file.ObjectKey,
		file.Size, nullIfEmpty(file.Checksum), file.CreatedAt.UTC().Format(time.RFC3339), file.Kind, nullIfEmpty(file.ContentType),
	)
	if err != nil {
		_ = s.blobs.Delete(ctx, file.ObjectKey)
		return err
	}
	if err := upsertNativeSymbolSources(ctx, s.db, file, data); err != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM debug_files WHERE id = ?`, file.ID)
		_ = s.blobs.Delete(ctx, file.ObjectKey)
		return err
	}
	return nil
}

// Get retrieves debug metadata and bytes by ID.
func (s *DebugFileStore) Get(ctx context.Context, id string) (*DebugFile, []byte, error) {
	var file DebugFile
	var uuid, codeID, contentType, checksum, createdAt sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, release_version, kind, uuid, code_id, name, content_type, object_key, size_bytes, checksum, created_at
		 FROM debug_files
		 WHERE id = ?`,
		id,
	).Scan(&file.ID, &file.ProjectID, &file.ReleaseID, &file.Kind, &uuid, &codeID, &file.Name, &contentType, &file.ObjectKey, &file.Size, &checksum, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load debug file: %w", err)
	}
	file.UUID = nullStr(uuid)
	file.CodeID = nullStr(codeID)
	file.ContentType = nullStr(contentType)
	file.Checksum = nullStr(checksum)
	file.CreatedAt = parseTime(nullStr(createdAt))
	body, err := s.blobs.Get(ctx, file.ObjectKey)
	if err != nil {
		if restoreErr := restoreArchivedBlob(ctx, s.db, s.blobs, file.ProjectID, "debug_file", file.ID, file.ObjectKey); restoreErr != nil {
			return &file, nil, restoreErr
		}
		body, err = s.blobs.Get(ctx, file.ObjectKey)
		if err != nil {
			return &file, nil, fmt.Errorf("load debug file blob: %w", err)
		}
	}
	return &file, body, nil
}

// ListByRelease lists debug files for a release, optionally filtered by kind.
func (s *DebugFileStore) ListByRelease(ctx context.Context, projectID, releaseVersion, kind string) ([]*DebugFile, error) {
	query := `SELECT id, project_id, release_version, kind, uuid, code_id, name, content_type, object_key, size_bytes, checksum, created_at
		FROM debug_files
		WHERE project_id = ? AND release_version = ? AND kind != 'proguard'`
	args := []any{projectID, releaseVersion}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list debug files: %w", err)
	}
	defer rows.Close()

	var files []*DebugFile
	for rows.Next() {
		var file DebugFile
		var uuid, codeID, contentType, checksum, createdAt sql.NullString
		if err := rows.Scan(&file.ID, &file.ProjectID, &file.ReleaseID, &file.Kind, &uuid, &codeID, &file.Name, &contentType, &file.ObjectKey, &file.Size, &checksum, &createdAt); err != nil {
			return nil, fmt.Errorf("scan debug file: %w", err)
		}
		file.UUID = nullStr(uuid)
		file.CodeID = nullStr(codeID)
		file.ContentType = nullStr(contentType)
		file.Checksum = nullStr(checksum)
		file.CreatedAt = parseTime(nullStr(createdAt))
		files = append(files, &file)
	}
	return files, rows.Err()
}

// LookupByDebugID finds the newest debug file for a release and debug ID.
func (s *DebugFileStore) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*DebugFile, []byte, error) {
	_, file, body, err := s.lookupNativeSymbolSource(ctx, projectID, releaseVersion, kind, NativeLookupInput{DebugID: debugID})
	return file, body, err
}

// LookupByCodeID finds the newest debug file for a release and code ID.
func (s *DebugFileStore) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*DebugFile, []byte, error) {
	_, file, body, err := s.lookupNativeSymbolSource(ctx, projectID, releaseVersion, kind, NativeLookupInput{CodeID: codeID, BuildID: codeID})
	return file, body, err
}

// SymbolicationStatus classifies whether a stored debug file is ready for symbolication.
func (s *DebugFileStore) SymbolicationStatus(ctx context.Context, file *DebugFile) (string, error) {
	if file == nil {
		return "", nil
	}
	if file.Kind == "proguard" {
		return "", nil
	}
	stored, body, err := s.Get(ctx, file.ID)
	if err != nil {
		return "", err
	}
	if stored == nil {
		return "uploaded", nil
	}
	status := nativesym.NewResolver(nil).SymbolicationStatus(&nativesym.File{
		ID:     stored.ID,
		CodeID: stored.CodeID,
		Kind:   stored.Kind,
	}, body)
	switch status {
	case nativesym.LookupStatusResolved:
		return "ready", nil
	case nativesym.LookupStatusMalformed:
		return "malformed", nil
	case nativesym.LookupStatusUnsupported:
		return "unsupported", nil
	default:
		return "uploaded", nil
	}
}

// ReprocessNativeEvents re-symbolicates native events in one release using the
// currently uploaded debug files for that release.
func (s *DebugFileStore) ReprocessNativeEvents(ctx context.Context, projectID, releaseVersion string) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("debug file store is not configured")
	}

	resolver := nativesym.NewResolver(nativeDebugFileLookup{store: s})
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(payload_json, '')
		   FROM events
		  WHERE project_id = ? AND release = ?
		    AND COALESCE(event_type, 'error') = 'error'
		    AND (
		          instr(payload_json, '"debug_id"') > 0
		       OR instr(payload_json, '"code_id"') > 0
		       OR instr(payload_json, '"instruction_addr"') > 0
		    )
		  ORDER BY ingested_at DESC`,
		projectID, releaseVersion,
	)
	if err != nil {
		return 0, fmt.Errorf("query native events: %w", err)
	}
	type candidate struct {
		rowID   string
		payload string
	}
	candidates := make([]candidate, 0, 16)
	for rows.Next() {
		var rowID, payload string
		if err := rows.Scan(&rowID, &payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan native event: %w", err)
		}
		candidates = append(candidates, candidate{rowID: rowID, payload: payload})
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close native event rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate native events: %w", err)
	}

	updated := 0
	for _, row := range candidates {
		if strings.TrimSpace(row.payload) == "" {
			continue
		}
		var evt normalize.Event
		if err := json.Unmarshal([]byte(row.payload), &evt); err != nil {
			continue
		}
		if !issue.ApplyEventResolvers(ctx, projectID, &evt, nil, nil, resolver) {
			continue
		}

		normalized, err := json.Marshal(&evt)
		if err != nil {
			return updated, fmt.Errorf("marshal reprocessed native event: %w", err)
		}
		tagsJSON, _ := json.Marshal(evt.Tags)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE events
			    SET title = ?, culprit = ?, message = ?, level = ?, platform = ?, release = ?, environment = ?,
			        tags_json = ?, payload_json = ?
			  WHERE id = ?`,
			evt.Title(), evt.Culprit(), evt.Message, evt.Level, evt.Platform, evt.Release, evt.Environment,
			string(tagsJSON), string(normalized), row.rowID,
		); err != nil {
			return updated, fmt.Errorf("update native event: %w", err)
		}
		updated++
	}
	return updated, nil
}

func debugObjectKey(kind, projectID, releaseVersion, fileID, name string) string {
	return fmt.Sprintf("debug/%s/%s/%s/%s/%s",
		sanitizeKeySegment(kind),
		sanitizeKeySegment(projectID),
		sanitizeKeySegment(releaseVersion),
		sanitizeKeySegment(fileID),
		sanitizeKeySegment(name),
	)
}

type nativeDebugFileLookup struct {
	store *DebugFileStore
}

func (n nativeDebugFileLookup) LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*nativesym.File, []byte, error) {
	_, file, body, err := n.store.lookupNativeSymbolSource(ctx, projectID, releaseVersion, kind, NativeLookupInput{DebugID: debugID})
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}

func (n nativeDebugFileLookup) LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*nativesym.File, []byte, error) {
	_, file, body, err := n.store.lookupNativeSymbolSource(ctx, projectID, releaseVersion, kind, NativeLookupInput{CodeID: codeID, BuildID: codeID})
	if err != nil || file == nil {
		return nil, body, err
	}
	return &nativesym.File{ID: file.ID, CodeID: file.CodeID, Kind: file.Kind}, body, nil
}
