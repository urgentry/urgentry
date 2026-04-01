package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"urgentry/internal/alert"
	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/migration"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func TestOrganizationImportExportRoundTrip(t *testing.T) {
	sourceDB := openTestSQLite(t)
	sourceBlobs := newTestBlobStore(t)
	sourceAttachments := sqlite.NewAttachmentStore(sourceDB, sourceBlobs)
	sourceSourceMaps := sqlite.NewSourceMapStore(sourceDB, sourceBlobs)
	sourceProGuard := sqlite.NewProGuardStore(sourceDB, sourceBlobs)
	sourceDebugFiles := sqlite.NewDebugFileStore(sourceDB, sourceBlobs)
	sourceReplays := sqlite.NewReplayStore(sourceDB, sourceBlobs)

	seedSQLiteAuth(t, sourceDB)
	insertSQLiteRelease(t, sourceDB, "rel-123", "1.2.3")
	insertSQLiteGroup(t, sourceDB, "grp-import-1", "ImportError: boom", "worker.go", "error", "unresolved")
	insertSQLiteEventWithRelease(t, sourceDB, "evt-import-1", "grp-import-1", "ImportError: boom", "error", "1.2.3")
	insertSQLiteAlertRule(t, sourceDB, "alert-import-1", "test-proj-id")

	if err := sourceAttachments.SaveAttachment(context.Background(), &attachmentstore.Attachment{
		ID:          "att-import-1",
		EventID:     "evt-import-1",
		ProjectID:   "test-proj-id",
		Name:        "crash.txt",
		ContentType: "text/plain",
		CreatedAt:   time.Now().UTC(),
	}, []byte("attachment payload")); err != nil {
		t.Fatalf("save source attachment: %v", err)
	}
	if err := sourceSourceMaps.SaveArtifact(context.Background(), &sourcemap.Artifact{
		ID:        "sm-import-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		Name:      "app.min.js.map",
		Checksum:  "sha1-source-map",
		CreatedAt: time.Now().UTC(),
	}, []byte(`{"version":3,"file":"app.min.js"}`)); err != nil {
		t.Fatalf("save source map: %v", err)
	}
	if err := sourceProGuard.SaveMapping(context.Background(), &proguard.Mapping{
		ID:        "pg-import-1",
		ProjectID: "test-proj-id",
		ReleaseID: "1.2.3",
		UUID:      "UUID-IMPORT-1",
		Name:      "mapping.txt",
		Checksum:  "sha1-proguard",
		CreatedAt: time.Now().UTC(),
	}, []byte("com.example.Foo -> a:")); err != nil {
		t.Fatalf("save proguard map: %v", err)
	}
	if err := sourceDebugFiles.Save(context.Background(), &sqlite.DebugFile{
		ID:          "dbg-import-1",
		ProjectID:   "test-proj-id",
		ReleaseID:   "1.2.3",
		Kind:        "apple",
		Name:        "App.dSYM.zip",
		UUID:        "DEBUG-IMPORT-1",
		ContentType: "application/zip",
		CreatedAt:   time.Now().UTC(),
	}, []byte("debug bundle")); err != nil {
		t.Fatalf("save debug file: %v", err)
	}
	replayPayload := []byte(`{
		"event_id":"evt-import-replay-1",
		"replay_id":"replay-import-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"}
	}`)
	if _, err := sourceReplays.SaveEnvelopeReplay(context.Background(), "test-proj-id", "evt-import-replay-1", replayPayload); err != nil {
		t.Fatalf("save replay receipt: %v", err)
	}
	if err := sourceAttachments.SaveAttachment(context.Background(), &attachmentstore.Attachment{
		ID:          "att-import-replay-1",
		EventID:     "evt-import-replay-1",
		ProjectID:   "test-proj-id",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
		CreatedAt:   time.Now().UTC(),
	}, []byte(`{"events":[{"type":"navigation","offset_ms":5,"data":{"url":"https://app.example.com/checkout"}},{"type":"click","offset_ms":25,"data":{"selector":"button.pay","text":"Pay"}}]}`)); err != nil {
		t.Fatalf("save replay attachment: %v", err)
	}

	sourceServer, sourcePAT := newSQLiteAuthorizedServerWithBootstrap(t, sourceDB, Dependencies{
		Attachments:    sourceAttachments,
		ProGuardStore:  sourceProGuard,
		SourceMapStore: sourceSourceMaps,
		BlobStore:      sourceBlobs,
	}, "source-owner@example.com", "Source Owner", "gpat_source_admin_token")
	defer sourceServer.Close()

	exportResp := authzJSONRequest(t, sourceServer, http.MethodGet, "/api/0/organizations/test-org/export/", sourcePAT, nil)
	if exportResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(exportResp.Body)
		exportResp.Body.Close()
		t.Fatalf("export status = %d, want 200, body=%s", exportResp.StatusCode, body)
	}

	var payload migration.ImportPayload
	decodeBody(t, exportResp, &payload)
	if len(payload.Projects) != 1 || len(payload.Releases) != 1 || len(payload.Issues) != 1 || len(payload.Events) != 2 {
		t.Fatalf("unexpected export counts: %+v", payload)
	}
	if len(payload.ProjectKeys) != 1 || len(payload.AlertRules) != 1 || len(payload.Members) != 1 || len(payload.Artifacts) != 5 {
		t.Fatalf("unexpected export coverage: %+v", payload)
	}
	for _, artifact := range payload.Artifacts {
		if artifact.BodyBase64 == "" || artifact.Checksum == "" {
			t.Fatalf("artifact %s missing integrity data", artifact.Name)
		}
	}

	targetDB := openTestSQLite(t)
	targetBlobs := newTestBlobStore(t)
	targetAttachments := sqlite.NewAttachmentStore(targetDB, targetBlobs)
	targetSourceMaps := sqlite.NewSourceMapStore(targetDB, targetBlobs)
	targetProGuard := sqlite.NewProGuardStore(targetDB, targetBlobs)
	targetDebugFiles := sqlite.NewDebugFileStore(targetDB, targetBlobs)

	targetServer, targetPAT := newSQLiteAuthorizedServerWithBootstrap(t, targetDB, Dependencies{
		Attachments:    targetAttachments,
		ProGuardStore:  targetProGuard,
		SourceMapStore: targetSourceMaps,
		BlobStore:      targetBlobs,
	}, "importer@example.com", "Importer", "gpat_target_admin_token")
	defer targetServer.Close()

	importResp := authzJSONRequest(t, targetServer, http.MethodPost, "/api/0/organizations/test-org/import/", targetPAT, payload)
	if importResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(importResp.Body)
		importResp.Body.Close()
		t.Fatalf("import status = %d, want 200, body=%s", importResp.StatusCode, body)
	}

	var result migration.ImportResult
	decodeBody(t, importResp, &result)
	if result.ProjectsImported != 1 || result.ReleasesImported != 1 || result.IssuesImported != 1 || result.EventsImported != 2 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if result.ProjectKeysImported != 1 || result.AlertRulesImported != 1 || result.MembersImported != 1 || result.ArtifactsImported != 5 || result.ArtifactsVerified != 5 {
		t.Fatalf("unexpected import coverage: %+v", result)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected import errors: %+v", result.Errors)
	}

	listAttachments := authzJSONRequest(t, targetServer, http.MethodGet, "/api/0/events/evt-import-1/attachments/", targetPAT, nil)
	if listAttachments.StatusCode != http.StatusOK {
		t.Fatalf("list attachments status = %d, want 200", listAttachments.StatusCode)
	}
	var attachments []Attachment
	decodeBody(t, listAttachments, &attachments)
	if len(attachments) != 1 || attachments[0].Name != "crash.txt" {
		t.Fatalf("unexpected imported attachments: %+v", attachments)
	}

	download := authzJSONRequest(t, targetServer, http.MethodGet, "/api/0/events/evt-import-1/attachments/"+attachments[0].ID+"/", targetPAT, nil)
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download attachment status = %d, want 200", download.StatusCode)
	}
	downloadBody, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatalf("read attachment download: %v", err)
	}
	_ = download.Body.Close()
	if string(downloadBody) != "attachment payload" {
		t.Fatalf("attachment body = %q, want %q", string(downloadBody), "attachment payload")
	}

	sourceMap, sourceMapBody, err := targetSourceMaps.LookupByName(context.Background(), "test-proj-id", "1.2.3", "app.min.js.map")
	if err != nil {
		t.Fatalf("lookup imported source map: %v", err)
	}
	if sourceMap == nil || string(sourceMapBody) != `{"version":3,"file":"app.min.js"}` {
		t.Fatalf("unexpected imported source map: %+v body=%q", sourceMap, string(sourceMapBody))
	}

	mapping, mappingBody, err := targetProGuard.LookupByUUID(context.Background(), "test-proj-id", "1.2.3", "UUID-IMPORT-1")
	if err != nil {
		t.Fatalf("lookup imported proguard: %v", err)
	}
	if mapping == nil || string(mappingBody) != "com.example.Foo -> a:" {
		t.Fatalf("unexpected imported proguard: %+v body=%q", mapping, string(mappingBody))
	}

	debugFile, debugBody, err := targetDebugFiles.LookupByDebugID(context.Background(), "test-proj-id", "1.2.3", "apple", "DEBUG-IMPORT-1")
	if err != nil {
		t.Fatalf("lookup imported debug file: %v", err)
	}
	if debugFile == nil || debugFile.Kind != "apple" || string(debugBody) != "debug bundle" {
		t.Fatalf("unexpected imported debug file: %+v body=%q", debugFile, string(debugBody))
	}

	targetReplays := sqlite.NewReplayStore(targetDB, targetBlobs)
	if err := targetReplays.IndexReplay(context.Background(), "test-proj-id", "replay-import-1"); err != nil {
		t.Fatalf("IndexReplay imported: %v", err)
	}
	replay, err := targetReplays.GetReplay(context.Background(), "test-proj-id", "replay-import-1")
	if err != nil {
		t.Fatalf("GetReplay imported: %v", err)
	}
	if replay.Manifest.AssetCount != 1 || len(replay.Timeline) == 0 {
		t.Fatalf("unexpected imported replay: %+v", replay.Manifest)
	}
}

func TestOrganizationImportIsAtomic(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

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

	resp := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/import/", pat, payload)
	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("import status = %d, want 500, body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'atomic-import'`).Scan(&count); err != nil {
		t.Fatalf("count imported projects: %v", err)
	}
	if count != 0 {
		t.Fatalf("import should have rolled back, found %d atomic-import projects", count)
	}
}

func TestOrganizationImportRejectsUnknownFields(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/0/organizations/test-org/import/",
		bytes.NewBufferString(`{"projects":[{"name":"Strict Import","slug":"strict-import"}],"unexpected":true}`),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("import status = %d, want 400, body=%s", resp.StatusCode, body)
	}
}

func TestOrganizationImportDryRunValidatesArtifacts(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{BlobStore: newTestBlobStore(t)})
	defer ts.Close()

	payload := migration.ImportPayload{
		Projects: []migration.ProjectImport{
			{Name: "Dry Run Import", Slug: "dry-run-import", Platform: "go"},
		},
		Artifacts: []migration.ArtifactImport{
			{
				Kind:        "attachment",
				ProjectSlug: "dry-run-import",
				EventID:     "evt-dry-run-1",
				Name:        "payload.txt",
				ContentType: "text/plain",
				BodyBase64:  "aGVsbG8=",
				Checksum:    "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d",
				Size:        5,
			},
		},
	}

	resp := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/import/?dry_run=1", pat, payload)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("dry-run import status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	var result migration.ImportResult
	decodeBody(t, resp, &result)
	if !result.DryRun || result.ProjectsImported != 1 || result.ArtifactsImported != 1 || result.ArtifactsVerified != 1 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'dry-run-import'`).Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 0 {
		t.Fatalf("dry run should not persist projects, found %d", count)
	}
}

func TestOrganizationImportRejectsArtifactChecksumMismatch(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{BlobStore: newTestBlobStore(t)})
	defer ts.Close()

	payload := migration.ImportPayload{
		Projects: []migration.ProjectImport{
			{Name: "Checksum Import", Slug: "checksum-import", Platform: "go"},
		},
		Artifacts: []migration.ArtifactImport{
			{
				Kind:        "attachment",
				ProjectSlug: "checksum-import",
				EventID:     "evt-bad-checksum-1",
				Name:        "payload.txt",
				ContentType: "text/plain",
				BodyBase64:  "aGVsbG8=",
				Checksum:    "deadbeef",
				Size:        5,
			},
		},
	}

	resp := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/import/?dry_run=1", pat, payload)
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("checksum import status = %d, want 400, body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

func TestOrganizationImportRejectsOversizePayload(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/0/organizations/test-org/import/",
		io.MultiReader(
			bytes.NewBufferString(`{"artifacts":[{"kind":"attachment","projectSlug":"test-project","name":"too-big.txt","bodyBase64":"`),
			&repeatByteReader{remaining: int64(maxOrganizationImportBodySize) + 1, b: 'a'},
			bytes.NewBufferString(`"}]}`),
		),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("import status = %d, want 413, body=%s", resp.StatusCode, body)
	}
}

func newTestBlobStore(t *testing.T) store.BlobStore {
	t.Helper()
	blobs, err := store.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("new blob store: %v", err)
	}
	return blobs
}

func insertSQLiteRelease(t *testing.T, db *sql.DB, id, version string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO releases (id, organization_id, version, date_released, created_at)
		 VALUES (?, 'test-org-id', ?, ?, ?)`,
		id, version, now, now,
	); err != nil {
		t.Fatalf("insert release %s: %v", version, err)
	}
}

func insertSQLiteEventWithRelease(t *testing.T, db *sql.DB, eventID, groupID, title, level, release string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	tagsJSON, err := json.Marshal(map[string]string{"environment": "production", "release": release})
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	payloadJSON, err := json.Marshal(map[string]any{
		"event_id": eventID,
		"release":  release,
		"message":  title,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, project_id, event_id, group_id, release, level, title, message, platform, culprit, occurred_at, tags_json, payload_json)
		 VALUES (?, 'test-proj-id', ?, ?, ?, ?, ?, 'test message', 'go', 'main.go', ?, ?, ?)`,
		eventID+"-internal", eventID, groupID, release, level, title, now, string(tagsJSON), string(payloadJSON),
	); err != nil {
		t.Fatalf("insert event %s: %v", eventID, err)
	}
}

func insertSQLiteAlertRule(t *testing.T, db *sql.DB, id, projectID string) {
	t.Helper()
	rule := alert.Rule{
		ID:        id,
		ProjectID: projectID,
		Name:      "New issue email",
		RuleType:  "all",
		Status:    "active",
		Conditions: []alert.Condition{
			{ID: "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition", Name: "first_seen"},
		},
		Actions: []alert.Action{
			{ID: "email", Type: "email", Target: "team@example.com"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	configJSON, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal alert rule: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO alert_rules (id, project_id, name, status, config_json, created_at)
		 VALUES (?, ?, ?, 'active', ?, ?)`,
		rule.ID, projectID, rule.Name, string(configJSON), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert alert rule: %v", err)
	}
}

type repeatByteReader struct {
	remaining int64
	b         byte
}

func (r *repeatByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	for i := range p {
		p[i] = r.b
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}
