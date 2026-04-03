package pipeline

import (
	"testing"
	"time"

	"urgentry/internal/normalize"
)

// makeSpan is a test helper for building spans.
func makeSpan(spanID, parentID, op, desc string, startMS, endMS int64) normalize.Span {
	base := time.Unix(0, 0).UTC()
	return normalize.Span{
		SpanID:         spanID,
		ParentSpanID:   parentID,
		Op:             op,
		Description:    desc,
		StartTimestamp: base.Add(time.Duration(startMS) * time.Millisecond),
		Timestamp:      base.Add(time.Duration(endMS) * time.Millisecond),
	}
}

// --- N+1 DB detection ---

func TestDetectNPlusOneDB_Detected(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM users WHERE id = 1", 0, 5),
		makeSpan("s2", "p0", "db", "SELECT * FROM users WHERE id = 2", 5, 10),
		makeSpan("s3", "p0", "db", "SELECT * FROM users WHERE id = 3", 10, 15),
		makeSpan("s4", "p0", "db", "SELECT * FROM users WHERE id = 4", 15, 20),
		makeSpan("s5", "p0", "db", "SELECT * FROM users WHERE id = 5", 20, 25),
	}
	evt := &normalize.Event{EventID: "evt1", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Type != PerfIssueNPlusOneDB {
		t.Errorf("expected type %q, got %q", PerfIssueNPlusOneDB, issues[0].Type)
	}
	if len(issues[0].SpanIDs) != 5 {
		t.Errorf("expected 5 span IDs, got %d", len(issues[0].SpanIDs))
	}
	if issues[0].ParentSpanID != "p0" {
		t.Errorf("expected parent span ID %q, got %q", "p0", issues[0].ParentSpanID)
	}
}

func TestDetectNPlusOneDB_BelowThreshold(t *testing.T) {
	// Only 4 consecutive same-template db spans — should NOT trigger.
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM users WHERE id = 1", 0, 5),
		makeSpan("s2", "p0", "db", "SELECT * FROM users WHERE id = 2", 5, 10),
		makeSpan("s3", "p0", "db", "SELECT * FROM users WHERE id = 3", 10, 15),
		makeSpan("s4", "p0", "db", "SELECT * FROM users WHERE id = 4", 15, 20),
	}
	evt := &normalize.Event{EventID: "evt2", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueNPlusOneDB {
			t.Errorf("unexpected N+1 detection with only 4 spans")
		}
	}
}

func TestDetectNPlusOneDB_DifferentTemplates(t *testing.T) {
	// 5 db spans but with different query templates — no N+1.
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM users WHERE id = 1", 0, 5),
		makeSpan("s2", "p0", "db", "SELECT * FROM orders WHERE user_id = 2", 5, 10),
		makeSpan("s3", "p0", "db", "SELECT * FROM users WHERE id = 3", 10, 15),
		makeSpan("s4", "p0", "db", "SELECT * FROM orders WHERE user_id = 4", 15, 20),
		makeSpan("s5", "p0", "db", "SELECT * FROM users WHERE id = 5", 20, 25),
	}
	evt := &normalize.Event{EventID: "evt3", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueNPlusOneDB {
			t.Errorf("unexpected N+1 detection with different query templates")
		}
	}
}

func TestDetectNPlusOneDB_ExactlyAtThreshold(t *testing.T) {
	// Exactly 5 — boundary case.
	spans := make([]normalize.Span, 5)
	for i := range spans {
		spans[i] = makeSpan("s"+itoa(i), "p0", "db",
			"SELECT name FROM products WHERE id = "+itoa(i+1),
			int64(i*5), int64((i+1)*5))
	}
	evt := &normalize.Event{EventID: "evt4", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	found := false
	for _, iss := range issues {
		if iss.Type == PerfIssueNPlusOneDB {
			found = true
		}
	}
	if !found {
		t.Error("expected N+1 detection at exactly threshold (5 spans)")
	}
}

func TestDetectNPlusOneDB_NonDBSpansInterrupt(t *testing.T) {
	// A non-db span in the middle resets the run.
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM users WHERE id = 1", 0, 5),
		makeSpan("s2", "p0", "db", "SELECT * FROM users WHERE id = 2", 5, 10),
		makeSpan("s3", "p0", "db", "SELECT * FROM users WHERE id = 3", 10, 15),
		makeSpan("mx", "p0", "http.client", "https://api.example.com/endpoint", 15, 20),
		makeSpan("s4", "p0", "db", "SELECT * FROM users WHERE id = 4", 20, 25),
		makeSpan("s5", "p0", "db", "SELECT * FROM users WHERE id = 5", 25, 30),
	}
	evt := &normalize.Event{EventID: "evt5", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueNPlusOneDB {
			t.Errorf("unexpected N+1 detection when non-db span interrupts the run")
		}
	}
}

// --- Slow DB detection ---

func TestDetectSlowDB_Detected(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM reports", 0, 1500),
	}
	evt := &normalize.Event{EventID: "evt10", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Type != PerfIssueSlowDB {
		t.Errorf("expected type %q, got %q", PerfIssueSlowDB, issues[0].Type)
	}
	if len(issues[0].SpanIDs) != 1 || issues[0].SpanIDs[0] != "s1" {
		t.Errorf("unexpected SpanIDs: %v", issues[0].SpanIDs)
	}
}

func TestDetectSlowDB_ExactThreshold_NotFlagged(t *testing.T) {
	// Exactly 1000ms — must NOT be flagged (strictly greater than).
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT * FROM reports", 0, 1000),
	}
	evt := &normalize.Event{EventID: "evt11", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueSlowDB {
			t.Error("span at exactly 1000ms should not be flagged")
		}
	}
}

func TestDetectSlowDB_FastSpan_NotFlagged(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db", "SELECT id FROM users LIMIT 1", 0, 50),
	}
	evt := &normalize.Event{EventID: "evt12", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueSlowDB {
			t.Error("fast span should not be flagged as slow")
		}
	}
}

func TestDetectSlowDB_SubOpPrefix(t *testing.T) {
	// Op "db.query" should still match as a slow DB span.
	spans := []normalize.Span{
		makeSpan("s1", "p0", "db.query", "SELECT count(*) FROM logs", 0, 2000),
	}
	evt := &normalize.Event{EventID: "evt13", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)

	found := false
	for _, iss := range issues {
		if iss.Type == PerfIssueSlowDB {
			found = true
		}
	}
	if !found {
		t.Error("expected slow DB detection for op 'db.query'")
	}
}

// --- Consecutive HTTP detection ---

func TestDetectConsecutiveHTTP_Detected(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("h1", "p0", "http.client", "https://api.example.com/users/1", 0, 100),
		makeSpan("h2", "p0", "http.client", "https://api.example.com/users/2", 100, 200),
		makeSpan("h3", "p0", "http.client", "https://api.example.com/users/3", 200, 300),
	}
	evt := &normalize.Event{EventID: "evt20", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Type != PerfIssueConsecutiveHTTP {
		t.Errorf("expected type %q, got %q", PerfIssueConsecutiveHTTP, issues[0].Type)
	}
	if len(issues[0].SpanIDs) != 3 {
		t.Errorf("expected 3 span IDs, got %d", len(issues[0].SpanIDs))
	}
}

func TestDetectConsecutiveHTTP_BelowThreshold(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("h1", "p0", "http.client", "https://api.example.com/a", 0, 100),
		makeSpan("h2", "p0", "http.client", "https://api.example.com/b", 100, 200),
	}
	evt := &normalize.Event{EventID: "evt21", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueConsecutiveHTTP {
			t.Error("unexpected consecutive HTTP detection with only 2 spans")
		}
	}
}

func TestDetectConsecutiveHTTP_DifferentHosts(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("h1", "p0", "http.client", "https://api1.example.com/a", 0, 100),
		makeSpan("h2", "p0", "http.client", "https://api2.example.com/b", 100, 200),
		makeSpan("h3", "p0", "http.client", "https://api3.example.com/c", 200, 300),
	}
	evt := &normalize.Event{EventID: "evt22", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueConsecutiveHTTP {
			t.Error("unexpected consecutive HTTP detection for different hosts")
		}
	}
}

func TestDetectConsecutiveHTTP_InterruptedByOtherOp(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("h1", "p0", "http.client", "https://api.example.com/a", 0, 100),
		makeSpan("h2", "p0", "http.client", "https://api.example.com/b", 100, 200),
		makeSpan("d1", "p0", "db", "SELECT 1", 200, 210),
		makeSpan("h3", "p0", "http.client", "https://api.example.com/c", 210, 310),
	}
	evt := &normalize.Event{EventID: "evt23", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	for _, iss := range issues {
		if iss.Type == PerfIssueConsecutiveHTTP {
			t.Error("unexpected consecutive HTTP detection when run is interrupted")
		}
	}
}

func TestDetectConsecutiveHTTP_ExactlyAtThreshold(t *testing.T) {
	spans := []normalize.Span{
		makeSpan("h1", "p0", "http.client", "https://svc.internal/1", 0, 50),
		makeSpan("h2", "p0", "http.client", "https://svc.internal/2", 50, 100),
		makeSpan("h3", "p0", "http.client", "https://svc.internal/3", 100, 150),
	}
	evt := &normalize.Event{EventID: "evt24", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)
	found := false
	for _, iss := range issues {
		if iss.Type == PerfIssueConsecutiveHTTP {
			found = true
		}
	}
	if !found {
		t.Error("expected consecutive HTTP detection at exactly threshold (3 spans)")
	}
}

// --- Multiple issues in one event ---

func TestDetectPerformanceIssues_MultipleIssues(t *testing.T) {
	spans := []normalize.Span{
		// N+1 DB run (5 spans)
		makeSpan("d1", "p0", "db", "SELECT * FROM items WHERE id = 1", 0, 5),
		makeSpan("d2", "p0", "db", "SELECT * FROM items WHERE id = 2", 5, 10),
		makeSpan("d3", "p0", "db", "SELECT * FROM items WHERE id = 3", 10, 15),
		makeSpan("d4", "p0", "db", "SELECT * FROM items WHERE id = 4", 15, 20),
		makeSpan("d5", "p0", "db", "SELECT * FROM items WHERE id = 5", 20, 25),
		// Slow DB span
		makeSpan("ds", "p0", "db", "SELECT * FROM archive", 25, 2000),
		// Consecutive HTTP (3 spans)
		makeSpan("h1", "p0", "http.client", "https://notify.example.com/push/1", 2000, 2100),
		makeSpan("h2", "p0", "http.client", "https://notify.example.com/push/2", 2100, 2200),
		makeSpan("h3", "p0", "http.client", "https://notify.example.com/push/3", 2200, 2300),
	}
	evt := &normalize.Event{EventID: "evt30", Type: "transaction", Spans: spans}
	issues := detectPerformanceIssues(evt)

	typeCount := map[string]int{}
	for _, iss := range issues {
		typeCount[iss.Type]++
	}

	if typeCount[PerfIssueNPlusOneDB] != 1 {
		t.Errorf("expected 1 N+1 DB issue, got %d", typeCount[PerfIssueNPlusOneDB])
	}
	if typeCount[PerfIssueSlowDB] != 1 {
		t.Errorf("expected 1 slow DB issue, got %d", typeCount[PerfIssueSlowDB])
	}
	if typeCount[PerfIssueConsecutiveHTTP] != 1 {
		t.Errorf("expected 1 consecutive HTTP issue, got %d", typeCount[PerfIssueConsecutiveHTTP])
	}
}

// --- Non-transaction event ---

func TestDetectPerformanceIssues_NonTransactionEvent_NoIssues(t *testing.T) {
	// Even with slow DB spans, an error event should be ignored (detection is
	// only called for transactions by the pipeline — but this tests the
	// function's own nil/empty guard).
	evt := &normalize.Event{EventID: "errEvt", Type: "error", Spans: nil}
	issues := detectPerformanceIssues(evt)
	if len(issues) != 0 {
		t.Errorf("expected no issues for event with no spans, got %d", len(issues))
	}
}

// --- queryTemplate helper ---

func TestQueryTemplate(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"SELECT * FROM users WHERE id = 1", "select * from users where id = ?"},
		{"SELECT * FROM users WHERE id = 42", "select * from users where id = ?"},
		{"SELECT name FROM orders WHERE status = 'paid' AND id = 99", "select name from orders where status = ? and id = ?"},
		{"INSERT INTO logs (msg) VALUES ('hello world')", "insert into logs (msg) values (?)"},
	}
	for _, c := range cases {
		got := queryTemplate(c.input)
		if got != c.want {
			t.Errorf("queryTemplate(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// --- extractHost helper ---

func TestExtractHost(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://api.example.com/users/1", "api.example.com"},
		{"http://svc.internal:8080/health", "svc.internal:8080"},
		{"https://cdn.example.com", "cdn.example.com"},
	}
	for _, c := range cases {
		got := extractHost(c.input)
		if got != c.want {
			t.Errorf("extractHost(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
