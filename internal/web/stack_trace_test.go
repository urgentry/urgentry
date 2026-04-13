package web

import (
	"testing"
)

func TestStackTraceFromPayload_BasicException(t *testing.T) {
	payload := `{
		"exception": {
			"values": [{
				"type": "ValueError",
				"value": "invalid literal",
				"stacktrace": {
					"frames": [
						{"filename": "lib/utils.py", "function": "helper", "lineno": 10, "in_app": false},
						{"filename": "app/views.py", "function": "index", "lineno": 42, "colno": 5, "in_app": true,
						 "context_line": "    raise ValueError('invalid literal')",
						 "pre_context": ["def index(request):", "    val = request.GET['x']"],
						 "post_context": ["", "def about(request):"]}
					]
				}
			}]
		}
	}`

	groups := stackTraceFromPayload([]byte(payload))
	if len(groups) != 1 {
		t.Fatalf("expected 1 exception group, got %d", len(groups))
	}
	eg := groups[0]

	if eg.Type != "ValueError" {
		t.Errorf("Type = %q, want ValueError", eg.Type)
	}
	if eg.Value != "invalid literal" {
		t.Errorf("Value = %q, want 'invalid literal'", eg.Value)
	}
	if !eg.HasFrames {
		t.Fatal("HasFrames should be true")
	}

	// Frames should be reversed (most recent first).
	if len(eg.Frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(eg.Frames))
	}

	// First frame = app/views.py (the last in the original array).
	f0 := eg.Frames[0]
	if f0.File != "app/views.py" {
		t.Errorf("frame[0].File = %q, want app/views.py", f0.File)
	}
	if !f0.InApp {
		t.Error("frame[0] should be in-app")
	}
	if f0.Collapsed {
		t.Error("in-app frame should not be collapsed")
	}
	if f0.LineNo != 42 {
		t.Errorf("frame[0].LineNo = %d, want 42", f0.LineNo)
	}
	if f0.ColNo != 5 {
		t.Errorf("frame[0].ColNo = %d, want 5", f0.ColNo)
	}
	if !f0.HasContext {
		t.Error("frame[0] should have context")
	}
	if f0.ContextLine != "    raise ValueError('invalid literal')" {
		t.Errorf("frame[0].ContextLine = %q", f0.ContextLine)
	}
	if len(f0.PreContext) != 2 {
		t.Errorf("frame[0].PreContext len = %d, want 2", len(f0.PreContext))
	}
	if len(f0.PostContext) != 2 {
		t.Errorf("frame[0].PostContext len = %d, want 2", len(f0.PostContext))
	}
	// Pre-context line numbers: lineno=42, pre_context has 2 lines => starts at 40.
	if f0.PreContext[0].Number != 40 {
		t.Errorf("pre_context[0].Number = %d, want 40", f0.PreContext[0].Number)
	}
	if f0.PostContext[0].Number != 43 {
		t.Errorf("post_context[0].Number = %d, want 43", f0.PostContext[0].Number)
	}

	// Second frame = lib/utils.py (library).
	f1 := eg.Frames[1]
	if f1.File != "lib/utils.py" {
		t.Errorf("frame[1].File = %q, want lib/utils.py", f1.File)
	}
	if f1.InApp {
		t.Error("frame[1] should not be in-app")
	}
	if !f1.Collapsed {
		t.Error("library frame should be collapsed")
	}

	// Counts
	if eg.InAppCount != 1 {
		t.Errorf("InAppCount = %d, want 1", eg.InAppCount)
	}
	if eg.LibCount != 1 {
		t.Errorf("LibCount = %d, want 1", eg.LibCount)
	}
}

func TestStackTraceFromPayload_EmptyPayload(t *testing.T) {
	if groups := stackTraceFromPayload(nil); groups != nil {
		t.Errorf("expected nil for nil input, got %v", groups)
	}
	if groups := stackTraceFromPayload([]byte("")); groups != nil {
		t.Errorf("expected nil for empty input, got %v", groups)
	}
	if groups := stackTraceFromPayload([]byte("not json")); groups != nil {
		t.Errorf("expected nil for invalid JSON, got %v", groups)
	}
}

func TestStackTraceFromPayload_NoException(t *testing.T) {
	payload := `{"message": "just a message"}`
	groups := stackTraceFromPayload([]byte(payload))
	if groups != nil {
		t.Errorf("expected nil for no exception, got %d groups", len(groups))
	}
}

func TestStackTraceFromPayload_ExceptionWithoutStacktrace(t *testing.T) {
	payload := `{
		"exception": {
			"values": [{"type": "Error", "value": "oops"}]
		}
	}`
	groups := stackTraceFromPayload([]byte(payload))
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].HasFrames {
		t.Error("HasFrames should be false when no stacktrace")
	}
	if groups[0].Type != "Error" {
		t.Errorf("Type = %q, want Error", groups[0].Type)
	}
}

func TestStackTraceFromPayload_MultipleExceptions(t *testing.T) {
	payload := `{
		"exception": {
			"values": [
				{
					"type": "IOError",
					"value": "disk full",
					"stacktrace": {
						"frames": [
							{"filename": "io.py", "function": "write", "lineno": 5, "in_app": false}
						]
					}
				},
				{
					"type": "AppError",
					"value": "save failed",
					"mechanism": {"type": "generic", "handled": false},
					"stacktrace": {
						"frames": [
							{"filename": "app.py", "function": "save", "lineno": 20, "in_app": true}
						]
					}
				}
			]
		}
	}`

	groups := stackTraceFromPayload([]byte(payload))
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	if groups[0].Type != "IOError" {
		t.Errorf("groups[0].Type = %q, want IOError", groups[0].Type)
	}
	if groups[1].Type != "AppError" {
		t.Errorf("groups[1].Type = %q, want AppError", groups[1].Type)
	}
	if groups[1].Mechanism != "generic" {
		t.Errorf("groups[1].Mechanism = %q, want generic", groups[1].Mechanism)
	}
	if groups[1].Handled != "false" {
		t.Errorf("groups[1].Handled = %q, want false", groups[1].Handled)
	}
}

func TestStackTraceFromPayload_SourceMapped(t *testing.T) {
	payload := `{
		"exception": {
			"values": [{
				"type": "TypeError",
				"value": "null ref",
				"stacktrace": {
					"frames": [{
						"filename": "src/app.ts",
						"function": "onClick",
						"lineno": 15,
						"abs_path": "dist/bundle.min.js",
						"in_app": true
					}]
				}
			}]
		}
	}`

	groups := stackTraceFromPayload([]byte(payload))
	if len(groups) != 1 || len(groups[0].Frames) != 1 {
		t.Fatal("expected 1 group with 1 frame")
	}
	f := groups[0].Frames[0]
	if f.MappedFrom != "mapped from dist/bundle.min.js" {
		t.Errorf("MappedFrom = %q", f.MappedFrom)
	}
}

func TestStackTraceFromPayload_SkipsEmptyFrames(t *testing.T) {
	payload := `{
		"exception": {
			"values": [{
				"type": "Error",
				"value": "test",
				"stacktrace": {
					"frames": [
						{"filename": "", "function": ""},
						{"filename": "app.go", "function": "main", "lineno": 1, "in_app": true}
					]
				}
			}]
		}
	}`

	groups := stackTraceFromPayload([]byte(payload))
	if len(groups) != 1 || len(groups[0].Frames) != 1 {
		t.Fatalf("expected 1 group with 1 frame (empty frame should be skipped), got %d groups", len(groups))
	}
	if groups[0].Frames[0].File != "app.go" {
		t.Errorf("File = %q, want app.go", groups[0].Frames[0].File)
	}
}
