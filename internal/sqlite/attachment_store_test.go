package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/attachment"
	"urgentry/internal/store"
)

func seedAttachmentProject(t *testing.T, db *sql.DB, projectID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES (?, 'org-1', ?, 'Project')`, projectID, projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func TestAttachmentStore_SaveGetList(t *testing.T) {
	db := openStoreTestDB(t)
	seedAttachmentProject(t, db, "proj-1")
	blobs := store.NewMemoryBlobStore()
	as := NewAttachmentStore(db, blobs)
	ctx := context.Background()

	att := &attachment.Attachment{
		ProjectID:   "proj-1",
		EventID:     "evt-1",
		Name:        "screenshot.png",
		ContentType: "image/png",
	}
	data := []byte("attachment-bytes")

	if err := as.SaveAttachment(ctx, att, data); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if att.ID == "" {
		t.Fatal("expected attachment ID to be set")
	}
	if att.ObjectKey == "" {
		t.Fatal("expected object key to be set")
	}
	if att.CreatedAt.IsZero() {
		t.Fatal("expected created at to be set")
	}

	got, payload, err := as.GetAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got == nil {
		t.Fatal("GetAttachment returned nil")
	}
	if got.ID != att.ID {
		t.Fatalf("ID = %q, want %q", got.ID, att.ID)
	}
	if got.Name != att.Name {
		t.Fatalf("Name = %q, want %q", got.Name, att.Name)
	}
	if got.ContentType != att.ContentType {
		t.Fatalf("ContentType = %q, want %q", got.ContentType, att.ContentType)
	}
	if got.Size != int64(len(data)) {
		t.Fatalf("Size = %d, want %d", got.Size, len(data))
	}
	if got.ObjectKey != att.ObjectKey {
		t.Fatalf("ObjectKey = %q, want %q", got.ObjectKey, att.ObjectKey)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("expected created at from DB")
	}
	if time.Since(got.CreatedAt) > time.Minute {
		t.Fatalf("CreatedAt looks stale: %v", got.CreatedAt)
	}
	if string(payload) != string(data) {
		t.Fatalf("payload = %q, want %q", payload, data)
	}

	list, err := as.ListByEvent(ctx, "evt-1")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByEvent returned %d items, want 1", len(list))
	}
	if list[0].ID != att.ID {
		t.Fatalf("ListByEvent[0].ID = %q, want %q", list[0].ID, att.ID)
	}
	if list[0].ObjectKey != att.ObjectKey {
		t.Fatalf("ListByEvent[0].ObjectKey = %q, want %q", list[0].ObjectKey, att.ObjectKey)
	}
}

func TestAttachmentStoreRestoresArchivedBlob(t *testing.T) {
	db := openStoreTestDB(t)
	seedAttachmentProject(t, db, "proj-1")
	blobs := store.NewMemoryBlobStore()
	as := NewAttachmentStore(db, blobs)
	ctx := context.Background()

	att := &attachment.Attachment{
		ProjectID:   "proj-1",
		EventID:     "evt-archived",
		Name:        "report.txt",
		ContentType: "text/plain",
	}
	if err := as.SaveAttachment(ctx, att, []byte("archived-attachment")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	archiveKey := telemetryArchiveBlobKey("proj-1", store.TelemetrySurfaceAttachments, "attachment", att.ID)
	body, err := blobs.Get(ctx, att.ObjectKey)
	if err != nil {
		t.Fatalf("Get original blob: %v", err)
	}
	if err := blobs.Put(ctx, archiveKey, body); err != nil {
		t.Fatalf("Put archive blob: %v", err)
	}
	if err := blobs.Delete(ctx, att.ObjectKey); err != nil {
		t.Fatalf("Delete original blob: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_archives (id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES ('archive-1', 'proj-1', 'attachments', 'attachment', ?, ?, '{}', ?)`,
		att.ID,
		archiveKey,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert telemetry archive: %v", err)
	}

	got, payload, err := as.GetAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got == nil || string(payload) != "archived-attachment" {
		t.Fatalf("GetAttachment restored payload = %q, want archived-attachment", payload)
	}
	if _, err := blobs.Get(ctx, archiveKey); err == nil {
		t.Fatal("expected archive blob to be removed after restore")
	}
}
