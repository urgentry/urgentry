package web

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/sourcemap"
)

func mustSourceMap(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func TestResolveSourceContext_ResolvesFrames(t *testing.T) {
	ctx := context.Background()
	store := sourcemap.NewMemoryStore()

	smData := mustSourceMap(t, map[string]any{
		"version":  3,
		"file":     "app.min.js",
		"sources":  []string{"src/app.ts"},
		"names":    []string{"handleClick"},
		"mappings": "AAUAA",
	})
	if err := store.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-1",
		ProjectID: "proj-1",
		ReleaseID: "frontend@1.0.0",
		Name:      "app.min.js.map",
	}, smData); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	resolver := &sourcemap.Resolver{Store: store}

	groups := []exceptionGroup{{
		Type:  "TypeError",
		Value: "null ref",
		Frames: []richFrame{
			{
				File:     "app.min.js",
				Function: "a",
				LineNo:   1,
				ColNo:    1,
				InApp:    true,
			},
		},
		HasFrames: true,
	}}

	result := resolveSourceContext(ctx, resolver, "proj-1", "frontend@1.0.0", groups)

	if len(result) != 1 || len(result[0].Frames) != 1 {
		t.Fatalf("expected 1 group with 1 frame, got %d groups", len(result))
	}
	f := result[0].Frames[0]

	if f.File != "src/app.ts" {
		t.Errorf("File = %q, want src/app.ts", f.File)
	}
	if f.LineNo != 11 {
		t.Errorf("LineNo = %d, want 11", f.LineNo)
	}
	if f.Function != "handleClick" {
		t.Errorf("Function = %q, want handleClick", f.Function)
	}
	if f.MappedFrom != "mapped from app.min.js:1:1" {
		t.Errorf("MappedFrom = %q, want 'mapped from app.min.js:1:1'", f.MappedFrom)
	}
}

func TestResolveSourceContext_NoSourceMap(t *testing.T) {
	ctx := context.Background()
	store := sourcemap.NewMemoryStore()
	resolver := &sourcemap.Resolver{Store: store}

	groups := []exceptionGroup{{
		Type: "Error",
		Frames: []richFrame{
			{
				File:     "bundle.js",
				Function: "x",
				LineNo:   1,
				ColNo:    500,
				InApp:    true,
			},
		},
		HasFrames: true,
	}}

	result := resolveSourceContext(ctx, resolver, "proj-1", "v1.0.0", groups)

	f := result[0].Frames[0]
	// Frame should remain unchanged when no source map exists.
	if f.File != "bundle.js" {
		t.Errorf("File = %q, want bundle.js (unchanged)", f.File)
	}
	if f.MappedFrom != "" {
		t.Errorf("MappedFrom = %q, want empty", f.MappedFrom)
	}
}

func TestResolveSourceContext_NilResolver(t *testing.T) {
	groups := []exceptionGroup{{
		Type: "Error",
		Frames: []richFrame{
			{File: "app.js", LineNo: 1, Function: "f"},
		},
	}}

	result := resolveSourceContext(context.Background(), nil, "proj", "v1", groups)
	if result[0].Frames[0].File != "app.js" {
		t.Error("nil resolver should pass through unchanged")
	}
}

func TestResolveSourceContext_EmptyRelease(t *testing.T) {
	store := sourcemap.NewMemoryStore()
	resolver := &sourcemap.Resolver{Store: store}

	groups := []exceptionGroup{{
		Type: "Error",
		Frames: []richFrame{
			{File: "app.js", LineNo: 1, Function: "f"},
		},
	}}

	result := resolveSourceContext(context.Background(), resolver, "proj", "", groups)
	if result[0].Frames[0].File != "app.js" {
		t.Error("empty release should pass through unchanged")
	}
}

func TestResolveSourceContext_SkipsFramesWithoutLine(t *testing.T) {
	ctx := context.Background()
	store := sourcemap.NewMemoryStore()

	smData := mustSourceMap(t, map[string]any{
		"version":  3,
		"file":     "app.min.js",
		"sources":  []string{"src/app.ts"},
		"names":    []string{},
		"mappings": "AACA",
	})
	if err := store.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-2",
		ProjectID: "proj-1",
		ReleaseID: "v2.0.0",
		Name:      "app.min.js.map",
	}, smData); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	resolver := &sourcemap.Resolver{Store: store}

	groups := []exceptionGroup{{
		Type: "Error",
		Frames: []richFrame{
			{File: "app.min.js", Function: "x", LineNo: 0, ColNo: 0, InApp: true},
			{File: "app.min.js", Function: "y", LineNo: 1, ColNo: 1, InApp: true},
		},
		HasFrames: true,
	}}

	result := resolveSourceContext(ctx, resolver, "proj-1", "v2.0.0", groups)

	// First frame (LineNo=0) should be skipped.
	if result[0].Frames[0].File != "app.min.js" {
		t.Errorf("frame[0] should be unchanged, File = %q", result[0].Frames[0].File)
	}
	// Second frame should be resolved.
	if result[0].Frames[1].File != "src/app.ts" {
		t.Errorf("frame[1].File = %q, want src/app.ts", result[0].Frames[1].File)
	}
}

func TestResolveSourceContext_MappedFromWithoutColNo(t *testing.T) {
	ctx := context.Background()
	store := sourcemap.NewMemoryStore()

	smData := mustSourceMap(t, map[string]any{
		"version":  3,
		"file":     "app.min.js",
		"sources":  []string{"src/index.ts"},
		"names":    []string{},
		"mappings": "AACA",
	})
	if err := store.SaveArtifact(ctx, &sourcemap.Artifact{
		ID:        "sm-3",
		ProjectID: "proj-1",
		ReleaseID: "v3.0.0",
		Name:      "app.min.js.map",
	}, smData); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	resolver := &sourcemap.Resolver{Store: store}

	groups := []exceptionGroup{{
		Type: "Error",
		Frames: []richFrame{
			{File: "app.min.js", Function: "z", LineNo: 1, ColNo: 0, InApp: true},
		},
		HasFrames: true,
	}}

	result := resolveSourceContext(ctx, resolver, "proj-1", "v3.0.0", groups)
	f := result[0].Frames[0]

	// ColNo=0 means the mapped-from string should omit the column.
	if f.MappedFrom != "mapped from app.min.js:1" {
		t.Errorf("MappedFrom = %q, want 'mapped from app.min.js:1'", f.MappedFrom)
	}
}
