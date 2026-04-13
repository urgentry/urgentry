package blob

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestResolverReadsDirectObject(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	blobs := store.NewMemoryBlobStore()
	locator := SourceMap("proj-1", "artifact-1", "sourcemaps/proj-1/release/app.js.map")
	if err := blobs.Put(context.Background(), locator.ObjectKey, []byte(`{"version":3}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	body, err := NewResolver(db, blobs).Read(context.Background(), locator)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(body) != `{"version":3}` {
		t.Fatalf("body = %q", string(body))
	}
}

func TestResolverRestoresArchivedObject(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('proj-1', 'org-1', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	blobs := store.NewMemoryBlobStore()
	locator := EventPayload("proj-1", "event-row-1", "profiles/proj-1/profile-1.raw")
	archiveKey := "archives/proj-1/profiles/event/event-row-1"
	if err := blobs.Put(context.Background(), archiveKey, []byte(`{"profile_id":"profile-1"}`)); err != nil {
		t.Fatalf("Put archive: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO telemetry_archives (id, project_id, surface, record_type, record_id, archive_key, metadata_json, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"archive-1",
		"proj-1",
		"profiles",
		"event",
		"event-row-1",
		archiveKey,
		`{}`,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert telemetry archive: %v", err)
	}

	body, err := NewResolver(db, blobs).Read(context.Background(), locator)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(body) != `{"profile_id":"profile-1"}` {
		t.Fatalf("body = %q", string(body))
	}
	if _, err := blobs.Get(context.Background(), locator.ObjectKey); err != nil {
		t.Fatalf("restored object missing: %v", err)
	}
	var restoredAt string
	if err := db.QueryRow(`SELECT COALESCE(restored_at, '') FROM telemetry_archives WHERE id = 'archive-1'`).Scan(&restoredAt); err != nil {
		t.Fatalf("query restored_at: %v", err)
	}
	if restoredAt == "" {
		t.Fatal("expected restored_at to be set")
	}
}
