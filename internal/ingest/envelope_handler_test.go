package ingest

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClientReportAcceptsNumericTimestamp(t *testing.T) {
	var report clientReportPayload
	if err := json.Unmarshal([]byte(`{
		"timestamp": 1712345678.25,
		"discarded_events": [
			{"reason": "sample_rate", "category": "error", "quantity": 2}
		]
	}`), &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	got := parseSessionTime(string(report.Timestamp))
	want := time.Unix(1712345678, 250_000_000).UTC()
	if !got.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestClientReportAcceptsStringTimestamp(t *testing.T) {
	var report clientReportPayload
	if err := json.Unmarshal([]byte(`{
		"timestamp": "2026-05-21T13:00:00Z",
		"discarded_events": []
	}`), &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(report.Timestamp) != "2026-05-21T13:00:00Z" {
		t.Fatalf("timestamp = %q", report.Timestamp)
	}
}
