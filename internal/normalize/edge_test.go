package normalize

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestEpochFloatTimestamp(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","timestamp":1774699200.123}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	// 1774699200 = 2026-03-28T12:00:00Z
	want := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	diff := evt.Timestamp.Sub(want)
	if math.Abs(diff.Seconds()) > 1 {
		t.Errorf("timestamp = %v, want ~%v (diff %v)", evt.Timestamp, want, diff)
	}
}

func TestMessageObjectFormat(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","message":{"formatted":"hello","message":"hi"}}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if evt.Message != "hello" {
		t.Errorf("message = %q, want %q", evt.Message, "hello")
	}
}

func TestMessageObjectFallback(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","message":{"message":"hi"}}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if evt.Message != "hi" {
		t.Errorf("message = %q, want %q", evt.Message, "hi")
	}
}

func TestEmptyExceptionList(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","exception":{"values":[]}}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// Must not panic, and exception should be set
	if evt.Exception == nil {
		t.Error("exception should not be nil")
	}
	if len(evt.Exception.Values) != 0 {
		t.Errorf("exception values = %d, want 0", len(evt.Exception.Values))
	}
}

func TestNilStacktraceInException(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","exception":{"values":[{"type":"Err","value":"msg"}]}}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// Must not panic
	if evt.Exception == nil || len(evt.Exception.Values) != 1 {
		t.Fatal("expected 1 exception")
	}
	if evt.Exception.Values[0].Stacktrace != nil {
		t.Error("stacktrace should be nil")
	}
}

func TestCulpritNoInAppFrames(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"event_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "Err",
					"value": "msg",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "stdlib/core.py",
								"function": "run",
								"module":   "stdlib.core",
								"in_app":   false,
							},
						},
					},
				},
			},
		},
	})

	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	// No in_app frames, so Culprit() should return "" (no in_app match)
	got := evt.Culprit()
	if got != "" {
		t.Errorf("culprit = %q, want empty string (no in_app frames)", got)
	}
}

func TestCulpritEmptyException(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","exception":{"values":[]}}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := evt.Culprit(); got != "" {
		t.Errorf("culprit = %q, want empty string", got)
	}
}

func TestCulpritNoException(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","message":"just a message"}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := evt.Culprit(); got != "" {
		t.Errorf("culprit = %q, want empty string", got)
	}
}

func TestTitleMessageOnly(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","message":"Something went wrong"}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := evt.Title(); got != "Something went wrong" {
		t.Errorf("title = %q, want %q", got, "Something went wrong")
	}
}

func TestTitleLongMessage(t *testing.T) {
	// Message > 100 chars should be truncated
	long := ""
	for i := 0; i < 120; i++ {
		long += "x"
	}
	raw, _ := json.Marshal(map[string]string{
		"event_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"message":  long,
	})
	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := evt.Title(); len(got) != 100 {
		t.Errorf("title length = %d, want 100", len(got))
	}
}

func TestTitleNoContent(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef"}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := evt.Title(); got != "<no title>" {
		t.Errorf("title = %q, want %q", got, "<no title>")
	}
}
