package issue

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/normalize"
	"urgentry/internal/sourcemap"
	"urgentry/internal/store"
)

// TestProcessor_SourceMapResolution verifies end-to-end source map resolution:
// upload a source map, send a minified JS error, see original code in the stored event.
func TestProcessor_SourceMapResolution(t *testing.T) {
	ctx := context.Background()

	// --- Set up source map store with a test source map ---
	smStore := sourcemap.NewMemoryStore()
	resolver := &sourcemap.Resolver{Store: smStore}

	// A minimal source map: app.min.js maps line 1 col 0 -> src/app.js line 10 col 0
	// with function name "handleClick".
	sourceMap := map[string]any{
		"version":  3,
		"file":     "app.min.js",
		"sources":  []string{"src/app.js"},
		"names":    []string{"handleClick"},
		"mappings": "AAUAA",
		// AAUAA decodes to: genCol=0, srcIdx=0, srcLine=10, srcCol=0, nameIdx=0
	}
	smData, _ := json.Marshal(sourceMap)

	// Upload the source map under the name "app.min.js.map"
	err := smStore.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-1",
		ProjectID: "proj-js",
		ReleaseID: "frontend@1.0.0",
		Name:      "app.min.js.map",
	}, smData)
	if err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	// --- Build processor with source map resolver ---
	proc := &Processor{
		Events:     store.NewMemoryEventStore(),
		Groups:     NewMemoryGroupStore(),
		Blobs:      store.NewMemoryBlobStore(),
		SourceMaps: resolver,
	}

	// --- Send an event with a minified frame ---
	payload := map[string]any{
		"platform": "javascript",
		"level":    "error",
		"release":  "frontend@1.0.0",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "TypeError",
					"value": "Cannot read property 'x' of undefined",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "app.min.js",
								"function": "n",
								"lineno":   1,
								"colno":    1,
								"in_app":   true,
							},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)

	result, err := proc.Process(ctx, "proj-js", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// --- Verify the stored event has resolved frames ---
	evt, err := proc.Events.GetEvent(ctx, "proj-js", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	// Parse the normalized JSON to check the resolved frame.
	var normalized normalize.Event
	if err := json.Unmarshal(evt.NormalizedJSON, &normalized); err != nil {
		t.Fatalf("unmarshal normalized: %v", err)
	}

	if normalized.Exception == nil || len(normalized.Exception.Values) == 0 {
		t.Fatal("expected exception in normalized event")
	}

	exc := normalized.Exception.Values[0]
	if exc.Stacktrace == nil || len(exc.Stacktrace.Frames) == 0 {
		t.Fatal("expected stacktrace frames")
	}

	frame := exc.Stacktrace.Frames[0]

	// Filename should be resolved to the original source.
	if frame.Filename != "src/app.js" {
		t.Errorf("Filename = %q, want %q", frame.Filename, "src/app.js")
	}

	// AbsPath should contain the original minified filename.
	if frame.AbsPath != "app.min.js" {
		t.Errorf("AbsPath = %q, want %q", frame.AbsPath, "app.min.js")
	}

	// Line should be resolved (VLQ "AAUAA" maps srcLine=10, so 1-based = 11).
	if frame.Lineno != 11 {
		t.Errorf("Lineno = %d, want 11", frame.Lineno)
	}

	// Function name should be resolved from the names array.
	if frame.Function != "handleClick" {
		t.Errorf("Function = %q, want %q", frame.Function, "handleClick")
	}
}

// TestProcessor_NoSourceMap verifies that events without source maps pass through unmodified.
func TestProcessor_NoSourceMap(t *testing.T) {
	ctx := context.Background()

	smStore := sourcemap.NewMemoryStore()
	resolver := &sourcemap.Resolver{Store: smStore}

	proc := &Processor{
		Events:     store.NewMemoryEventStore(),
		Groups:     NewMemoryGroupStore(),
		Blobs:      store.NewMemoryBlobStore(),
		SourceMaps: resolver,
	}

	payload := map[string]any{
		"platform": "javascript",
		"level":    "error",
		"release":  "frontend@2.0.0",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "ReferenceError",
					"value": "foo is not defined",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "bundle.js",
								"function": "a",
								"lineno":   1,
								"colno":    500,
								"in_app":   true,
							},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)

	result, err := proc.Process(ctx, "proj-js", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	evt, err := proc.Events.GetEvent(ctx, "proj-js", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	var normalized normalize.Event
	if err := json.Unmarshal(evt.NormalizedJSON, &normalized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	frame := normalized.Exception.Values[0].Stacktrace.Frames[0]

	// No source map uploaded for this release, so frame should be unchanged.
	if frame.Filename != "bundle.js" {
		t.Errorf("Filename = %q, want %q (unchanged)", frame.Filename, "bundle.js")
	}
	if frame.AbsPath != "" {
		t.Errorf("AbsPath = %q, want empty (no source map)", frame.AbsPath)
	}
	if frame.Function != "a" {
		t.Errorf("Function = %q, want %q (unchanged)", frame.Function, "a")
	}
}

// TestProcessor_NilSourceMaps verifies processor works fine with nil SourceMaps.
func TestProcessor_NilSourceMaps(t *testing.T) {
	ctx := context.Background()

	proc := &Processor{
		Events: store.NewMemoryEventStore(),
		Groups: NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		// SourceMaps deliberately nil
	}

	payload := map[string]any{
		"platform": "javascript",
		"level":    "error",
		"release":  "v1.0.0",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  "Error",
					"value": "test",
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{"filename": "test.js", "function": "x", "lineno": 1, "colno": 1},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(payload)

	_, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process with nil SourceMaps: %v", err)
	}
}
