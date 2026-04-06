package synthetic

import (
	"context"
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
