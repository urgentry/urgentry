package issue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"urgentry/internal/sourcemap"
	"urgentry/internal/store"
)

func sourceMapFixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testfixtures", "sourcemap", "testdata")
}

func TestProcessor_SourceMapResolutionGolden(t *testing.T) {
	t.Helper()

	fixtureDir := sourceMapFixtureDir(t)
	sourceMapData, err := os.ReadFile(filepath.Join(fixtureDir, "app.min.js.map.json"))
	if err != nil {
		t.Fatalf("read source map fixture: %v", err)
	}
	rawEvent, err := os.ReadFile(filepath.Join(fixtureDir, "minified_event.json"))
	if err != nil {
		t.Fatalf("read minified event fixture: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(fixtureDir, "resolved_event.expected.json"))
	if err != nil {
		t.Fatalf("read resolved event golden: %v", err)
	}

	smStore := sourcemap.NewMemoryStore()
	if err := smStore.SaveArtifact(context.Background(), &sourcemap.Artifact{
		ID:        "sm-golden-1",
		ProjectID: "proj-js",
		ReleaseID: "frontend@1.0.0",
		Name:      "app.min.js.map",
	}, sourceMapData); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	proc := &Processor{
		Events:     store.NewMemoryEventStore(),
		Groups:     NewMemoryGroupStore(),
		Blobs:      store.NewMemoryBlobStore(),
		SourceMaps: &sourcemap.Resolver{Store: smStore},
	}
	result, err := proc.Process(context.Background(), "proj-js", rawEvent)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	evt, err := proc.Events.GetEvent(context.Background(), "proj-js", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	var gotObj any
	if err := json.Unmarshal(evt.NormalizedJSON, &gotObj); err != nil {
		t.Fatalf("unmarshal got normalized event: %v", err)
	}
	var wantObj any
	if err := json.Unmarshal(want, &wantObj); err != nil {
		t.Fatalf("unmarshal golden event: %v", err)
	}

	gotStable, err := json.Marshal(gotObj)
	if err != nil {
		t.Fatalf("marshal got normalized event: %v", err)
	}
	wantStable, err := json.Marshal(wantObj)
	if err != nil {
		t.Fatalf("marshal golden event: %v", err)
	}

	if string(gotStable) != string(wantStable) {
		t.Fatalf("source-map resolution golden mismatch\n--- got ---\n%s\n--- want ---\n%s", evt.NormalizedJSON, want)
	}
}
