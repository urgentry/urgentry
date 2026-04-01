package telemetryquery

import "testing"

func TestExecutionContractDefaultPassesValidation(t *testing.T) {
	t.Parallel()
	if err := DefaultExecutionContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestExecutionContractValidateRejectsEmptySurfaces(t *testing.T) {
	t.Parallel()
	if err := (ExecutionContract{}).Validate(); err == nil {
		t.Fatal("empty surfaces should fail")
	}
}

func TestExecutionContractValidateRejectsBadStaleBudget(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].StaleBudgetSeconds = 0
	if err := contract.Validate(); err == nil {
		t.Fatal("zero stale budget should fail")
	}
}

func TestExecutionContractValidateRejectsFailClosedLessThanStale(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].FailClosedAfterSeconds = contract.Surfaces[0].StaleBudgetSeconds
	if err := contract.Validate(); err == nil {
		t.Fatal("fail-closed == stale should fail")
	}
}

func TestExecutionContractValidateRejectsZeroConcurrency(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].MaxOrgConcurrency = 0
	if err := contract.Validate(); err == nil {
		t.Fatal("zero org concurrency should fail")
	}
}

func TestExecutionContractValidateRejectsZeroTimeout(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].TimeoutSeconds = 0
	if err := contract.Validate(); err == nil {
		t.Fatal("zero timeout should fail")
	}
}

func TestExecutionContractValidateRejectsInlinRebuilds(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].RebuildMode = RebuildMode("inline")
	if err := contract.Validate(); err == nil {
		t.Fatal("inline rebuild mode should fail")
	}
}

func TestExecutionContractValidateRejectsBadFreshnessMode(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Surfaces[0].FreshnessMode = FreshnessMode("always_fresh")
	if err := contract.Validate(); err == nil {
		t.Fatal("invalid freshness mode should fail")
	}
}

func TestExecutionContractValidateRejectsEmptyQuotaBackend(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Admission.SharedQuotaBackend = ""
	if err := contract.Validate(); err == nil {
		t.Fatal("empty quota backend should fail")
	}
}

func TestExecutionContractValidateRejectsZeroNodeConcurrency(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.Admission.MaxNodeConcurrency = 0
	if err := contract.Validate(); err == nil {
		t.Fatal("zero node concurrency should fail")
	}
}

func TestExecutionContractValidateRejectsEmptySignals(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	contract.RequiredSignals = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("empty signals should fail")
	}
}

func TestExecutionContractSurfaceLookup(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	surface, err := contract.Surface(QuerySurfaceDiscoverLogs)
	if err != nil {
		t.Fatalf("Surface() error = %v", err)
	}
	if surface.Surface != QuerySurfaceDiscoverLogs {
		t.Fatalf("Surface = %q", surface.Surface)
	}
}

func TestExecutionContractSurfaceLookupUnknown(t *testing.T) {
	t.Parallel()

	contract := DefaultExecutionContract()
	_, err := contract.Surface(QuerySurface("bogus"))
	if err == nil {
		t.Fatal("expected error for unknown surface")
	}
}

func TestSupportedQuerySurfaces(t *testing.T) {
	t.Parallel()

	surfaces := SupportedQuerySurfaces()
	if len(surfaces) != len(querySurfaceOrder) {
		t.Fatalf("SupportedQuerySurfaces() returned %d, want %d", len(surfaces), len(querySurfaceOrder))
	}
	// Verify it's a copy
	surfaces[0] = "modified"
	if querySurfaceOrder[0] == "modified" {
		t.Fatal("SupportedQuerySurfaces returned original slice, not a copy")
	}
}

func TestValidQuerySurface(t *testing.T) {
	t.Parallel()

	if !ValidQuerySurface(QuerySurfaceDiscoverLogs) {
		t.Fatal("expected discover_logs to be valid")
	}
	if ValidQuerySurface(QuerySurface("nonexistent")) {
		t.Fatal("expected nonexistent to be invalid")
	}
}

func TestClampFunction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value int
		min   int
		max   int
		want  int
	}{
		{"within range", 5, 1, 10, 5},
		{"below min", -1, 0, 10, 0},
		{"above max", 15, 0, 10, 10},
		{"at min", 0, 0, 10, 0},
		{"at max", 10, 0, 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clamp(tt.value, tt.min, tt.max); got != tt.want {
				t.Fatalf("clamp(%d, %d, %d) = %d, want %d", tt.value, tt.min, tt.max, got, tt.want)
			}
		})
	}
}
