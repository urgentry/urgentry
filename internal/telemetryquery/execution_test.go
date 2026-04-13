package telemetryquery

import "testing"

func TestDefaultExecutionContractValidate(t *testing.T) {
	if err := DefaultExecutionContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultExecutionContractDisablesInlineRebuilds(t *testing.T) {
	contract := DefaultExecutionContract()
	for _, surface := range SupportedQuerySurfaces() {
		item, err := contract.Surface(surface)
		if err != nil {
			t.Fatalf("Surface(%q) error = %v", surface, err)
		}
		if got, want := item.RebuildMode, RebuildModeExplicitOnly; got != want {
			t.Fatalf("Surface(%q).RebuildMode = %q, want %q", surface, got, want)
		}
	}
}

func TestDefaultExecutionContractProfilesAreTighterThanDiscover(t *testing.T) {
	contract := DefaultExecutionContract()
	profiles, err := contract.Surface(QuerySurfaceProfiles)
	if err != nil {
		t.Fatalf("Surface(profiles) error = %v", err)
	}
	discover, err := contract.Surface(QuerySurfaceDiscoverTransactions)
	if err != nil {
		t.Fatalf("Surface(discover_transactions) error = %v", err)
	}
	if profiles.MaxOrgConcurrency >= discover.MaxOrgConcurrency {
		t.Fatalf("profiles concurrency = %d, want tighter than discover %d", profiles.MaxOrgConcurrency, discover.MaxOrgConcurrency)
	}
	if profiles.TimeoutSeconds < discover.TimeoutSeconds {
		t.Fatalf("profiles timeout = %d, want at least discover timeout %d", profiles.TimeoutSeconds, discover.TimeoutSeconds)
	}
}
