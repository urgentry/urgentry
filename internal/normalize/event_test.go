package normalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixtureDir() string {
	// Walk up from internal/normalize to repo root, then into eval/fixtures
	return filepath.Join("..", "..", "..", "..", "eval", "fixtures")
}

func loadFixture(t *testing.T, subdir, name string) []byte {
	t.Helper()
	path := filepath.Join(fixtureDir(), subdir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load fixture %s/%s: %v", subdir, name, err)
	}
	return data
}

func TestNormalizeBasicError(t *testing.T) {
	data := loadFixture(t, "store", "basic_error.json")
	evt, err := Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if evt.EventID != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4" {
		t.Errorf("event_id: got %q", evt.EventID)
	}
	if evt.Platform != "python" {
		t.Errorf("platform: got %q", evt.Platform)
	}
	if evt.Level != "error" {
		t.Errorf("level: got %q", evt.Level)
	}
	if evt.Release != "myapp@1.0.0" {
		t.Errorf("release: got %q", evt.Release)
	}
	if evt.Environment != "production" {
		t.Errorf("environment: got %q", evt.Environment)
	}

	// Exception
	if evt.Exception == nil || len(evt.Exception.Values) != 1 {
		t.Fatal("expected 1 exception")
	}
	exc := evt.Exception.Values[0]
	if exc.Type != "ValueError" {
		t.Errorf("exception type: got %q", exc.Type)
	}
	if exc.Stacktrace == nil || len(exc.Stacktrace.Frames) != 2 {
		t.Fatal("expected 2 frames")
	}

	// Tags
	if evt.Tags == nil || evt.Tags["os"] != "Linux" {
		t.Errorf("tags: got %v", evt.Tags)
	}

	// User
	if evt.User == nil || evt.User.ID != "user-123" {
		t.Errorf("user: got %v", evt.User)
	}

	// Breadcrumbs
	if evt.Breadcrumbs == nil || len(evt.Breadcrumbs.Values) != 2 {
		t.Errorf("breadcrumbs: got %v", evt.Breadcrumbs)
	}

	// Contexts
	if evt.Contexts == nil {
		t.Error("contexts nil")
	}

	// SDK
	if evt.SDK == nil || evt.SDK.Name != "sentry.python" {
		t.Errorf("sdk: got %v", evt.SDK)
	}

	// Title
	if evt.Title() != "ValueError: invalid literal for int() with base 10: 'abc'" {
		t.Errorf("title: got %q", evt.Title())
	}

	// Culprit
	if evt.Culprit() != "app.utils in parse_input" {
		t.Errorf("culprit: got %q", evt.Culprit())
	}
}

func TestNormalizeAllStoreFixtures(t *testing.T) {
	fixtures := []string{
		"basic_error.json",
		"js_browser_error.json",
		"js_node_error.json",
		"go_error.json",
		"java_error.json",
		"dotnet_error.json",
		"ruby_error.json",
		"tags_array_format.json",
		"epoch_timestamp.json",
		"python_full_realistic.json",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data := loadFixture(t, "store", name)
			evt, err := Normalize(data)
			if err != nil {
				t.Fatalf("normalize %s: %v", name, err)
			}
			if evt.EventID == "" {
				t.Error("empty event_id after normalization")
			}
			if evt.Platform == "" {
				t.Error("empty platform after normalization")
			}
			if evt.Level == "" {
				t.Error("empty level after normalization")
			}
		})
	}
}

func TestNormalizeTagsArrayFormat(t *testing.T) {
	data := loadFixture(t, "store", "tags_array_format.json")
	evt, err := Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if evt.Tags == nil {
		t.Fatal("tags nil")
	}
	if evt.Tags["browser"] != "Chrome" {
		t.Errorf("tags[browser]: got %q", evt.Tags["browser"])
	}
	if evt.Tags["custom_tag"] != "value" {
		t.Errorf("tags[custom_tag]: got %q", evt.Tags["custom_tag"])
	}
}

func TestNormalizeMissingEventID(t *testing.T) {
	data := loadFixture(t, "negative", "missing_event_id.json")
	evt, err := Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if evt.EventID == "" {
		t.Error("server should generate event_id when missing")
	}
	if len(evt.EventID) != 32 {
		t.Errorf("generated event_id should be 32 hex chars, got %d: %q", len(evt.EventID), evt.EventID)
	}
}

func TestNormalizeUnicodePreserved(t *testing.T) {
	data := loadFixture(t, "negative", "unicode_edge_cases.json")
	evt, err := Normalize(data)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if evt.Tags == nil || evt.Tags["emoji_tag"] != "🎉" {
		t.Errorf("emoji tag not preserved: got %v", evt.Tags)
	}
	if evt.Tags["cjk_tag"] != "你好世界" {
		t.Errorf("CJK tag not preserved: got %q", evt.Tags["cjk_tag"])
	}
}

func TestNormalizeDefaultEnvironment(t *testing.T) {
	raw := `{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef","platform":"go"}`
	evt, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if evt.Environment != "production" {
		t.Errorf("default environment: got %q, want %q", evt.Environment, "production")
	}
}

func TestNormalizeLevelNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"error", "error"},
		{"ERROR", "error"},
		{"warn", "warning"},
		{"warning", "warning"},
		{"fatal", "fatal"},
		{"info", "info"},
		{"debug", "debug"},
		{"", "error"},
		{"unknown", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{
				"event_id": "deadbeefdeadbeefdeadbeefdeadbeef",
				"level":    tt.input,
			})
			evt, err := Normalize(raw)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if evt.Level != tt.want {
				t.Errorf("level %q: got %q, want %q", tt.input, evt.Level, tt.want)
			}
		})
	}
}

func TestNormalizeDeterministic(t *testing.T) {
	data := loadFixture(t, "store", "basic_error.json")

	evt1, _ := Normalize(data)
	evt2, _ := Normalize(data)

	j1, _ := json.Marshal(evt1)
	j2, _ := json.Marshal(evt2)

	if string(j1) != string(j2) {
		t.Error("normalization is not deterministic")
	}
}

func TestNormalizeTransaction(t *testing.T) {
	raw := []byte(`{
		"type":"transaction",
		"event_id":"11111111111111111111111111111111",
		"platform":"javascript",
		"transaction":"GET /items/:id",
		"start_timestamp":"2026-03-27T12:00:00Z",
		"timestamp":"2026-03-27T12:00:01Z",
		"contexts":{"trace":{"trace_id":"trace-1","span_id":"root-1","op":"http.server","status":"ok"}},
		"measurements":{"lcp":{"value":1234.5,"unit":"millisecond"}},
		"spans":[
			{"trace_id":"trace-1","span_id":"child-1","parent_span_id":"root-1","op":"db","description":"SELECT 1","start_timestamp":"2026-03-27T12:00:00.100Z","timestamp":"2026-03-27T12:00:00.250Z"}
		]
	}`)
	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if evt.EventType() != "transaction" {
		t.Fatalf("EventType = %q, want transaction", evt.EventType())
	}
	if evt.Title() != "GET /items/:id" {
		t.Fatalf("Title = %q, want transaction title", evt.Title())
	}
	if evt.TraceContext().TraceID != "trace-1" {
		t.Fatalf("trace_id = %q, want trace-1", evt.TraceContext().TraceID)
	}
	if len(evt.Spans) != 1 || evt.Spans[0].Description != "SELECT 1" {
		t.Fatalf("unexpected spans: %+v", evt.Spans)
	}
	if evt.Measurements["lcp"].Value != 1234.5 {
		t.Fatalf("measurement lcp = %+v", evt.Measurements["lcp"])
	}
}

func TestNormalizeErrorWithTraceContextStaysError(t *testing.T) {
	raw := []byte(`{
		"event_id":"33333333333333333333333333333333",
		"platform":"javascript",
		"level":"error",
		"transaction":"GET /items/:id",
		"contexts":{"trace":{"trace_id":"trace-3","span_id":"root-3","op":"http.server","status":"internal_error"}},
		"exception":{"values":[{"type":"TypeError","value":"boom"}]}
	}`)

	evt, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if evt.EventType() != "error" {
		t.Fatalf("EventType = %q, want error", evt.EventType())
	}
	if evt.Title() != "TypeError: boom" {
		t.Fatalf("Title = %q, want exception title", evt.Title())
	}
}
