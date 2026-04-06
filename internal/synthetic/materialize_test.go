package synthetic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeArtifactCase(t *testing.T) {
	outputs, err := GenerateOutputs(RepoRoot())
	if err != nil {
		t.Fatalf("GenerateOutputs: %v", err)
	}
	for _, id := range []string{
		"artifact/envelope_attachment_text",
		"artifact/standalone_attachment_text",
		"artifact/source_map_basic",
		"artifact/proguard_basic",
		"artifact/import_bundle_minimal",
	} {
		var found ArtifactCase
		for _, item := range outputs.Artifacts.Cases {
			if item.ID == id {
				found = item
				break
			}
		}
		if found.ID == "" {
			t.Fatalf("artifact case %s not found", id)
		}
		body, name, err := MaterializeArtifactCase(found)
		if err != nil {
			t.Fatalf("MaterializeArtifactCase(%q): %v", id, err)
		}
		if len(body) == 0 {
			t.Fatalf("%s body is empty", id)
		}
		if strings.TrimSpace(name) == "" {
			t.Fatalf("%s output filename empty", id)
		}
	}
}

func TestWriteDeepCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDeepCorpus(dir); err != nil {
		t.Fatalf("WriteDeepCorpus: %v", err)
	}
	for _, rel := range []string{
		"replay-core_journey/envelope.envelope",
		"profile-save_read/payload.json",
		"native-apple_multimodule/apple-multimodule.dmp",
		"native-apple_multimodule/event.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
}
