package telemetrybridge

import (
	"context"
	"testing"
)

func TestNormalizeFamiliesDefaultOrder(t *testing.T) {
	t.Parallel()

	families := normalizeFamilies(nil)
	if len(families) != len(projectorFamilyOrder) {
		t.Fatalf("normalizeFamilies(nil) returned %d families, want %d", len(families), len(projectorFamilyOrder))
	}
	for i, f := range projectorFamilyOrder {
		if families[i] != f {
			t.Fatalf("families[%d] = %q, want %q", i, families[i], f)
		}
	}
}

func TestNormalizeFamiliesDeduplicates(t *testing.T) {
	t.Parallel()

	families := normalizeFamilies([]Family{FamilyEvents, FamilyLogs, FamilyEvents})
	if len(families) != 2 {
		t.Fatalf("normalizeFamilies with duplicates returned %d, want 2", len(families))
	}
	if families[0] != FamilyEvents || families[1] != FamilyLogs {
		t.Fatalf("families = %v", families)
	}
}

func TestNormalizeFamiliesSkipsEmpty(t *testing.T) {
	t.Parallel()

	families := normalizeFamilies([]Family{FamilyEvents, "", FamilyLogs})
	if len(families) != 2 {
		t.Fatalf("normalizeFamilies with empty returned %d, want 2", len(families))
	}
}

func TestNormalizeFamiliesEmptySlice(t *testing.T) {
	t.Parallel()

	families := normalizeFamilies([]Family{})
	if len(families) != len(projectorFamilyOrder) {
		t.Fatalf("normalizeFamilies([]) returned %d, want full set of %d", len(families), len(projectorFamilyOrder))
	}
}

func TestCursorName(t *testing.T) {
	t.Parallel()

	// Project scope
	got := cursorName(Scope{OrganizationID: "org-1", ProjectID: "proj-1"}, FamilyEvents)
	if got != "events:project:proj-1" {
		t.Fatalf("cursorName(project) = %q", got)
	}

	// Organization scope
	got = cursorName(Scope{OrganizationID: "org-1"}, FamilyTransactions)
	if got != "transactions:organization:org-1" {
		t.Fatalf("cursorName(org) = %q", got)
	}
}

func TestScopeKindAndID(t *testing.T) {
	t.Parallel()

	kind, id := scopeKindAndID(Scope{OrganizationID: "org-1", ProjectID: "proj-1"})
	if kind != "project" || id != "proj-1" {
		t.Fatalf("scopeKindAndID(project) = %q, %q", kind, id)
	}

	kind, id = scopeKindAndID(Scope{OrganizationID: "org-1"})
	if kind != "organization" || id != "org-1" {
		t.Fatalf("scopeKindAndID(org) = %q, %q", kind, id)
	}

	kind, id = scopeKindAndID(Scope{OrganizationID: "org-1", ProjectID: "  "})
	if kind != "organization" || id != "org-1" {
		t.Fatalf("scopeKindAndID(whitespace project) = %q, %q", kind, id)
	}
}

func TestScopeCacheKey(t *testing.T) {
	t.Parallel()

	got := scopeCacheKey(Scope{OrganizationID: "org-1", ProjectID: "proj-1"})
	if got != "project:proj-1" {
		t.Fatalf("scopeCacheKey = %q", got)
	}
}

func TestScopeStepKey(t *testing.T) {
	t.Parallel()

	got := scopeStepKey(Scope{OrganizationID: "org-1"}, []Family{FamilyEvents, FamilyLogs})
	if got != "organization:org-1|events,logs" {
		t.Fatalf("scopeStepKey = %q", got)
	}
}

func TestBridgeScopeArgs(t *testing.T) {
	t.Parallel()

	clause, args := bridgeScopeArgs(Scope{ProjectID: "proj-1", OrganizationID: "org-1"})
	if clause != "project_id = $1" {
		t.Fatalf("bridgeScopeArgs(project) clause = %q", clause)
	}
	if len(args) != 1 || args[0] != "proj-1" {
		t.Fatalf("bridgeScopeArgs(project) args = %v", args)
	}

	clause, args = bridgeScopeArgs(Scope{OrganizationID: "org-1"})
	if clause != "organization_id = $1" {
		t.Fatalf("bridgeScopeArgs(org) clause = %q", clause)
	}
	if len(args) != 1 || args[0] != "org-1" {
		t.Fatalf("bridgeScopeArgs(org) args = %v", args)
	}
}

func TestDefaultProjectorFamiliesCoversAllFamilies(t *testing.T) {
	t.Parallel()

	families := defaultProjectorFamilies()
	for _, f := range projectorFamilyOrder {
		if _, ok := families[f]; !ok {
			t.Fatalf("defaultProjectorFamilies missing %q", f)
		}
	}
}

func TestNewProjectorInitializesCorrectly(t *testing.T) {
	t.Parallel()

	p := NewProjector(nil, nil)
	if p.batchSize != 128 {
		t.Fatalf("batchSize = %d, want 128", p.batchSize)
	}
	if p.cursorMap == nil {
		t.Fatal("cursorMap is nil")
	}
	if p.stepMap == nil {
		t.Fatal("stepMap is nil")
	}
	if len(p.families) != len(projectorFamilyOrder) {
		t.Fatalf("families count = %d, want %d", len(p.families), len(projectorFamilyOrder))
	}
}

func TestProjectorSyncFamilyBatchNilProjector(t *testing.T) {
	t.Parallel()

	// Nil projector should return done without error
	var p *Projector
	result, err := p.syncFamilyBatch(context.TODO(), Scope{}, FamilyEvents)
	if err != nil {
		t.Fatalf("syncFamilyBatch(nil) error = %v", err)
	}
	if !result.Done {
		t.Fatal("syncFamilyBatch(nil) should be done")
	}
}

func TestProjectorSyncFamilyBatchNilDBs(t *testing.T) {
	t.Parallel()

	p := &Projector{}
	result, err := p.syncFamilyBatch(context.TODO(), Scope{}, FamilyEvents)
	if err != nil {
		t.Fatalf("syncFamilyBatch(nil dbs) error = %v", err)
	}
	if !result.Done {
		t.Fatal("syncFamilyBatch(nil dbs) should be done")
	}
}

func TestProjectorFamilyDescriptorErrors(t *testing.T) {
	t.Parallel()

	p := NewProjector(nil, nil)
	_, err := p.familyDescriptor(Family("nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent family")
	}
}

func TestProjectorAssessFreshnessNilProjector(t *testing.T) {
	t.Parallel()

	p := &Projector{}
	items, err := p.AssessFreshness(context.TODO(), Scope{}, FamilyEvents)
	if err != nil {
		t.Fatalf("AssessFreshness(nil) error = %v", err)
	}
	if items != nil {
		t.Fatalf("AssessFreshness(nil) = %v, want nil", items)
	}
}

func TestFamilyConstants(t *testing.T) {
	t.Parallel()

	expected := []Family{
		FamilyEvents,
		FamilyLogs,
		FamilyTransactions,
		FamilySpans,
		FamilyOutcomes,
		FamilyReplays,
		FamilyReplayTimeline,
		FamilyProfiles,
	}
	if len(expected) != len(projectorFamilyOrder) {
		t.Fatalf("family constants count mismatch")
	}
	for i, f := range expected {
		if projectorFamilyOrder[i] != f {
			t.Fatalf("projectorFamilyOrder[%d] = %q, want %q", i, projectorFamilyOrder[i], f)
		}
	}
}

func TestBackendConstants(t *testing.T) {
	t.Parallel()

	if BackendPostgres != "postgres" {
		t.Fatalf("BackendPostgres = %q", BackendPostgres)
	}
	if BackendTimescale != "timescale" {
		t.Fatalf("BackendTimescale = %q", BackendTimescale)
	}
}

func TestApplyNilExecutorReturnsError(t *testing.T) {
	t.Parallel()

	err := Apply(context.TODO(), nil, BackendPostgres)
	if err == nil {
		t.Fatal("Apply(nil) should return error")
	}
}
