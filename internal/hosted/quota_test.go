package hosted

import "testing"

func TestDefaultQuotaPolicyValidate(t *testing.T) {
	if err := DefaultQuotaPolicy().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestQuotaPolicyAllowsStarterQueryGrace(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanStarter, QuotaRequest{
		Surface:        QuotaSurfaceQuery,
		UsedUnits:      25_000,
		RequestedUnits: 4_000,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true, decision=%+v", decision)
	}
	if got, want := decision.GraceUnits, int64(4_000); got != want {
		t.Fatalf("GraceUnits = %d, want %d", got, want)
	}
	if got, want := decision.GraceDaysRemaining, 3; got != want {
		t.Fatalf("GraceDaysRemaining = %d, want %d", got, want)
	}
}

func TestQuotaPolicyBlocksStarterQueryAfterGraceCap(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanStarter, QuotaRequest{
		Surface:        QuotaSurfaceQuery,
		UsedUnits:      25_000,
		RequestedUnits: 6_000,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Allowed {
		t.Fatalf("Allowed = true, want false, decision=%+v", decision)
	}
}

func TestQuotaPolicyMetersTeamEvents(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanTeam, QuotaRequest{
		Surface:        QuotaSurfaceIngestEvents,
		UsedUnits:      10_000_000,
		RequestedUnits: 500,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true, decision=%+v", decision)
	}
	if got, want := decision.BillableUnits, int64(500); got != want {
		t.Fatalf("BillableUnits = %d, want %d", got, want)
	}
}

func TestQuotaPolicyBlocksExportJobsAtBoundary(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanTeam, QuotaRequest{
		Surface:        QuotaSurfaceExportJob,
		UsedUnits:      250,
		RequestedUnits: 1,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Allowed {
		t.Fatalf("Allowed = true, want false, decision=%+v", decision)
	}
}

func TestQuotaPolicyRequiresEnterpriseContractLimit(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanEnterprise, QuotaRequest{
		Surface:        QuotaSurfaceIngestEvents,
		UsedUnits:      1_000_000_000,
		RequestedUnits: 1,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true, decision=%+v", decision)
	}
	if !decision.RequiresContractLimit {
		t.Fatalf("RequiresContractLimit = false, want true, decision=%+v", decision)
	}
}

func TestQuotaPolicyHonorsEnterpriseContractOverride(t *testing.T) {
	decision, err := DefaultQuotaPolicy().Evaluate(DefaultCatalog(), PlanEnterprise, QuotaRequest{
		Surface:        QuotaSurfaceIngestEvents,
		UsedUnits:      100,
		RequestedUnits: 1,
		ContractLimit: &Limit{
			Included:    100,
			OverageMode: OverageModeBlock,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Allowed {
		t.Fatalf("Allowed = true, want false, decision=%+v", decision)
	}
}
