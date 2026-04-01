package hosted

import "testing"

func TestDefaultValidationScenarios(t *testing.T) {
	scenarios := DefaultValidationScenarios()
	if got, want := len(scenarios), 3; got != want {
		t.Fatalf("len(DefaultValidationScenarios()) = %d, want %d", got, want)
	}
	for _, scenario := range scenarios {
		if _, err := EvaluateValidationScenario(DefaultCatalog(), DefaultQuotaPolicy(), scenario); err != nil {
			t.Fatalf("EvaluateValidationScenario(%q) error = %v", scenario.Name, err)
		}
	}
}

func TestEvaluateValidationScenarioKeepsTenantsIndependent(t *testing.T) {
	report, err := EvaluateValidationScenario(DefaultCatalog(), DefaultQuotaPolicy(), ValidationScenario{
		Name: "independent tenants",
		Requests: []ValidationTenantRequest{
			{
				AccountSlug: "starter-a",
				Plan:        PlanStarter,
				Request: QuotaRequest{
					Surface:        QuotaSurfaceQuery,
					UsedUnits:      25_000,
					RequestedUnits: 6_000,
				},
				Expectation: ValidationExpectation{Allowed: false},
			},
			{
				AccountSlug: "starter-b",
				Plan:        PlanStarter,
				Request: QuotaRequest{
					Surface:        QuotaSurfaceQuery,
					UsedUnits:      500,
					RequestedUnits: 500,
				},
				Expectation: ValidationExpectation{Allowed: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("EvaluateValidationScenario() error = %v", err)
	}
	if got, want := len(report.Results), 2; got != want {
		t.Fatalf("len(Results) = %d, want %d", got, want)
	}
	if report.Results[0].Decision.Allowed {
		t.Fatalf("tenant A allowed = true, want false")
	}
	if !report.Results[1].Decision.Allowed {
		t.Fatalf("tenant B allowed = false, want true")
	}
}
