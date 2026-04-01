package hosted

import "testing"

func TestDefaultCatalogValidate(t *testing.T) {
	if err := DefaultCatalog().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultCatalogLookup(t *testing.T) {
	catalog := DefaultCatalog()
	spec, ok := catalog.Lookup(PlanTeam)
	if !ok {
		t.Fatal("Lookup(team) = missing, want present")
	}
	if spec.DisplayName != "Team" {
		t.Fatalf("team display name = %q, want Team", spec.DisplayName)
	}
	if !spec.Features[FeatureAuditExport] {
		t.Fatal("team should expose audit export")
	}
}

func TestDefaultCatalogPolicies(t *testing.T) {
	catalog := DefaultCatalog()

	starter := catalog.Plans[PlanStarter]
	if got := starter.Limits[UsageDailyQueryUnits].OverageMode; got != OverageModeGraceThenBlock {
		t.Fatalf("starter query overage mode = %q, want %q", got, OverageModeGraceThenBlock)
	}
	if got := starter.Limits[UsageMaxRetentionDays].OverageMode; got != OverageModeBlock {
		t.Fatalf("starter retention overage mode = %q, want %q", got, OverageModeBlock)
	}

	business := catalog.Plans[PlanBusiness]
	if !business.Features[FeatureRegionPinning] {
		t.Fatal("business should allow region pinning")
	}
	if business.Features[FeatureCustomSSO] {
		t.Fatal("business should not allow custom SSO")
	}

	enterprise := catalog.Plans[PlanEnterprise]
	if got := enterprise.Limits[UsageMonthlyEvents].OverageMode; got != OverageModeContract {
		t.Fatalf("enterprise monthly events overage mode = %q, want %q", got, OverageModeContract)
	}
	if !enterprise.Features[FeaturePrivateConnectivity] {
		t.Fatal("enterprise should allow private connectivity")
	}

	if catalog.Trial.Plan != PlanTeam {
		t.Fatalf("trial plan = %q, want %q", catalog.Trial.Plan, PlanTeam)
	}
	if catalog.Trial.PostTrialState != PostTrialStateReadOnly {
		t.Fatalf("trial post state = %q, want %q", catalog.Trial.PostTrialState, PostTrialStateReadOnly)
	}
}
