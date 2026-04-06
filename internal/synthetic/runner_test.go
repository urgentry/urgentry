package synthetic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunnerCoreStoreRoundtripScenario(t *testing.T) {
	runner := Runner{RepoRoot: RepoRoot()}
	if err := runner.RunScenario(context.Background(), "core-store-roundtrip"); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
}

func TestRunnerQueryReadSmokeScenario(t *testing.T) {
	runner := Runner{RepoRoot: RepoRoot()}
	if err := runner.RunScenario(context.Background(), "query-read-smoke"); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
}

func TestRunnerAdminLifecycleSmokeScenario(t *testing.T) {
	runner := Runner{RepoRoot: RepoRoot()}
	if err := runner.RunScenario(context.Background(), "admin-lifecycle-smoke"); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
}

func TestRunnerPreservesBinaryBodyFiles(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "payload.bin")
	raw := []byte{0x00, 0x01, '{', '{', 0xff}
	if err := os.WriteFile(bodyPath, raw, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	body, err := loadScenarioBody(dir, Step{BodyFile: "payload.bin"}, map[string]string{})
	if err != nil {
		t.Fatalf("loadScenarioBody: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("body changed: %v != %v", body, raw)
	}
}
