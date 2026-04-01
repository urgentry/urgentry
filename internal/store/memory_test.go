package store

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestMemoryBlobStore_PutGetDelete(t *testing.T) {
	ctx := context.Background()
	bs := NewMemoryBlobStore()

	data := []byte(`{"hello":"world"}`)

	// Put
	if err := bs.Put(ctx, "key1", data); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get
	got, err := bs.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Get = %q, want %q", got, data)
	}

	// Get returns a copy (mutation safety)
	got[0] = 'X'
	got2, _ := bs.Get(ctx, "key1")
	if got2[0] == 'X' {
		t.Fatal("Get did not return a copy")
	}

	// Delete
	if err := bs.Delete(ctx, "key1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Get after delete
	_, err = bs.Get(ctx, "key1")
	if err != ErrNotFound {
		t.Fatalf("Get after Delete: got err %v, want ErrNotFound", err)
	}
}

func TestMemoryBlobStore_NotFound(t *testing.T) {
	ctx := context.Background()
	bs := NewMemoryBlobStore()

	_, err := bs.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Get nonexistent: got %v, want ErrNotFound", err)
	}

	err = bs.Delete(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Delete nonexistent: got %v, want ErrNotFound", err)
	}
}

func TestMemoryEventStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	now := time.Now().UTC()
	evt := &StoredEvent{
		ID:             "uuid-1",
		ProjectID:      "proj-1",
		EventID:        "evt-aaa",
		GroupID:        "grp-1",
		Environment:    "production",
		Platform:       "python",
		Level:          "error",
		EventType:      "error",
		OccurredAt:     now,
		IngestedAt:     now,
		Message:        "something broke",
		Title:          "ValueError: bad input",
		Culprit:        "app.views in handle_request",
		Fingerprint:    []string{"{{ default }}"},
		Tags:           map[string]string{"server": "web-01"},
		NormalizedJSON: json.RawMessage(`{}`),
		PayloadKey:     "raw/proj-1/evt-aaa.json.gz",
	}

	if err := es.SaveEvent(ctx, evt); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	got, err := es.GetEvent(ctx, "proj-1", "evt-aaa")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.Title != evt.Title {
		t.Fatalf("Title = %q, want %q", got.Title, evt.Title)
	}
	if got.Message != evt.Message {
		t.Fatalf("Message = %q, want %q", got.Message, evt.Message)
	}
}

func TestMemoryEventStore_DuplicateReject(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	evt := &StoredEvent{ProjectID: "p1", EventID: "e1"}
	if err := es.SaveEvent(ctx, evt); err != nil {
		t.Fatal(err)
	}
	if err := es.SaveEvent(ctx, evt); err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
}

func TestMemoryEventStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	_, err := es.GetEvent(ctx, "proj-1", "nope")
	if err != ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestMemoryEventStore_ListEvents(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		evt := &StoredEvent{
			ID:         "id-" + string(rune('a'+i)),
			ProjectID:  "proj-1",
			EventID:    "evt-" + string(rune('a'+i)),
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
		}
		if err := es.SaveEvent(ctx, evt); err != nil {
			t.Fatal(err)
		}
	}

	// Also add an event for a different project
	if err := es.SaveEvent(ctx, &StoredEvent{ProjectID: "proj-2", EventID: "evt-other"}); err != nil {
		t.Fatal(err)
	}

	// Default sort (desc) - all events for proj-1
	events, err := es.ListEvents(ctx, "proj-1", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("len = %d, want 5", len(events))
	}
	// First should be the latest
	if events[0].EventID != "evt-e" {
		t.Fatalf("first event = %s, want evt-e", events[0].EventID)
	}

	// Ascending sort
	events, err = es.ListEvents(ctx, "proj-1", ListOpts{Sort: "occurred_at_asc"})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].EventID != "evt-a" {
		t.Fatalf("first event asc = %s, want evt-a", events[0].EventID)
	}

	// Limit
	events, err = es.ListEvents(ctx, "proj-1", ListOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2", len(events))
	}

	// Cursor: skip past evt-d (which is index 1 in desc order)
	events, err = es.ListEvents(ctx, "proj-1", ListOpts{Cursor: "evt-d"})
	if err != nil {
		t.Fatal(err)
	}
	// In desc order: [e, d, c, b, a] -> cursor=d -> skip past d -> [c, b, a]
	if len(events) != 3 {
		t.Fatalf("len after cursor = %d, want 3", len(events))
	}
	if events[0].EventID != "evt-c" {
		t.Fatalf("first after cursor = %s, want evt-c", events[0].EventID)
	}
}

func TestMemoryEventStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			evt := &StoredEvent{
				ProjectID: "proj-1",
				EventID:   "evt-" + time.Now().Format("150405.000000000") + "-" + string(rune('A'+n%26)) + string(rune('a'+n/26)),
			}
			_ = es.SaveEvent(ctx, evt)
		}(i)
	}
	wg.Wait()

	events, err := es.ListEvents(ctx, "proj-1", ListOpts{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected some events after concurrent writes")
	}
}

func TestMemoryTraceStore_SaveAndList(t *testing.T) {
	ctx := context.Background()
	ts := NewMemoryTraceStore()
	now := time.Now().UTC()

	txn := &StoredTransaction{
		ID:             "txn-1",
		ProjectID:      "proj-1",
		EventID:        "trace-evt-1",
		TraceID:        "trace-1",
		SpanID:         "span-1",
		Transaction:    "GET /items",
		Op:             "http.server",
		Status:         "ok",
		Platform:       "javascript",
		StartTimestamp: now.Add(-250 * time.Millisecond),
		EndTimestamp:   now,
		DurationMS:     250,
		Measurements: map[string]StoredMeasurement{
			"lcp": {Value: 1200, Unit: "millisecond"},
		},
		Spans: []StoredSpan{{
			ID:             "child-1",
			ProjectID:      "proj-1",
			TraceID:        "trace-1",
			SpanID:         "span-2",
			ParentSpanID:   "span-1",
			Op:             "db",
			Description:    "SELECT 1",
			StartTimestamp: now.Add(-200 * time.Millisecond),
			EndTimestamp:   now.Add(-100 * time.Millisecond),
			DurationMS:     100,
		}},
	}
	if err := ts.SaveTransaction(ctx, txn); err != nil {
		t.Fatalf("SaveTransaction: %v", err)
	}

	got, err := ts.GetTransaction(ctx, "proj-1", "trace-evt-1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.Transaction != "GET /items" {
		t.Fatalf("Transaction = %q, want GET /items", got.Transaction)
	}

	items, err := ts.ListTransactions(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}

	spans, err := ts.ListTraceSpans(ctx, "proj-1", "trace-1")
	if err != nil {
		t.Fatalf("ListTraceSpans: %v", err)
	}
	if len(spans) != 1 || spans[0].Description != "SELECT 1" {
		t.Fatalf("unexpected spans: %+v", spans)
	}

	traceItems, err := ts.ListTransactionsByTrace(ctx, "proj-1", "trace-1")
	if err != nil {
		t.Fatalf("ListTransactionsByTrace: %v", err)
	}
	if len(traceItems) != 1 || traceItems[0].EventID != "trace-evt-1" {
		t.Fatalf("unexpected trace transactions: %+v", traceItems)
	}

	txn.DurationMS = 500
	if err := ts.SaveTransaction(ctx, txn); err != nil {
		t.Fatalf("SaveTransaction overwrite: %v", err)
	}
	items, err = ts.ListTransactions(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListTransactions after overwrite: %v", err)
	}
	if len(items) != 1 || items[0].DurationMS != 500 {
		t.Fatalf("unexpected overwritten transactions: %+v", items)
	}
}
