package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	attachmentstore "urgentry/internal/attachment"
	memorystore "urgentry/internal/store"
	replayfixtures "urgentry/internal/testfixtures/replays"
)

func BenchmarkReplayStoreIngestAndIndex(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedReplayTestProjectFromBench(b, db, "bench-org", "bench-proj")

	blobs := memorystore.NewMemoryBlobStore()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	ctx := context.Background()
	base := replayfixtures.CoreJourney()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spec := base.Spec().
			WithIDs(fmt.Sprintf("%032x", i+1), fmt.Sprintf("%032x", i+1)).
			WithTimestamp(time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second))
		replayID, err := replays.SaveEnvelopeReplay(ctx, "bench-proj", spec.EventID, spec.Payload())
		if err != nil {
			b.Fatalf("SaveEnvelopeReplay %d: %v", i, err)
		}
		if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
			ID:          fmt.Sprintf("att-bench-%04d", i),
			ProjectID:   "bench-proj",
			EventID:     replayID,
			Name:        "segment-1.rrweb",
			ContentType: "application/json",
			CreatedAt:   spec.Timestamp,
		}, base.RecordingPayload()); err != nil {
			b.Fatalf("SaveAttachment %d: %v", i, err)
		}
		if err := replays.IndexReplay(ctx, "bench-proj", replayID); err != nil {
			b.Fatalf("IndexReplay %d: %v", i, err)
		}
	}
}

func BenchmarkReplayStoreGetReplay(b *testing.B) {
	fx := newBenchmarkReplayStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		record, err := fx.store.GetReplay(fx.ctx, fx.projectID, fx.replayID)
		if err != nil {
			b.Fatalf("GetReplay: %v", err)
		}
		if record.Manifest.ReplayID == "" || len(record.Timeline) == 0 {
			b.Fatalf("GetReplay returned incomplete record: %+v", record.Manifest)
		}
	}
}

func BenchmarkReplayStoreListReplayTimeline(b *testing.B) {
	fx := newBenchmarkReplayStore(b)
	filter := memorystore.ReplayTimelineFilter{Limit: 100}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := fx.store.ListReplayTimeline(fx.ctx, fx.projectID, fx.replayID, filter)
		if err != nil {
			b.Fatalf("ListReplayTimeline: %v", err)
		}
		if len(items) == 0 {
			b.Fatal("ListReplayTimeline returned no items")
		}
	}
}

func BenchmarkReplayStoreListReplayTimelineErrorsPane(b *testing.B) {
	fx := newBenchmarkReplayStore(b)
	filter := memorystore.ReplayTimelineFilter{Pane: "errors", Limit: 25}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := fx.store.ListReplayTimeline(fx.ctx, fx.projectID, fx.replayID, filter)
		if err != nil {
			b.Fatalf("ListReplayTimeline errors pane: %v", err)
		}
		if len(items) == 0 || items[0].Kind != "error" {
			b.Fatalf("unexpected errors pane items: %+v", items)
		}
	}
}

func BenchmarkReplayStoreReindexExisting(b *testing.B) {
	fx := newBenchmarkReplayStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fx.store.IndexReplay(fx.ctx, fx.projectID, fx.replayID); err != nil {
			b.Fatalf("IndexReplay existing: %v", err)
		}
	}
}

type benchmarkReplayFixture struct {
	ctx       context.Context
	projectID string
	store     *ReplayStore
	replayID  string
}

func newBenchmarkReplayStore(b *testing.B) benchmarkReplayFixture {
	b.Helper()

	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedReplayTestProjectFromBench(b, db, "bench-org", "bench-proj")

	blobs := memorystore.NewMemoryBlobStore()
	replays := NewReplayStore(db, blobs)
	attachments := NewAttachmentStore(db, blobs)
	ctx := context.Background()
	base := replayfixtures.CoreJourney()

	var replayID string
	for i := 0; i < 24; i++ {
		spec := base.Spec().
			WithIDs(fmt.Sprintf("%032x", i+1), fmt.Sprintf("%032x", i+1)).
			WithTimestamp(time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute))
		replayID, err = replays.SaveEnvelopeReplay(ctx, "bench-proj", spec.EventID, spec.Payload())
		if err != nil {
			b.Fatalf("SaveEnvelopeReplay %d: %v", i, err)
		}
		if err := attachments.SaveAttachment(ctx, &attachmentstore.Attachment{
			ID:          fmt.Sprintf("att-bench-seed-%02d", i),
			ProjectID:   "bench-proj",
			EventID:     replayID,
			Name:        "segment-1.rrweb",
			ContentType: "application/json",
			CreatedAt:   spec.Timestamp,
		}, base.RecordingPayload()); err != nil {
			b.Fatalf("SaveAttachment %d: %v", i, err)
		}
		if err := replays.IndexReplay(ctx, "bench-proj", replayID); err != nil {
			b.Fatalf("IndexReplay %d: %v", i, err)
		}
	}

	return benchmarkReplayFixture{
		ctx:       ctx,
		projectID: "bench-proj",
		store:     replays,
		replayID:  replayID,
	}
}

func seedReplayTestProjectFromBench(b *testing.B, db *sql.DB, orgID, projectID string) {
	b.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES (?, 'bench-org', 'Benchmark Org')`, orgID); err != nil {
		b.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES (?, ?, 'bench-project', 'Benchmark Project')`, projectID, orgID); err != nil {
		b.Fatalf("insert project: %v", err)
	}
}
