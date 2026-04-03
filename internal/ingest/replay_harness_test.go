package ingest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	replayfixtures "urgentry/internal/testfixtures/replays"
)

func TestReplayFixtureCorpusManifestFidelity(t *testing.T) {
	for _, fixture := range replayfixtures.Corpus() {
		t.Run(fixture.Name, func(t *testing.T) {
			db, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			seedAttachmentProjectForTest(t, db, "1")
			seedReplayHarnessLinkedIssue(t, db, "1")
			applyReplayFixturePolicy(t, db, "1", fixture.Policy())

			blobs := store.NewMemoryBlobStore()
			replays := sqlite.NewReplayStore(db, blobs)
			handler := EnvelopeHandlerWithDeps(IngestDeps{
				EventStore:      sqlite.NewEventStore(db),
				ReplayStore:     replays,
				ReplayPolicies:  sqlite.NewReplayConfigStore(db),
				AttachmentStore: sqlite.NewAttachmentStore(db, blobs),
				BlobStore:       blobs,
				OutcomeStore:    sqlite.NewOutcomeStore(db),
			})

			req := httptestNewReplayFixtureRequest(t, fixture)
			rec := newRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}

			record, err := replays.GetReplay(t.Context(), "1", fixture.Spec().ReplayID)
			if err != nil {
				t.Fatalf("GetReplay: %v", err)
			}
			if record.Manifest.ConsoleCount != fixture.Expected.ConsoleCount ||
				record.Manifest.NetworkCount != fixture.Expected.NetworkCount ||
				record.Manifest.ClickCount != fixture.Expected.ClickCount ||
				record.Manifest.NavigationCount != fixture.Expected.NavigationCount ||
				record.Manifest.ErrorMarkerCount != fixture.Expected.ErrorCount {
				t.Fatalf("unexpected replay counts: %+v", record.Manifest)
			}
			for _, title := range fixture.Expected.Titles {
				if !replayTimelineHasTitle(record.Timeline, title) {
					t.Fatalf("timeline missing title %q: %+v", title, record.Timeline)
				}
			}
			if fixture.Policy().SampleRate != 0 || fixture.Policy().MaxBytes != 0 || len(fixture.Policy().ScrubFields) != 0 || len(fixture.Policy().ScrubSelectors) != 0 {
				if record.Manifest.PrivacyPolicyVersion == "" {
					t.Fatalf("expected privacy policy version on manifest: %+v", record.Manifest)
				}
			}
			payloadText := record.Manifest.RequestURL + replayTimelineText(record.Timeline)
			for _, forbidden := range fixture.Expected.PageExcludes {
				if strings.Contains(payloadText, forbidden) {
					t.Fatalf("replay corpus leaked %q into canonical replay text: %s", forbidden, payloadText)
				}
			}
		})
	}
}

func applyReplayFixturePolicy(t *testing.T, db *sql.DB, projectID string, policy store.ReplayIngestPolicy) {
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
		t.Fatalf("insert replay policy: %v", err)
	}
}

func seedReplayHarnessLinkedIssue(t *testing.T, db *sql.DB, projectID string) {
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
		t.Fatalf("insert replay linked group: %v", err)
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
		t.Fatalf("insert replay linked event: %v", err)
	}
}

func httptestNewReplayFixtureRequest(t *testing.T, fixture replayfixtures.Fixture) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewReader(fixture.EnvelopeBody()))
}

func newRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

func replayTimelineHasTitle(items []store.ReplayTimelineItem, title string) bool {
	for _, item := range items {
		if item.Title == title {
			return true
		}
	}
	return false
}

func replayTimelineText(items []store.ReplayTimelineItem) string {
	var parts []string
	for _, item := range items {
		parts = append(parts, item.Title, item.Message, item.URL, item.Selector, item.Text)
	}
	return strings.Join(parts, " ")
}
