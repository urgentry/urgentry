package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"urgentry/internal/alert"
	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/migration"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	memorystore "urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

type failingBlobStore struct {
	store  memorystore.BlobStore
	getErr error
	delErr error
}

func (f *failingBlobStore) Put(ctx context.Context, key string, data []byte) error {
	return f.store.Put(ctx, key, data)
}

func (f *failingBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.store.Get(ctx, key)
}

func (f *failingBlobStore) Delete(ctx context.Context, key string) error {
	if f.delErr != nil {
		return f.delErr
	}
	return f.store.Delete(ctx, key)
}

func TestImportExportStoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	sourceDB := openSQLiteTestDB(t)
	sourceBlobs := memorystore.NewMemoryBlobStore()
	sourceAttachments := NewAttachmentStore(sourceDB, sourceBlobs)
	sourceSourceMaps := NewSourceMapStore(sourceDB, sourceBlobs)
	sourceProGuard := NewProGuardStore(sourceDB, sourceBlobs)
	sourceDebugFiles := NewDebugFileStore(sourceDB, sourceBlobs)
	sourceProfiles := NewProfileStore(sourceDB, sourceBlobs)
	sourceReplays := NewReplayStore(sourceDB, sourceBlobs)

	seedImportExportOrganization(t, sourceDB)
	seedImportExportAuth(t, sourceDB)
	seedImportExportRelease(t, sourceDB, "1.2.3")
	seedImportExportIssueAndEvent(t, sourceDB, "grp-import-1", "evt-import-1", "ImportError: boom", "1.2.3")
	if err := NewAlertStore(sourceDB).CreateRule(ctx, &alert.Rule{
		ID:        "alert-import-1",
		ProjectID: "test-proj-id",
		Name:      "Import rule",
		Status:    "active",
		Conditions: []alert.Condition{{
			ID:   alert.ConditionEveryEvent,
			Name: "Every event",
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := sourceAttachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-import-1",
		EventID:     "evt-import-1",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("attachment payload")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := sourceSourceMaps.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-import-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		Name:      "app.min.js.map",
		Checksum:  "sha1-source-map",
		CreatedAt: time.Now().UTC(),
	}, []byte(`{"version":3,"file":"app.min.js"}`)); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if err := sourceProGuard.SaveMapping(ctx, &proguard.Mapping{
		ID:        "pg-import-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		UUID:      "UUID-IMPORT-1",
		Name:      "mapping.txt",
		Checksum:  "sha1-proguard",
		CreatedAt: time.Now().UTC(),
	}, []byte("com.example.Foo -> a:")); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}
	if err := sourceDebugFiles.Save(ctx, &DebugFile{
		ID:          "dbg-import-1",
		ProjectID:   "test-proj-id",
		ReleaseID:   "1.2.3",
		Kind:        "macho",
		Name:        "App.dSYM.zip",
		UUID:        "DEBUG-IMPORT-1",
		CodeID:      "CODE-IMPORT-1",
		ContentType: "application/zip",
		CreatedAt:   time.Now().UTC(),
	}, []byte("MODULE mac arm64 DEBUG-IMPORT-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}
	profilefixtures.Save(t, sourceProfiles, "test-proj-id", profilefixtures.SaveRead().Spec().
		WithIDs("evt-import-profile-1", "profile-import-1"))
	replayPayload := []byte(`{
		"event_id":"evt-import-replay-1",
		"replay_id":"replay-import-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"}
	}`)
	if _, err := sourceReplays.SaveEnvelopeReplay(ctx, "test-proj-id", "evt-import-replay-1", replayPayload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	}
	if err := sourceAttachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-import-replay-1",
		EventID:     "evt-import-replay-1",
		ProjectID:   "test-proj-id",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, []byte(`{"events":[{"type":"navigation","offset_ms":5,"data":{"url":"https://app.example.com/checkout"}},{"type":"click","offset_ms":25,"data":{"selector":"button.pay","text":"Pay"}}]}`)); err != nil {
		t.Fatalf("Save replay attachment: %v", err)
	}

	sourceStore := NewImportExportStore(sourceDB, sourceAttachments, sourceProGuard, sourceSourceMaps, sourceBlobs)
	var exported bytes.Buffer
	if err := sourceStore.WriteOrganizationPayloadJSON(ctx, "test-org", &exported); err != nil {
		t.Fatalf("WriteOrganizationPayloadJSON: %v", err)
	}
	var payload migration.ImportPayload
	if err := json.Unmarshal(exported.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if len(payload.Projects) != 1 || len(payload.Releases) != 1 || len(payload.Issues) != 1 || len(payload.Events) != 3 {
		t.Fatalf("unexpected export counts: %+v", payload)
	}
	if len(payload.ProjectKeys) != 1 || len(payload.AlertRules) != 1 || len(payload.Members) != 1 || len(payload.Artifacts) != 6 {
		t.Fatalf("unexpected export coverage: %+v", payload)
	}
	for _, artifact := range payload.Artifacts {
		if artifact.BodyBase64 == "" {
			t.Fatalf("artifact %s missing body data: %+v", artifact.Name, artifact)
		}
	}
	for i := range payload.Events {
		if payload.Events[i].EventType == "profile" {
			payload.Events[i].PayloadJSON = ""
		}
	}

	targetDB := openSQLiteTestDB(t)
	targetBlobs := memorystore.NewMemoryBlobStore()
	targetAttachments := NewAttachmentStore(targetDB, targetBlobs)
	targetSourceMaps := NewSourceMapStore(targetDB, targetBlobs)
	targetProGuard := NewProGuardStore(targetDB, targetBlobs)
	targetDebugFiles := NewDebugFileStore(targetDB, targetBlobs)
	seedImportExportOrganization(t, targetDB)

	targetStore := NewImportExportStore(targetDB, targetAttachments, targetProGuard, targetSourceMaps, targetBlobs)
	result, err := targetStore.ImportOrganizationPayload(ctx, "test-org-id", "test-org", payload)
	if err != nil {
		t.Fatalf("ImportOrganizationPayload: %v", err)
	}
	if result.ProjectsImported != 1 || result.ReleasesImported != 1 || result.IssuesImported != 1 || result.EventsImported != 3 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if result.ProjectKeysImported != 1 || result.AlertRulesImported != 1 || result.MembersImported != 1 || result.ArtifactsImported != 6 || result.ArtifactsVerified != 6 {
		t.Fatalf("unexpected import coverage: %+v", result)
	}

	attachments, err := targetAttachments.ListByEvent(ctx, "evt-import-1")
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Name != "crash.txt" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
	_, attachmentBody, err := targetAttachments.GetAttachment(ctx, attachments[0].ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if string(attachmentBody) != "attachment payload" {
		t.Fatalf("attachment body = %q, want attachment payload", string(attachmentBody))
	}

	sourceMap, sourceMapBody, err := targetSourceMaps.LookupByName(ctx, "test-proj-id", "1.2.3", "app.min.js.map")
	if err != nil {
		t.Fatalf("LookupByName: %v", err)
	}
	if sourceMap == nil || string(sourceMapBody) != `{"version":3,"file":"app.min.js"}` {
		t.Fatalf("unexpected source map: %+v body=%q", sourceMap, string(sourceMapBody))
	}

	mapping, mappingBody, err := targetProGuard.LookupByUUID(ctx, "test-proj-id", "1.2.3", "UUID-IMPORT-1")
	if err != nil {
		t.Fatalf("LookupByUUID: %v", err)
	}
	if mapping == nil || string(mappingBody) != "com.example.Foo -> a:" {
		t.Fatalf("unexpected mapping: %+v body=%q", mapping, string(mappingBody))
	}

	debugFile, debugBody, err := targetDebugFiles.LookupByDebugID(ctx, "test-proj-id", "1.2.3", "macho", "DEBUG-IMPORT-1")
	if err != nil {
		t.Fatalf("LookupByDebugID: %v", err)
	}
	if debugFile == nil || debugFile.Kind != "macho" || string(debugBody) != "MODULE mac arm64 DEBUG-IMPORT-1 App\nFILE 0 src/AppDelegate.swift\nFUNC 1010 10 0 main\n1010 10 42 0\n" {
		t.Fatalf("unexpected debug file: %+v body=%q", debugFile, string(debugBody))
	}
	sources, err := targetDebugFiles.ListNativeSymbolSourcesByRelease(ctx, "test-proj-id", "1.2.3")
	if err != nil {
		t.Fatalf("ListNativeSymbolSourcesByRelease: %v", err)
	}
	if len(sources) != 1 || sources[0].DebugID != "debug-import-1" || sources[0].ModuleName != "app" || sources[0].Platform != "mac" {
		t.Fatalf("unexpected imported native symbol sources: %+v", sources)
	}

	targetProfiles := NewProfileStore(targetDB, targetBlobs)
	profile, err := targetProfiles.GetProfile(ctx, "test-proj-id", "profile-import-1")
	if err != nil {
		t.Fatalf("GetProfile imported: %v", err)
	}
	if profile.Manifest.SampleCount != profilefixtures.SaveRead().Expected.SampleCount || len(profile.TopFrames) == 0 || profile.TopFrames[0].Name != profilefixtures.SaveRead().Expected.TopFrame {
		t.Fatalf("unexpected imported profile: %+v", profile)
	}
	if _, err := targetBlobs.Get(ctx, profile.Manifest.RawBlobKey); err != nil {
		t.Fatalf("Get imported raw profile blob: %v", err)
	}

	targetReplays := NewReplayStore(targetDB, targetBlobs)
	if err := targetReplays.IndexReplay(ctx, "test-proj-id", "replay-import-1"); err != nil {
		t.Fatalf("IndexReplay imported: %v", err)
	}
	replay, err := targetReplays.GetReplay(ctx, "test-proj-id", "replay-import-1")
	if err != nil {
		t.Fatalf("GetReplay imported: %v", err)
	}
	if replay.Manifest.AssetCount != 1 || len(replay.Timeline) == 0 {
		t.Fatalf("unexpected imported replay: %+v", replay.Manifest)
	}
}

func TestImportExportStoreImportIsAtomic(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()
	seedImportExportOrganization(t, db)

	store := NewImportExportStore(db, NewAttachmentStore(db, blobs), NewProGuardStore(db, blobs), NewSourceMapStore(db, blobs), blobs)
	payload := migration.ImportPayload{
		Projects: []migration.ProjectImport{
			{Name: "Atomic Import", Slug: "atomic-import", Platform: "go"},
		},
		Issues: []migration.IssueImport{
			{
				ID:          "atomic-issue-1",
				ProjectSlug: "missing-project",
				Title:       "should fail",
			},
		},
	}

	if _, err := store.ImportOrganizationPayload(ctx, "test-org-id", "test-org", payload); err == nil {
		t.Fatal("expected import to fail")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'atomic-import'`).Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 0 {
		t.Fatalf("project count = %d, want 0 after rollback", count)
	}
}

func TestImportExportStoreExportOrganizationPayloadSkipsBlobMaterialization(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()
	attachments := NewAttachmentStore(db, blobs)

	seedImportExportOrganization(t, db)
	seedImportExportAuth(t, db)
	seedImportExportIssueAndEvent(t, db, "grp-export-1", "evt-export-1", "ExportError: boom", "1.2.3")
	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-export-1",
		EventID:     "evt-export-1",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("attachment payload")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	store := NewImportExportStore(db, attachments, nil, nil, &failingBlobStore{
		store:  blobs,
		getErr: errors.New("blob backend unavailable"),
	})

	payload, err := store.ExportOrganizationPayload(ctx, "test-org")
	if err != nil {
		t.Fatalf("ExportOrganizationPayload: %v", err)
	}
	if len(payload.Artifacts) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(payload.Artifacts))
	}
	if payload.Artifacts[0].BodyBase64 != "" {
		t.Fatalf("expected metadata-only export, got body for %+v", payload.Artifacts[0])
	}
}

func TestImportExportStoreImportFailsWhenProjectTeamIsMissing(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()

	seedImportExportOrganization(t, db)
	store := NewImportExportStore(db, NewAttachmentStore(db, blobs), nil, nil, blobs)
	payload := migration.ImportPayload{
		Projects: []migration.ProjectImport{
			{Name: "Team Scoped Project", Slug: "team-scoped", TeamSlug: "missing-team", Platform: "go"},
		},
	}

	if _, err := store.ImportOrganizationPayload(ctx, "test-org-id", "test-org", payload); err == nil || !strings.Contains(err.Error(), "missing-team") {
		t.Fatalf("ImportOrganizationPayload error = %v, want missing team failure", err)
	}
}

func TestImportExportStoreImportJoinsBlobCleanupFailureOnRollback(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()
	wrappedBlobs := &failingBlobStore{
		store:  blobs,
		delErr: errors.New("blob cleanup failed"),
	}

	seedImportExportOrganization(t, db)
	store := NewImportExportStore(db, NewAttachmentStore(db, wrappedBlobs), nil, nil, wrappedBlobs)
	payload := migration.ImportPayload{
		Projects: []migration.ProjectImport{
			{Name: "Cleanup Import", Slug: "cleanup-import", Platform: "go"},
		},
		Artifacts: []migration.ArtifactImport{
			{
				ID:          "att-cleanup-1",
				Kind:        "attachment",
				ProjectSlug: "cleanup-import",
				EventID:     "evt-cleanup-1",
				Name:        "crash.txt",
				ContentType: "text/plain",
				BodyBase64:  "Y3Jhc2ggbG9n",
			},
		},
		Members: []migration.MemberImport{
			{DisplayName: "Missing Email"},
		},
	}

	_, err := store.ImportOrganizationPayload(ctx, "test-org-id", "test-org", payload)
	if err == nil {
		t.Fatal("expected import to fail")
	}
	if !strings.Contains(err.Error(), "email is required") || !strings.Contains(err.Error(), "blob cleanup failed") {
		t.Fatalf("ImportOrganizationPayload error = %v, want import and cleanup failures", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'cleanup-import'`).Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 0 {
		t.Fatalf("project count = %d, want 0 after rollback", count)
	}
}

func TestImportExportStoreWriteOrganizationPayloadJSON(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteTestDB(t)
	blobs := memorystore.NewMemoryBlobStore()
	attachments := NewAttachmentStore(db, blobs)
	sourceMaps := NewSourceMapStore(db, blobs)
	proguardStore := NewProGuardStore(db, blobs)
	debugFiles := NewDebugFileStore(db, blobs)

	seedImportExportOrganization(t, db)
	seedImportExportAuth(t, db)
	seedImportExportRelease(t, db, "1.2.3")
	seedImportExportIssueAndEvent(t, db, "grp-stream-1", "evt-stream-1", "StreamError: boom", "1.2.3")

	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-stream-1",
		EventID:     "evt-stream-1",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("attachment payload")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if err := sourceMaps.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-stream-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		Name:      "app.min.js.map",
		CreatedAt: time.Now().UTC(),
	}, []byte(`{"version":3}`)); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if err := proguardStore.SaveMapping(ctx, &proguard.Mapping{
		ID:        "pg-stream-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		UUID:      "UUID-STREAM-1",
		Name:      "mapping.txt",
		CreatedAt: time.Now().UTC(),
	}, []byte("mapping payload")); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}
	if err := debugFiles.Save(ctx, &DebugFile{
		ID:        "dbg-stream-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		Kind:      "apple",
		Name:      "App.dSYM.zip",
		UUID:      "DEBUG-STREAM-1",
		CreatedAt: time.Now().UTC(),
	}, []byte("debug bundle")); err != nil {
		t.Fatalf("Save debug file: %v", err)
	}

	exportStore := NewImportExportStore(db, attachments, proguardStore, sourceMaps, blobs)
	var out bytes.Buffer
	if err := exportStore.WriteOrganizationPayloadJSON(ctx, "test-org", &out); err != nil {
		t.Fatalf("WriteOrganizationPayloadJSON: %v", err)
	}

	var payload migration.ImportPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if len(payload.Projects) != 1 || len(payload.Releases) != 1 || len(payload.Issues) != 1 || len(payload.Events) != 1 {
		t.Fatalf("unexpected export counts: %+v", payload)
	}
	if len(payload.ProjectKeys) != 1 || len(payload.Members) != 1 || len(payload.Artifacts) != 4 {
		t.Fatalf("unexpected export coverage: %+v", payload)
	}
	for _, artifact := range payload.Artifacts {
		if artifact.BodyBase64 == "" {
			t.Fatalf("artifact %s missing streamed body", artifact.Name)
		}
	}
}

func openSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedImportExportOrganization(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO organizations (id, slug, name) VALUES ('test-org-id', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj-id', 'test-org-id', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func seedImportExportAuth(t *testing.T, db *sql.DB) {
	t.Helper()
	authStore := NewAuthStore(db)
	if _, err := authStore.EnsureBootstrapAccess(context.Background(), BootstrapOptions{
		DefaultOrganizationID: "test-org-id",
		Email:                 "owner@example.com",
		DisplayName:           "Owner",
		Password:              "test-password-123",
		PersonalAccessToken:   "gpat_import_export_token",
	}); err != nil {
		t.Fatalf("EnsureBootstrapAccess: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO project_keys (id, project_id, public_key, status, label) VALUES ('key-test', 'test-proj-id', 'test-token-abc', 'active', 'Test Key')`); err != nil {
		t.Fatalf("seed project key: %v", err)
	}
}

func seedImportExportRelease(t *testing.T, db *sql.DB, version string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO releases (id, organization_id, version, created_at) VALUES (?, 'test-org-id', ?, ?)`, "rel-"+version, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed release: %v", err)
	}
}

func seedImportExportIssueAndEvent(t *testing.T, db *sql.DB, groupID, eventID, title, release string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO groups (id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen)
		 VALUES (?, 'test-proj-id', 'urgentry-v1', ?, ?, 'worker.go', 'error', 'unresolved', ?, ?, 1)`,
		groupID, groupID, title, now, now,
	); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, occurred_at, tags_json, payload_json, user_identifier)
		 VALUES
			(?, 'test-proj-id', ?, ?, ?, 'production', 'go', 'error', 'error', ?, 'test message', 'worker.go', ?, '{"environment":"production"}', '{"event_id":"`+eventID+`"}', 'user-1')`,
		eventID+"-internal", eventID, groupID, release, title, now,
	); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}
