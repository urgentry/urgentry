package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	memorystore "urgentry/internal/store"
)

func TestReplayStoreSaveAndIndex(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	blobs := memorystore.NewMemoryBlobStore()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	events := NewEventStore(db)
	ctx := context.Background()

	payload := []byte(`{
		"event_id":"evt-replay-1",
		"replay_id":"replay-1",
		"timestamp":"2026-03-29T12:00:00Z",
		"platform":"javascript",
		"release":"web@1.2.3",
		"environment":"production",
		"request":{"url":"https://app.example.com/checkout"},
		"user":{"email":"dev@example.com"},
		"contexts":{"trace":{"trace_id":"trace-123"}}
	}`)
	if replayEventID, err := replays.SaveEnvelopeReplay(ctx, "proj-1", "evt-replay-1", payload); err != nil {
		t.Fatalf("SaveEnvelopeReplay: %v", err)
	} else if replayEventID != "evt-replay-1" {
		t.Fatalf("replayEventID = %q, want evt-replay-1", replayEventID)
	}

	if err := events.SaveEvent(ctx, &memorystore.StoredEvent{
		ID:             "evt-row-linked-1",
		ProjectID:      "proj-1",
		EventID:        "evt-linked-1",
		GroupID:        "grp-linked-1",
		EventType:      "error",
		Platform:       "javascript",
		Level:          "error",
		OccurredAt:     time.Now().UTC(),
		IngestedAt:     time.Now().UTC(),
		NormalizedJSON: json.RawMessage(`{"event_id":"evt-linked-1"}`),
	}); err != nil {
		t.Fatalf("SaveEvent linked error: %v", err)
	}

	recording := []byte(`{
		"events":[
			{"type":"snapshot","offset_ms":0,"data":{"snapshot_id":"snap-1"}},
			{"type":"navigation","offset_ms":100,"data":{"url":"https://app.example.com/checkout?step=1","title":"Checkout"}},
			{"type":"console","offset_ms":200,"data":{"level":"error","message":"boom"}},
			{"type":"network","offset_ms":300,"data":{"method":"POST","url":"https://api.example.com/pay","status_code":500,"duration_ms":182}},
			{"type":"click","offset_ms":420,"data":{"selector":"button.pay","text":"Pay now"}},
			{"type":"error","offset_ms":430,"data":{"event_id":"evt-linked-1","trace_id":"trace-123","message":"Payment failed"}}
		]
	}`)
	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-replay-1",
		EventID:     "evt-replay-1",
		ProjectID:   "proj-1",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
	}, recording); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	if err := replays.IndexReplay(ctx, "proj-1", "replay-1"); err != nil {
		t.Fatalf("IndexReplay: %v", err)
	}

	record, err := replays.GetReplay(ctx, "proj-1", "replay-1")
	if err != nil {
		t.Fatalf("GetReplay: %v", err)
	}
	if record.Manifest.ProcessingStatus != memorystore.ReplayProcessingStatusReady {
		t.Fatalf("processing status = %q, want ready", record.Manifest.ProcessingStatus)
	}
	if record.Manifest.RequestURL != "https://app.example.com/checkout" {
		t.Fatalf("request_url = %q", record.Manifest.RequestURL)
	}
	if record.Manifest.UserRef.Email != "dev@example.com" {
		t.Fatalf("unexpected user ref: %+v", record.Manifest.UserRef)
	}
	if record.Manifest.AssetCount != 1 || len(record.Assets) != 1 || record.Assets[0].Kind != "recording" {
		t.Fatalf("unexpected replay assets: %+v / %+v", record.Manifest, record.Assets)
	}
	if record.Manifest.ConsoleCount != 1 || record.Manifest.NetworkCount != 1 || record.Manifest.ClickCount != 1 || record.Manifest.NavigationCount != 1 || record.Manifest.ErrorMarkerCount != 1 {
		t.Fatalf("unexpected manifest counts: %+v", record.Manifest)
	}
	if len(record.Timeline) != 6 {
		t.Fatalf("len(timeline) = %d, want 6", len(record.Timeline))
	}
	if record.Timeline[5].Kind != "error" || record.Timeline[5].LinkedEventID != "evt-linked-1" || record.Timeline[5].LinkedIssueID != "grp-linked-1" {
		t.Fatalf("unexpected error timeline item: %+v", record.Timeline[5])
	}
	if len(record.Manifest.TraceIDs) != 1 || record.Manifest.TraceIDs[0] != "trace-123" {
		t.Fatalf("unexpected trace ids: %+v", record.Manifest.TraceIDs)
	}
	filtered, err := replays.ListReplayTimeline(ctx, "proj-1", "replay-1", memorystore.ReplayTimelineFilter{Pane: "errors"})
	if err != nil {
		t.Fatalf("ListReplayTimeline(errors): %v", err)
	}
	if len(filtered) != 1 || filtered[0].LinkedIssueID != "grp-linked-1" {
		t.Fatalf("unexpected filtered timeline: %+v", filtered)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE project_id = 'proj-1' AND replay_id = 'replay-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_assets WHERE replay_id = 'replay-1'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-1'`, 6)
}

func TestReplayStorePartialAndMalformedReplayIndexing(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	blobs := memorystore.NewMemoryBlobStore()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	ctx := context.Background()

	partialPayload := []byte(`{"event_id":"evt-replay-partial","replay_id":"replay-partial","timestamp":"2026-03-29T12:00:00Z"}`)
	if _, err := replays.SaveEnvelopeReplay(ctx, "proj-1", "evt-replay-partial", partialPayload); err != nil {
		t.Fatalf("SaveEnvelopeReplay(partial): %v", err)
	}
	if err := replays.IndexReplay(ctx, "proj-1", "replay-partial"); err != nil {
		t.Fatalf("IndexReplay(partial): %v", err)
	}
	partial, err := replays.GetReplay(ctx, "proj-1", "replay-partial")
	if err != nil {
		t.Fatalf("GetReplay(partial): %v", err)
	}
	if partial.Manifest.ProcessingStatus != memorystore.ReplayProcessingStatusPartial {
		t.Fatalf("partial status = %q, want partial", partial.Manifest.ProcessingStatus)
	}
	if !strings.Contains(partial.Manifest.IngestError, "recording not uploaded") {
		t.Fatalf("partial ingest error = %q", partial.Manifest.IngestError)
	}

	badPayload := []byte(`{"event_id":"evt-replay-bad","replay_id":"replay-bad","timestamp":"2026-03-29T12:05:00Z"}`)
	if _, err := replays.SaveEnvelopeReplay(ctx, "proj-1", "evt-replay-bad", badPayload); err != nil {
		t.Fatalf("SaveEnvelopeReplay(bad): %v", err)
	}
	if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
		ID:          "att-replay-bad",
		EventID:     "evt-replay-bad",
		ProjectID:   "proj-1",
		Name:        "segment-1.rrweb",
		ContentType: "application/json",
	}, []byte(`{"events":`)); err != nil {
		t.Fatalf("SaveAttachment(bad): %v", err)
	}
	if err := replays.IndexReplay(ctx, "proj-1", "replay-bad"); err != nil {
		t.Fatalf("IndexReplay(bad): %v", err)
	}
	bad, err := replays.GetReplay(ctx, "proj-1", "replay-bad")
	if err != nil {
		t.Fatalf("GetReplay(bad): %v", err)
	}
	if bad.Manifest.ProcessingStatus != memorystore.ReplayProcessingStatusFailed {
		t.Fatalf("bad status = %q, want failed", bad.Manifest.ProcessingStatus)
	}
	if bad.Manifest.AssetCount != 1 || len(bad.Timeline) != 0 {
		t.Fatalf("unexpected bad replay record: %+v", bad)
	}
	if !strings.Contains(bad.Manifest.IngestError, "unsupported replay recording payload") && !strings.Contains(bad.Manifest.IngestError, "unexpected end of JSON input") {
		t.Fatalf("bad ingest error = %q", bad.Manifest.IngestError)
	}
}

func TestReplayStoreIdempotentRetryIndexing(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	blobs := memorystore.NewMemoryBlobStore()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	ctx := context.Background()

	payload := []byte(`{"event_id":"evt-replay-retry","replay_id":"replay-retry","timestamp":"2026-03-29T12:10:00Z"}`)
	recording := []byte(`{"events":[{"type":"navigation","offset_ms":10,"data":{"url":"https://app.example.com"}}]}`)
	for i := 0; i < 2; i++ {
		if _, err := replays.SaveEnvelopeReplay(ctx, "proj-1", "evt-replay-retry", payload); err != nil {
			t.Fatalf("SaveEnvelopeReplay retry %d: %v", i, err)
		}
		if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
			ID:          "att-replay-retry",
			EventID:     "evt-replay-retry",
			ProjectID:   "proj-1",
			Name:        "segment-1.rrweb",
			ContentType: "application/json",
		}, recording); err != nil {
			t.Fatalf("SaveAttachment retry %d: %v", i, err)
		}
		if err := replays.IndexReplay(ctx, "proj-1", "replay-retry"); err != nil {
			t.Fatalf("IndexReplay retry %d: %v", i, err)
		}
	}

	record, err := replays.GetReplay(ctx, "proj-1", "replay-retry")
	if err != nil {
		t.Fatalf("GetReplay(retry): %v", err)
	}
	if len(record.Assets) != 1 || len(record.Timeline) != 1 {
		t.Fatalf("unexpected replay after retries: %+v", record)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM replay_manifests WHERE project_id = 'proj-1' AND replay_id = 'replay-retry'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_assets WHERE replay_id = 'replay-retry'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM replay_timeline_items WHERE replay_id = 'replay-retry'`, 1)
}
