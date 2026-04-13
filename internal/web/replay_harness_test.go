package web

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"urgentry/internal/ingest"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	replayfixtures "urgentry/internal/testfixtures/replays"
)

func TestReplayPlayerFixtureCorpus(t *testing.T) {
	for _, fixture := range replayfixtures.Corpus() {
		t.Run(fixture.Name, func(t *testing.T) {
			srv, db, blobs := setupReplayHarnessServer(t)
			defer srv.Close()
			seedReplayHarnessPageLinks(t, db, "test-proj")
			ingestReplayFixture(t, db, blobs, "test-proj", fixture)

			record, err := sqlite.NewReplayStore(db, blobs).GetReplay(t.Context(), "test-proj", fixture.Spec().ReplayID)
			if err != nil {
				t.Fatalf("GetReplay: %v", err)
			}
			anchor := replayHarnessAnchor(t, record.Timeline, fixture.Expected.AnchorKind)
			resp, err := http.Get(srv.URL + "/replays/" + fixture.Spec().ReplayID + "/?pane=" + url.QueryEscape(anchor.Pane) + "&anchor=" + url.QueryEscape(anchor.ID) + "&ts=" + strconv.FormatInt(anchor.TSMS, 10))
			if err != nil {
				t.Fatalf("GET replay detail: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("replay detail status = %d, want 200", resp.StatusCode)
			}
			body := getBody(t, resp)
			for _, want := range fixture.Expected.PageContains {
				if !strings.Contains(body, want) {
					t.Fatalf("replay page missing %q: %s", want, body)
				}
			}
			for _, forbidden := range fixture.Expected.PageExcludes {
				if strings.Contains(body, forbidden) {
					t.Fatalf("replay page leaked %q: %s", forbidden, body)
				}
			}
		})
	}
}

func setupReplayHarnessServer(t *testing.T) (*httptest.Server, *sql.DB, store.BlobStore) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('test-org', 'test-org', 'Test Org')`); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('test-proj', 'test-org', 'test-project', 'Test Project', 'go', 'active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	blobs := store.NewMemoryBlobStore()
	handler := NewHandlerWithDeps(testHandlerDeps(db, blobs, dataDir, nil))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return httptest.NewServer(mux), db, blobs
}

func ingestReplayFixture(t *testing.T, db *sql.DB, blobs store.BlobStore, projectID string, fixture replayfixtures.Fixture) {
	t.Helper()
	applyReplayHarnessPolicy(t, db, projectID, fixture.Policy())
	handler := ingest.EnvelopeHandlerWithDeps(ingest.IngestDeps{
		EventStore:      sqlite.NewEventStore(db),
		ReplayStore:     sqlite.NewReplayStore(db, blobs),
		ReplayPolicies:  sqlite.NewReplayConfigStore(db),
		AttachmentStore: sqlite.NewAttachmentStore(db, blobs),
		BlobStore:       blobs,
		OutcomeStore:    sqlite.NewOutcomeStore(db),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/"+projectID+"/envelope/", bytes.NewReader(fixture.EnvelopeBody()))
	req.SetPathValue("project_id", projectID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest replay fixture status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

func applyReplayHarnessPolicy(t *testing.T, db *sql.DB, projectID string, policy store.ReplayIngestPolicy) {
	t.Helper()
	if policy.SampleRate == 0 && policy.MaxBytes == 0 && len(policy.ScrubFields) == 0 && len(policy.ScrubSelectors) == 0 {
		return
	}
	fieldsJSON, _ := json.Marshal(policy.ScrubFields)
	selectorsJSON, _ := json.Marshal(policy.ScrubSelectors)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO project_replay_configs
			(project_id, sample_rate, max_bytes, scrub_fields_json, scrub_selectors_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		policy.SampleRate,
		policy.MaxBytes,
		string(fieldsJSON),
		string(selectorsJSON),
		now,
		now,
	); err != nil {
		t.Fatalf("insert replay harness policy: %v", err)
	}
}

func seedReplayHarnessPageLinks(t *testing.T, db *sql.DB, projectID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO groups
			(id, project_id, grouping_version, grouping_key, title, culprit, level, status, first_seen, last_seen, times_seen)
		 VALUES
			('grp-replay-linked-1', ?, 'urgentry-v1', 'grp-replay-linked-1', 'CheckoutError', 'checkout.go', 'error', 'unresolved', ?, ?, 1)`,
		projectID,
		now,
		now,
	); err != nil {
		t.Fatalf("seed replay link group: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events
			(id, project_id, event_id, group_id, title, culprit, event_type, level, ingested_at, occurred_at, payload_json)
		 VALUES
			('evt-replay-linked-row-1', ?, 'evt-replay-linked-1', 'grp-replay-linked-1', 'CheckoutError', 'checkout.go', 'error', 'error', ?, ?, '{}')`,
		projectID,
		now,
		now,
	); err != nil {
		t.Fatalf("seed replay link event: %v", err)
	}
}

func replayHarnessAnchor(t *testing.T, items []store.ReplayTimelineItem, kind string) store.ReplayTimelineItem {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind {
			return item
		}
	}
	if len(items) == 0 {
		t.Fatal("expected replay timeline item")
	}
	return items[len(items)-1]
}
