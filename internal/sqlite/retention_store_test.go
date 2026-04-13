package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func seedReplayRetentionFixture(t *testing.T, db *sql.DB, blobs store.BlobStore, projectID, replayID string, agedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	payload := []byte(`{"event_id":"` + replayID + `","replay_id":"` + replayID + `","timestamp":"` + agedAt.Format(time.RFC3339) + `","request":{"url":"https://app.example.com/checkout?token=secret"},"user":{"email":"dev@example.com"}}`)
	if _, err := replays.SaveEnvelopeReplay(ctx, projectID, replayID, payload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-" + replayID,
		ProjectID:   projectID,
		EventID:     replayID,
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   agedAt,
	}, []byte(`{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com/checkout?token=secret"}},{"type":"error","offset_ms":30,"data":{"message":"boom"}}]}`)); err != nil {
		t.Fatalf("SaveAttachment(replay): %v", err)
	}
	if err := replays.IndexReplay(ctx, projectID, replayID); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET ingested_at = ?, occurred_at = ? WHERE project_id = ? AND event_id = ? AND event_type = 'replay'`, agedAt.Format(time.RFC3339), agedAt.Format(time.RFC3339), projectID, replayID); err != nil {
		t.Fatalf("age replay event: %v", err)
	}
	if _, err := db.Exec(`UPDATE replay_manifests SET created_at = ?, updated_at = ?, started_at = ?, ended_at = ? WHERE project_id = ? AND replay_id = ?`, agedAt.Format(time.RFC3339), agedAt.Format(time.RFC3339), agedAt.Format(time.RFC3339), agedAt.Add(30*time.Millisecond).Format(time.RFC3339Nano), projectID, replayID); err != nil {
		t.Fatalf("age replay manifest: %v", err)
	}
	if _, err := db.Exec(`UPDATE replay_assets SET created_at = ? WHERE replay_id = ?`, agedAt.Format(time.RFC3339), replayID); err != nil {
		t.Fatalf("age replay assets: %v", err)
	}
}

func TestRetentionStoreApplyDeletePolicies(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, event_retention_days, attachment_retention_days, debug_file_retention_days) VALUES ('proj-1', 'org-1', 'proj', 'Project', 1, 1, 1)`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceErrors, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceProfiles, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceTraces, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceOutcomes, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceReplays, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceAttachments, 1, store.TelemetryStorageTierDelete, 0)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceDebugFiles, 1, store.TelemetryStorageTierDelete, 0)

	blobs := store.NewMemoryBlobStore()
	old := time.Now().UTC().Add(-72 * time.Hour)
	profiles := NewProfileStore(db, blobs)

	if _, err := db.Exec(`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, first_seen, last_seen, times_seen) VALUES ('grp-1', 'proj-1', 'urgentry-v1', 'k1', 'Old group', ?, ?, 1)`, old.Format(time.RFC3339), old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, project_id, event_id, group_id, title, ingested_at, event_type) VALUES ('evt-1', 'proj-1', 'event-1', 'grp-1', 'Old event', ?, 'error')`, old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := blobs.Put(context.Background(), "attachments/proj-1/event-1/att-1", []byte("old-attachment")); err != nil {
		t.Fatalf("put attachment blob: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_attachments (id, project_id, event_id, name, object_key, created_at) VALUES ('att-1', 'proj-1', 'event-1', 'trace.txt', 'attachments/proj-1/event-1/att-1', ?)`, old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if err := blobs.Put(context.Background(), "payloads/txn-1", []byte(`{"transaction":"checkout"}`)); err != nil {
		t.Fatalf("put transaction payload: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO transactions
		(id, project_id, event_id, trace_id, span_id, transaction_name, start_timestamp, end_timestamp, duration_ms, tags_json, measurements_json, payload_json, payload_key, created_at)
		VALUES ('txn-1', 'proj-1', 'txn-event-1', 'trace-1', 'span-root', 'checkout', ?, ?, 25, '{}', '{}', '{}', 'payloads/txn-1', ?)`,
		old.Add(-25*time.Millisecond).Format(time.RFC3339Nano),
		old.Format(time.RFC3339Nano),
		old.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO spans
		(id, project_id, transaction_event_id, trace_id, span_id, parent_span_id, op, description, status, start_timestamp, end_timestamp, duration_ms, tags_json, data_json, created_at)
		VALUES ('span-1', 'proj-1', 'txn-event-1', 'trace-1', 'span-child', 'span-root', 'db', 'query', 'ok', ?, ?, 5, '{}', '{}', ?)`,
		old.Add(-10*time.Millisecond).Format(time.RFC3339Nano),
		old.Format(time.RFC3339Nano),
		old.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert span: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO outcomes (id, project_id, category, reason, quantity, source, recorded_at, created_at) VALUES ('outcome-1', 'proj-1', 'error', 'sample_rate', 3, 'client_report', ?, ?)`, old.Format(time.RFC3339), old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}
	seedReplayRetentionFixture(t, db, blobs, "proj-1", "replay-delete-1", old)
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.SaveRead().Spec().
		WithIDs("evt-profile-1", "profile-delete-1").
		WithTimestamp(old))
	if _, err := db.Exec(`UPDATE events SET ingested_at = ?, occurred_at = ? WHERE event_id = 'evt-profile-1'`, old.Format(time.RFC3339), old.Format(time.RFC3339)); err != nil {
		t.Fatalf("age profile event: %v", err)
	}
	if err := blobs.Put(context.Background(), "debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip", []byte("debug-file")); err != nil {
		t.Fatalf("put debug blob: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO debug_files (id, project_id, release_version, uuid, name, object_key, created_at, kind) VALUES ('file-1', 'proj-1', 'ios@1.0.0', '', 'App.dSYM.zip', 'debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip', ?, 'apple')`, old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert debug file: %v", err)
	}

	report, err := NewRetentionStore(db, blobs).Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.ErrorsDeleted != 1 || report.ProfilesDeleted != 1 || report.TracesDeleted != 1 || report.OutcomesDeleted != 1 || report.ReplaysDeleted != 1 || report.AttachmentsDeleted != 1 || report.DebugFilesDeleted != 1 || report.GroupsDeleted != 1 {
		t.Fatalf("unexpected retention report: %+v", report)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE id = 'evt-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE event_id = 'evt-profile-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE event_id = 'replay-delete-1' AND event_type = 'replay'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM profile_manifests WHERE profile_id = 'profile-delete-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE replay_id = 'replay-delete-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_assets WHERE replay_id = 'replay-delete-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-delete-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE id = 'att-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE id = 'att-replay-delete-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM transactions WHERE id = 'txn-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM spans WHERE id = 'span-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM outcomes WHERE id = 'outcome-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM debug_files WHERE id = 'file-1'`, 0)

	if _, err := blobs.Get(context.Background(), "attachments/proj-1/event-1/att-1"); err == nil {
		t.Fatal("expected attachment blob to be deleted")
	}
	if _, err := blobs.Get(context.Background(), "attachments/proj-1/replay-delete-1/att-replay-delete-1"); err == nil {
		t.Fatal("expected replay attachment blob to be deleted")
	}
	if _, err := blobs.Get(context.Background(), "payloads/txn-1"); err == nil {
		t.Fatal("expected transaction payload blob to be deleted")
	}
	if _, err := blobs.Get(context.Background(), "debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip"); err == nil {
		t.Fatal("expected debug blob to be deleted")
	}
}

func TestRetentionStoreArchiveAndRestore(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, event_retention_days, attachment_retention_days, debug_file_retention_days) VALUES ('proj-1', 'org-1', 'proj', 'Project', 1, 1, 1)`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceReplays, 1, store.TelemetryStorageTierArchive, 30)
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceDebugFiles, 1, store.TelemetryStorageTierArchive, 30)

	blobs := store.NewMemoryBlobStore()
	old := time.Now().UTC().Add(-72 * time.Hour)
	seedReplayRetentionFixture(t, db, blobs, "proj-1", "replay-1", old)
	if err := blobs.Put(context.Background(), "debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip", []byte("debug-file")); err != nil {
		t.Fatalf("put debug blob: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO debug_files (id, project_id, release_version, uuid, name, object_key, created_at, kind) VALUES ('file-1', 'proj-1', 'ios@1.0.0', '', 'App.dSYM.zip', 'debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip', ?, 'apple')`, old.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert debug file: %v", err)
	}

	report, err := NewRetentionStore(db, blobs).Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.ReplaysArchived != 1 || report.DebugFilesArchived != 1 || report.ReplaysDeleted != 0 || report.DebugFilesDeleted != 0 {
		t.Fatalf("unexpected archive report: %+v", report)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE event_id = 'replay-1' AND event_type = 'replay'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE replay_id = 'replay-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_assets WHERE replay_id = 'replay-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE id = 'att-replay-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM debug_files WHERE id = 'file-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM telemetry_archives WHERE project_id = 'proj-1' AND restored_at IS NULL`, 3)

	if _, err := blobs.Get(context.Background(), "attachments/proj-1/replay-1/att-replay-1"); err == nil {
		t.Fatal("expected live attachment blob to move to archive")
	}
	if _, err := blobs.Get(context.Background(), "debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip"); err == nil {
		t.Fatal("expected live debug blob to move to archive")
	}
	if _, err := blobs.Get(context.Background(), telemetryArchiveBlobKey("proj-1", store.TelemetrySurfaceReplays, "attachment", "att-replay-1")); err != nil {
		t.Fatalf("expected archived attachment blob: %v", err)
	}
	if _, err := blobs.Get(context.Background(), telemetryArchiveBlobKey("proj-1", store.TelemetrySurfaceDebugFiles, "debug_file", "file-1")); err != nil {
		t.Fatalf("expected archived debug blob: %v", err)
	}
	restored, err := NewRetentionStore(db, blobs).RestoreSurface(context.Background(), "proj-1", store.TelemetrySurfaceReplays, 10)
	if err != nil {
		t.Fatalf("RestoreSurface(replays): %v", err)
	}
	if restored != 2 {
		t.Fatalf("RestoreSurface(replays) restored %d rows, want 2", restored)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM events WHERE event_id = 'replay-1' AND event_type = 'replay'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM event_attachments WHERE id = 'att-replay-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE replay_id = 'replay-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_assets WHERE replay_id = 'replay-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-1'`, 2)
	assertCount(t, db, `SELECT COUNT(*) FROM telemetry_archives WHERE project_id = 'proj-1' AND surface = 'replays' AND restored_at IS NULL`, 0)

	attachmentStore := NewAttachmentStore(db, blobs)
	att, attBody, err := attachmentStore.GetAttachment(context.Background(), "att-replay-1")
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if att == nil || !strings.Contains(string(attBody), "\"navigation\"") {
		t.Fatalf("unexpected restored attachment: %+v body=%q", att, string(attBody))
	}
	replay, err := NewReplayStore(db, blobs).GetReplay(context.Background(), "proj-1", "replay-1")
	if err != nil {
		t.Fatalf("GetReplay restored: %v", err)
	}
	if replay.Manifest.AssetCount != 1 || len(replay.Timeline) != 2 {
		t.Fatalf("unexpected restored replay: %+v", replay.Manifest)
	}

	debugStore := NewDebugFileStore(db, blobs)
	file, debugBody, err := debugStore.Get(context.Background(), "file-1")
	if err != nil {
		t.Fatalf("Get debug file: %v", err)
	}
	if file == nil || string(debugBody) != "debug-file" {
		t.Fatalf("unexpected restored debug file: %+v body=%q", file, string(debugBody))
	}

	if _, err := blobs.Get(context.Background(), "attachments/proj-1/replay-1/att-replay-1"); err != nil {
		t.Fatalf("expected restored attachment blob: %v", err)
	}
	if _, err := blobs.Get(context.Background(), "debug/apple/proj-1/ios@1.0.0/file-1/App.dSYM.zip"); err != nil {
		t.Fatalf("expected restored debug blob: %v", err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM telemetry_archives WHERE project_id = 'proj-1' AND restored_at IS NULL`, 0)
}

func TestRetentionStoreArchiveAndRestoreProfiles(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, event_retention_days, attachment_retention_days, debug_file_retention_days) VALUES ('proj-1', 'org-1', 'proj', 'Project', 1, 1, 1)`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertTelemetryPolicy(t, db, "proj-1", store.TelemetrySurfaceProfiles, 1, store.TelemetryStorageTierArchive, 30)

	blobs := store.NewMemoryBlobStore()
	profiles := NewProfileStore(db, blobs)
	old := time.Now().UTC().Add(-72 * time.Hour)
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.SaveRead().Spec().
		WithIDs("evt-profile-archive-1", "profile-archive-1").
		WithTimestamp(old))
	if _, err := db.Exec(`UPDATE events SET ingested_at = ?, occurred_at = ? WHERE event_id = 'evt-profile-archive-1'`, old.Format(time.RFC3339), old.Format(time.RFC3339)); err != nil {
		t.Fatalf("age profile event: %v", err)
	}

	report, err := NewRetentionStore(db, blobs).Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if report.ProfilesArchived != 1 || report.ProfilesDeleted != 0 {
		t.Fatalf("unexpected archive report: %+v", report)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM profile_manifests WHERE profile_id = 'profile-archive-1'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM telemetry_archives WHERE project_id = 'proj-1' AND surface = 'profiles' AND restored_at IS NULL`, 1)

	restored, err := NewRetentionStore(db, blobs).RestoreSurface(context.Background(), "proj-1", store.TelemetrySurfaceProfiles, 10)
	if err != nil {
		t.Fatalf("RestoreSurface(profiles): %v", err)
	}
	if restored != 1 {
		t.Fatalf("RestoreSurface(profiles) restored %d rows, want 1", restored)
	}

	record, err := profiles.GetProfile(context.Background(), "proj-1", "profile-archive-1")
	if err != nil {
		t.Fatalf("GetProfile restored: %v", err)
	}
	if record.Manifest.SampleCount != profilefixtures.SaveRead().Expected.SampleCount || len(record.TopFrames) == 0 || record.TopFrames[0].Name != profilefixtures.SaveRead().Expected.TopFrame {
		t.Fatalf("unexpected restored profile: %+v", record)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM telemetry_archives WHERE project_id = 'proj-1' AND surface = 'profiles' AND restored_at IS NULL`, 0)
}

func insertTelemetryPolicy(t *testing.T, db *sql.DB, projectID string, surface store.TelemetrySurface, days int, tier store.TelemetryStorageTier, archiveDays int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO telemetry_retention_policies (project_id, surface, retention_days, storage_tier, archive_retention_days) VALUES (?, ?, ?, ?, ?)`,
		projectID, string(surface), days, string(tier), archiveDays,
	); err != nil {
		t.Fatalf("insert telemetry policy %s: %v", surface, err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query count %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}
