package hosted

import "fmt"

type ValidationExpectation struct {
	Allowed               bool  `json:"allowed"`
	BillableUnits         int64 `json:"billableUnits,omitempty"`
	RequiresContractLimit bool  `json:"requiresContractLimit,omitempty"`
}

type ValidationTenantRequest struct {
	AccountSlug string                `json:"accountSlug"`
	Plan        Plan                  `json:"plan"`
	Request     QuotaRequest          `json:"request"`
	Expectation ValidationExpectation `json:"expectation"`
}

type ValidationScenario struct {
	Name     string                    `json:"name"`
	Requests []ValidationTenantRequest `json:"requests"`
}

type ValidationResult struct {
	AccountSlug string                `json:"accountSlug"`
	Decision    QuotaDecision         `json:"decision"`
	Expectation ValidationExpectation `json:"expectation"`
}

type ValidationReport struct {
	Name    string             `json:"name"`
	Results []ValidationResult `json:"results"`
}

func DefaultValidationScenarios() []ValidationScenario {
	return []ValidationScenario{
		{
			Name: "starter noisy neighbor",
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
						UsedUnits:      1_000,
						RequestedUnits: 2_000,
					},
					Expectation: ValidationExpectation{Allowed: true},
				},
			},
		},
		{
			Name: "team metered overage",
			Requests: []ValidationTenantRequest{
				{
					AccountSlug: "team-a",
					Plan:        PlanTeam,
					Request: QuotaRequest{
						Surface:        QuotaSurfaceIngestEvents,
						UsedUnits:      10_000_000,
						RequestedUnits: 750_000,
					},
					Expectation: ValidationExpectation{Allowed: true, BillableUnits: 750_000},
				},
			},
		},
		{
			Name: "enterprise contract required",
			Requests: []ValidationTenantRequest{
				{
					AccountSlug: "enterprise-a",
					Plan:        PlanEnterprise,
					Request: QuotaRequest{
						Surface:        QuotaSurfaceIngestEvents,
						UsedUnits:      1_000_000_000,
						RequestedUnits: 1,
					},
					Expectation: ValidationExpectation{Allowed: true, RequiresContractLimit: true},
				},
			},
		},
	}
}

func EvaluateValidationScenario(catalog Catalog, policy QuotaPolicy, scenario ValidationScenario) (*ValidationReport, error) {
	if scenario.Name == "" {
		return nil, fmt.Errorf("scenario name is required")
	}
	if len(scenario.Requests) == 0 {
		return nil, fmt.Errorf("scenario must define at least one request")
	}
	report := &ValidationReport{
		Name:    scenario.Name,
		Results: make([]ValidationResult, 0, len(scenario.Requests)),
	}
	for i, item := range scenario.Requests {
		if item.AccountSlug == "" {
			return nil, fmt.Errorf("request %d account slug is required", i)
		}
		decision, err := policy.Evaluate(catalog, item.Plan, item.Request)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		if decision.Allowed != item.Expectation.Allowed {
			return nil, fmt.Errorf("request %d allowed = %t, want %t", i, decision.Allowed, item.Expectation.Allowed)
		}
		if decision.BillableUnits != item.Expectation.BillableUnits {
			return nil, fmt.Errorf("request %d billable units = %d, want %d", i, decision.BillableUnits, item.Expectation.BillableUnits)
		}
		if decision.RequiresContractLimit != item.Expectation.RequiresContractLimit {
			return nil, fmt.Errorf("request %d requires contract = %t, want %t", i, decision.RequiresContractLimit, item.Expectation.RequiresContractLimit)
		}
		report.Results = append(report.Results, ValidationResult{
			AccountSlug: item.AccountSlug,
			Decision:    decision,
			Expectation: item.Expectation,
		})
	}
	return report, nil
}
