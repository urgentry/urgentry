package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fixturesDir returns the absolute path to eval/fixtures/envelopes/.
func fixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	// thisFile = .../internal/envelope/envelope_test.go
	// fixtures = .../eval/fixtures/envelopes/
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "envelopes")
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir(t), name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func TestSingleError(t *testing.T) {
	env, err := Parse(loadFixture(t, "single_error.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if env.Header.EventID != "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1" {
		t.Errorf("event_id = %q, want a1a1...", env.Header.EventID)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(env.Items))
	}
	if env.Items[0].Header.Type != "event" {
		t.Errorf("item type = %q, want event", env.Items[0].Header.Type)
	}
	// Verify payload is valid JSON with matching event_id.
	var payload map[string]any
	if err := json.Unmarshal(env.Items[0].Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["event_id"] != "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1" {
		t.Errorf("payload event_id = %v", payload["event_id"])
	}
}

func TestErrorWithAttachment(t *testing.T) {
	env, err := Parse(loadFixture(t, "error_with_attachment.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(env.Items))
	}
	if env.Items[0].Header.Type != "event" {
		t.Errorf("item[0] type = %q, want event", env.Items[0].Header.Type)
	}
	if env.Items[1].Header.Type != "attachment" {
		t.Errorf("item[1] type = %q, want attachment", env.Items[1].Header.Type)
	}
	if env.Items[1].Header.Filename != "log.txt" {
		t.Errorf("attachment filename = %q, want log.txt", env.Items[1].Header.Filename)
	}
	if string(env.Items[1].Payload) != "this is a log file" {
		t.Errorf("attachment payload = %q", string(env.Items[1].Payload))
	}
}

func TestUserFeedback(t *testing.T) {
	env, err := Parse(loadFixture(t, "user_feedback.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(env.Items))
	}
	if env.Items[0].Header.Type != "user_report" {
		t.Errorf("item type = %q, want user_report", env.Items[0].Header.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Items[0].Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["name"] != "Jane Doe" {
		t.Errorf("name = %v", payload["name"])
	}
}

func TestMultiItem(t *testing.T) {
	env, err := Parse(loadFixture(t, "multi_item.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(env.Items))
	}
	wantTypes := []string{"event", "attachment", "user_report"}
	for i, wt := range wantTypes {
		if env.Items[i].Header.Type != wt {
			t.Errorf("item[%d] type = %q, want %q", i, env.Items[i].Header.Type, wt)
		}
	}
}

func TestWithClientReport(t *testing.T) {
	env, err := Parse(loadFixture(t, "with_client_report.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(env.Items))
	}
	if env.Items[0].Header.Type != "event" {
		t.Errorf("item[0] type = %q, want event", env.Items[0].Header.Type)
	}
	if env.Items[1].Header.Type != "client_report" {
		t.Errorf("item[1] type = %q, want client_report", env.Items[1].Header.Type)
	}
}

func TestGoSDKError(t *testing.T) {
	env, err := Parse(loadFixture(t, "go_sdk_error.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if env.Header.EventID != "f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6" {
		t.Errorf("event_id = %q", env.Header.EventID)
	}
	if env.Header.SDK == nil || env.Header.SDK.Name != "sentry.go" {
		t.Errorf("sdk = %+v, want sentry.go", env.Header.SDK)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(env.Items))
	}
	if env.Items[0].Header.Type != "event" {
		t.Errorf("item type = %q, want event", env.Items[0].Header.Type)
	}
	// Verify Go-specific payload fields.
	var payload map[string]any
	if err := json.Unmarshal(env.Items[0].Payload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["platform"] != "go" {
		t.Errorf("platform = %v, want go", payload["platform"])
	}
}

func TestEmpty(t *testing.T) {
	env, err := Parse(loadFixture(t, "empty.envelope"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Items) != 0 {
		t.Errorf("items = %d, want 0", len(env.Items))
	}
}

func TestMalformedHeader(t *testing.T) {
	_, err := Parse(loadFixture(t, "malformed_header.envelope"))
	if err == nil {
		t.Fatal("expected error for malformed header, got nil")
	}
}

func TestEmptyInput(t *testing.T) {
	_, err := Parse(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	_, err = Parse([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseRoundTrip(t *testing.T) {
	// Verify all valid fixtures parse without error.
	fixtures := []string{
		"single_error.envelope",
		"error_with_attachment.envelope",
		"user_feedback.envelope",
		"multi_item.envelope",
		"with_client_report.envelope",
		"go_sdk_error.envelope",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data := loadFixture(t, name)
			env, err := Parse(data)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			for i, item := range env.Items {
				if item.Header.Type == "" {
					t.Errorf("item %d has empty type", i)
				}
				if len(item.Payload) == 0 {
					t.Errorf("item %d has empty payload", i)
				}
			}
		})
	}
}
