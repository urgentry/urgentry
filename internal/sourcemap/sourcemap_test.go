package sourcemap

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMemoryStoreAndResolver(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	data := mustJSON(t, map[string]any{
		"version":  3,
		"file":     "app.min.js",
		"sources":  []string{"src/app.js"},
		"names":    []string{"handleClick"},
		"mappings": "AAUAA",
	})
	artifact := &Artifact{
		ID:        "sm-1",
		ProjectID: "proj-1",
		ReleaseID: "frontend@1.0.0",
		Name:      "app.min.js.map",
	}
	if err := store.SaveArtifact(ctx, artifact, data); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	got, payload, err := store.GetArtifact(ctx, "sm-1")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if got == nil || string(payload) != string(data) {
		t.Fatalf("GetArtifact = %+v payload=%q", got, payload)
	}

	list, err := store.ListByRelease(ctx, "proj-1", "frontend@1.0.0")
	if err != nil {
		t.Fatalf("ListByRelease: %v", err)
	}
	if len(list) != 1 || list[0].ID != "sm-1" {
		t.Fatalf("ListByRelease = %+v", list)
	}

	resolver := &Resolver{Store: store}
	file, line, function, err := resolver.Resolve(ctx, "proj-1", "frontend@1.0.0", "app.min.js", 1, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if file != "src/app.js" || line != 11 || function != "handleClick" {
		t.Fatalf("Resolve = %q %d %q", file, line, function)
	}

	if err := store.DeleteArtifact(ctx, "sm-1"); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	missing, _, err := store.LookupByName(ctx, "proj-1", "frontend@1.0.0", "app.min.js.map")
	if err != nil {
		t.Fatalf("LookupByName after delete: %v", err)
	}
	if missing != nil {
		t.Fatalf("LookupByName after delete = %+v, want nil", missing)
	}
}

func TestResolveRejectsInvalidSourceMap(t *testing.T) {
	if _, _, _, err := Resolve([]byte(`{"version":2}`), 1, 1); err == nil {
		t.Fatal("expected unsupported version error")
	}
	if _, _, _, err := Resolve([]byte(`{"version":3,"sources":["a.js"],"names":[],"mappings":"!"}`), 1, 1); err == nil {
		t.Fatal("expected invalid mappings error")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}
