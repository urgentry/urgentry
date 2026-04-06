package main

import (
	"path/filepath"
	"testing"
)

func TestGenerateBundleMatchesCheckedInArtifacts(t *testing.T) {
	repoRoot := defaultRepoRoot()
	bundle, err := GenerateBundle(repoRoot)
	if err != nil {
		t.Fatalf("GenerateBundle: %v", err)
	}
	if err := CheckOutputs(filepath.Join(repoRoot, ".synthetic"), bundle); err != nil {
		t.Fatalf("CheckOutputs: %v", err)
	}
}

func TestGenerateBundleCapturesCoreSurfaceFacts(t *testing.T) {
	repoRoot := defaultRepoRoot()
	bundle, err := GenerateBundle(repoRoot)
	if err != nil {
		t.Fatalf("GenerateBundle: %v", err)
	}

	if bundle.Surfaces.OpenAPI.OperationCount == 0 {
		t.Fatal("expected OpenAPI operations")
	}
	if bundle.Surfaces.RouteMatrix.RouteCount == 0 {
		t.Fatal("expected documented routes")
	}
	if bundle.Entities.TableCount == 0 || bundle.Entities.RelationCount == 0 {
		t.Fatalf("expected tables and relations, got tables=%d relations=%d", bundle.Entities.TableCount, bundle.Entities.RelationCount)
	}
	if bundle.QueryFields.DatasetCount == 0 || bundle.QueryFields.AggregateCount == 0 {
		t.Fatalf("expected query datasets and aggregates, got datasets=%d aggregates=%d", bundle.QueryFields.DatasetCount, bundle.QueryFields.AggregateCount)
	}
}
