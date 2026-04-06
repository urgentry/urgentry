package synthetic

import (
	"path/filepath"
	"testing"
)

func TestGenerateOutputsMatchesCheckedInManifests(t *testing.T) {
	repoRoot := RepoRoot()
	outputs, err := GenerateOutputs(repoRoot)
	if err != nil {
		t.Fatalf("GenerateOutputs: %v", err)
	}
	if err := CheckOutputs(filepath.Join(repoRoot, ".synthetic", "generated"), outputs); err != nil {
		t.Fatalf("CheckOutputs: %v", err)
	}
}
