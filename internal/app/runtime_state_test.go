package app

import (
	"testing"

	"urgentry/internal/config"
)

func TestEffectivePipelineWorkersDefaultsByDeployment(t *testing.T) {
	t.Parallel()

	if got := effectivePipelineWorkers(config.Config{AppendOnlyIngest: true}, deploymentModeTiny); got != 1 {
		t.Fatalf("tiny workers = %d, want 1", got)
	}
	if got := effectivePipelineWorkers(config.Config{}, deploymentModeSeriousSelfHosted); got != 4 {
		t.Fatalf("self-hosted workers = %d, want 4", got)
	}
	if got := effectivePipelineWorkers(config.Config{PipelineWorkers: 2}, deploymentModeSeriousSelfHosted); got != 2 {
		t.Fatalf("explicit workers = %d, want 2", got)
	}
}
