package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/attachment"
	"urgentry/internal/store"
	"urgentry/pkg/id"
)

// AttachmentStore persists attachment metadata in SQLite and bytes in a blob store.
type AttachmentStore struct {
	db    *sql.DB
	blobs store.BlobStore
}

// NewAttachmentStore creates an AttachmentStore.
func NewAttachmentStore(db *sql.DB, blobs store.BlobStore) *AttachmentStore {
	return &AttachmentStore{db: db, blobs: blobs}
}

var _ attachment.Store = (*AttachmentStore)(nil)

// SaveAttachment stores attachment content and metadata.
func (s *AttachmentStore) SaveAttachment(ctx context.Context, a *attachment.Attachment, data []byte) error {
	if a == nil {
		return errors.New("attachment is nil")
	}
	if s.blobs == nil {
		return errors.New("attachment blob store is not configured")
	}
	if a.ProjectID == "" || a.EventID == "" {
		return errors.New("attachment project_id and event_id are required")
	}
	if a.ID == "" {
		a.ID = id.New()
	}
	if a.Name == "" {
		a.Name = "unnamed"
	}
	a.Size = int64(len(data))
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.ObjectKey = attachmentObjectKey(a.ProjectID, a.EventID, a.ID)

	if err := s.blobs.Put(ctx, a.ObjectKey, data); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO event_attachments
			(id, project_id, event_id, name, content_type, size_bytes, object_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ProjectID, a.EventID, a.Name, a.ContentType, a.Size, a.ObjectKey, a.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		_ = s.blobs.Delete(ctx, a.ObjectKey)
	}
	return err
}

// GetAttachment retrieves attachment metadata and content by ID.
func (s *AttachmentStore) GetAttachment(ctx context.Context, id string) (*attachment.Attachment, []byte, error) {
	var a attachment.Attachment
	var createdAt sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event_id, name, COALESCE(content_type, ''), size_bytes, object_key, created_at
		 FROM event_attachments WHERE id = ?`, id,
	).Scan(&a.ID, &a.ProjectID, &a.EventID, &a.Name, &a.ContentType, &a.Size, &a.ObjectKey, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if createdAt.Valid {
		a.CreatedAt = parseTime(createdAt.String)
	}

	data, err := s.blobs.Get(ctx, a.ObjectKey)
	if err != nil {
		if restoreErr := restoreArchivedBlob(ctx, s.db, s.blobs, a.ProjectID, "attachment", a.ID, a.ObjectKey); restoreErr != nil {
			return &a, nil, restoreErr
		}
		data, err = s.blobs.Get(ctx, a.ObjectKey)
		if err != nil {
			return &a, nil, fmt.Errorf("load attachment blob: %w", err)
		}
	}
	return &a, data, nil
}

// ListByEvent returns attachment metadata for an event ordered by recency.
func (s *AttachmentStore) ListByEvent(ctx context.Context, eventID string) ([]*attachment.Attachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event_id, name, COALESCE(content_type, ''), size_bytes, object_key, created_at
		 FROM event_attachments WHERE event_id = ?
		 ORDER BY created_at DESC, id DESC`,
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*attachment.Attachment
	for rows.Next() {
		var a attachment.Attachment
		var createdAt sql.NullString
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.EventID, &a.Name, &a.ContentType, &a.Size, &a.ObjectKey, &createdAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			a.CreatedAt = parseTime(createdAt.String)
		}
		result = append(result, &a)
	}
	return result, rows.Err()
}

func attachmentObjectKey(projectID, eventID, attachmentID string) string {
	return fmt.Sprintf("attachments/%s/%s/%s",
		sanitizeKeySegment(projectID),
		sanitizeKeySegment(eventID),
		sanitizeKeySegment(attachmentID),
	)
}

func sanitizeKeySegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(s)
}
