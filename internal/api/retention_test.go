package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestRetentionArchiveAndRestoreReplays(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	blobStore := store.NewMemoryBlobStore()
	attachments := sqlite.NewAttachmentStore(db, blobStore)
	replays := sqlite.NewReplayStore(db, blobStore)
	events := sqlite.NewEventStore(db)

	payload := []byte(`{
		"event_id":"evt-retention-replay-1",
		"replay_id":"replay-retention-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@9.9.9",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"}
	}`)
	if _, err := replays.SaveEnvelopeReplay(t.Context(), "test-proj-id", "evt-retention-replay-1", payload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := events.SaveEvent(t.Context(), &store.StoredEvent{
		ID:             "evt-retention-linked-row",
		ProjectID:      "test-proj-id",
		EventID:        "evt-retention-linked",
		GroupID:        "grp-retention-linked",
		EventType:      "error",
		Platform:       "javascript",
		Level:          "error",
		OccurredAt:     time.Now().UTC(),
		IngestedAt:     time.Now().UTC(),
		NormalizedJSON: []byte(`{"event_id":"evt-retention-linked"}`),
	}); err != nil {
		t.Fatalf("SaveEvent linked: %v", err)
	}
	recording := []byte(`{"events":[{"type":"navigation","offset_ms":5,"data":{"url":"https://app.example.com"}},{"type":"error","offset_ms":25,"data":{"event_id":"evt-retention-linked","message":"boom"}}]}`)
	if err := attachments.SaveAttachment(t.Context(), &attachmentstore.Attachment{
		ID:          "att-retention-replay-1",
		EventID:     "evt-retention-replay-1",
		ProjectID:   "test-proj-id",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, recording); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := replays.IndexReplay(t.Context(), "test-proj-id", "replay-retention-1"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	agedAt := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE events SET occurred_at = ?, ingested_at = ? WHERE event_id = 'evt-retention-replay-1'`, agedAt, agedAt); err != nil {
		t.Fatalf("age replay event: %v", err)
	}
	if _, err := db.Exec(`UPDATE replay_manifests SET created_at = ?, updated_at = ?, started_at = ?, ended_at = ? WHERE project_id = 'test-proj-id' AND replay_id = 'replay-retention-1'`, agedAt, agedAt, agedAt, time.Now().UTC().Add(-47*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("age replay manifest: %v", err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO telemetry_retention_policies (project_id, surface, retention_days, storage_tier, archive_retention_days, created_at, updated_at) VALUES ('test-proj-id', 'replays', 1, 'archive', 30, ?, ?)`, agedAt, agedAt); err != nil {
		t.Fatalf("set replay retention policy: %v", err)
	}

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{
		Attachments: attachments,
		BlobStore:   blobStore,
	})
	defer ts.Close()

	before := authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-retention-1/manifest/")
	if before.StatusCode != http.StatusOK {
		t.Fatalf("manifest before archive status = %d, want 200", before.StatusCode)
	}
	before.Body.Close()

	archive := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/retention/replays/archive/", pat, nil)
	if archive.StatusCode != http.StatusOK {
		t.Fatalf("archive status = %d, want 200", archive.StatusCode)
	}
	var archived RetentionExecution
	decodeBody(t, archive, &archived)
	if archived.Archived < 1 || archived.Surface != "replays" || len(archived.Archives) < 2 {
		t.Fatalf("unexpected archive response: %+v", archived)
	}
	foundBlobArchive := false
	for _, entry := range archived.Archives {
		if strings.Contains(entry.ArchiveKey, "archives/test-proj-id/replays/") {
			foundBlobArchive = true
			break
		}
	}
	if !foundBlobArchive {
		t.Fatalf("expected at least one blob-backed replay archive: %+v", archived.Archives)
	}

	afterArchive := authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-retention-1/manifest/")
	if afterArchive.StatusCode != http.StatusNotFound {
		t.Fatalf("manifest after archive status = %d, want 404", afterArchive.StatusCode)
	}
	afterArchive.Body.Close()

	list := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/retention/replays/archives/", pat, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list archives status = %d, want 200", list.StatusCode)
	}
	var listed []RetentionArchiveEntry
	decodeBody(t, list, &listed)
	if len(listed) < 2 {
		t.Fatalf("archive listing length = %d, want >= 2", len(listed))
	}

	restore := authzJSONRequest(t, ts, http.MethodPost, "/api/0/projects/test-org/test-project/retention/replays/restore/", pat, map[string]any{"limit": 10})
	if restore.StatusCode != http.StatusOK {
		t.Fatalf("restore status = %d, want 200", restore.StatusCode)
	}
	var restored RetentionExecution
	decodeBody(t, restore, &restored)
	if restored.Restored < 2 {
		t.Fatalf("unexpected restore response: %+v", restored)
	}

	afterRestore := authGet(t, ts, "/api/0/projects/test-org/test-project/replays/replay-retention-1/manifest/")
	if afterRestore.StatusCode != http.StatusOK {
		t.Fatalf("manifest after restore status = %d, want 200", afterRestore.StatusCode)
	}
	afterRestore.Body.Close()
}
