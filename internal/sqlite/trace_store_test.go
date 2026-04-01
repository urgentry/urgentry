package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestTraceStoreSaveAndList(t *testing.T) {
	db := openStoreTestDB(t)
	seedReleaseHealthProject(t, db, "org-1", "acme", "proj-1", "backend")

	traces := NewTraceStore(db)
	now := time.Now().UTC()
	txn := &store.StoredTransaction{
		ProjectID:      "proj-1",
		EventID:        "txn-evt-1",
		TraceID:        "trace-1",
		SpanID:         "root-1",
		Transaction:    "GET /items",
		Op:             "http.server",
		Status:         "ok",
		Platform:       "javascript",
		Environment:    "production",
		ReleaseID:      "web@1.2.3",
		StartTimestamp: now.Add(-250 * time.Millisecond),
		EndTimestamp:   now,
		DurationMS:     250,
		Tags:           map[string]string{"environment": "production"},
		Measurements: map[string]store.StoredMeasurement{
			"lcp": {Value: 1200, Unit: "millisecond"},
		},
		NormalizedJSON: json.RawMessage(`{"type":"transaction"}`),
		Spans: []store.StoredSpan{{
			ProjectID:          "proj-1",
			TransactionEventID: "txn-evt-1",
			TraceID:            "trace-1",
			SpanID:             "db-1",
			ParentSpanID:       "root-1",
			Op:                 "db",
			Description:        "SELECT 1",
			Status:             "ok",
			StartTimestamp:     now.Add(-200 * time.Millisecond),
			EndTimestamp:       now.Add(-100 * time.Millisecond),
			DurationMS:         100,
			Tags:               map[string]string{"db.system": "sqlite"},
			Data:               map[string]any{"rows": 1},
		}},
	}
	if err := traces.SaveTransaction(context.Background(), txn); err != nil {
		t.Fatalf("SaveTransaction: %v", err)
	}

	got, err := traces.GetTransaction(context.Background(), "proj-1", "txn-evt-1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.Transaction != "GET /items" || len(got.Spans) != 1 {
		t.Fatalf("unexpected transaction: %+v", got)
	}

	items, err := traces.ListTransactions(context.Background(), "proj-1", 10)
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if len(items) != 1 || items[0].TraceID != "trace-1" {
		t.Fatalf("unexpected transactions: %+v", items)
	}

	spans, err := traces.ListTraceSpans(context.Background(), "proj-1", "trace-1")
	if err != nil {
		t.Fatalf("ListTraceSpans: %v", err)
	}
	if len(spans) != 1 || spans[0].Description != "SELECT 1" {
		t.Fatalf("unexpected spans: %+v", spans)
	}

	traceItems, err := traces.ListTransactionsByTrace(context.Background(), "proj-1", "trace-1")
	if err != nil {
		t.Fatalf("ListTransactionsByTrace: %v", err)
	}
	if len(traceItems) != 1 || traceItems[0].EventID != "txn-evt-1" {
		t.Fatalf("unexpected trace transactions: %+v", traceItems)
	}
}
