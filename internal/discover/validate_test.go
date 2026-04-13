package discover

import "testing"

func TestValidateQueryRejectsMixedAggregateWithoutGroupBy(t *testing.T) {
	_, _, err := ValidateQuery(Query{
		Version: CurrentVersion,
		Dataset: DatasetTransactions,
		Scope: Scope{
			Kind:         ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a"},
		},
		Select: []SelectItem{
			{Alias: "transaction", Expr: Expression{Field: "transaction"}},
			{Alias: "p95", Expr: Expression{Call: "p95", Args: []Expression{{Field: "duration.ms"}}}},
		},
		Limit: 20,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	errs := err.(ValidationErrors)
	if errs[0].Code != "missing_group_by" {
		t.Fatalf("unexpected errors: %+v", errs)
	}
}

func TestValidateQueryRejectsLogsOrgQueryWithoutTimeRange(t *testing.T) {
	_, cost, err := ValidateQuery(Query{
		Version: CurrentVersion,
		Dataset: DatasetLogs,
		Scope: Scope{
			Kind:         ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []SelectItem{{Alias: "count", Expr: Expression{Call: "count"}}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cost.Class != CostClassReject {
		t.Fatalf("cost class = %s, want reject", cost.Class)
	}
}

func TestUnmarshalQueryRejectsUnknownFields(t *testing.T) {
	_, _, err := UnmarshalQuery([]byte(`{"version":1,"dataset":"issues","scope":{"kind":"organization","organization":"acme"},"nope":true}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateQueryAcceptsGroupedTransactionAggregate(t *testing.T) {
	query, cost, err := ValidateQuery(Query{
		Version: CurrentVersion,
		Dataset: DatasetTransactions,
		Scope: Scope{
			Kind:         ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-b", "proj-a"},
		},
		Select: []SelectItem{
			{Alias: "transaction", Expr: Expression{Field: "transaction"}},
			{Alias: "p95", Expr: Expression{Call: "p95", Args: []Expression{{Field: "duration.ms"}}}},
		},
		GroupBy: []Expression{{Field: "transaction"}},
		OrderBy: []OrderBy{{Expr: Expression{Alias: "p95"}, Direction: "desc"}},
		TimeRange: &TimeRange{
			Kind:  "relative",
			Value: "7d",
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("ValidateQuery: %v", err)
	}
	if cost.Class == CostClassReject {
		t.Fatalf("unexpected cost class: %s", cost.Class)
	}
	if len(query.Scope.ProjectIDs) != 2 || query.Scope.ProjectIDs[0] != "proj-a" {
		t.Fatalf("project ids not normalized: %+v", query.Scope.ProjectIDs)
	}
}

func TestValidateQueryAcceptsMultipleAggregatesAndAliasOrdering(t *testing.T) {
	_, _, err := ValidateQuery(Query{
		Version: CurrentVersion,
		Dataset: DatasetTransactions,
		Scope: Scope{
			Kind:         ScopeKindOrganization,
			Organization: "acme",
		},
		Select: []SelectItem{
			{Alias: "project", Expr: Expression{Field: "project"}},
			{Alias: "transaction", Expr: Expression{Field: "transaction"}},
			{Alias: "count", Expr: Expression{Call: "count"}},
			{Alias: "p95", Expr: Expression{Call: "p95", Args: []Expression{{Field: "duration.ms"}}}},
		},
		GroupBy: []Expression{{Field: "project"}, {Field: "transaction"}},
		OrderBy: []OrderBy{
			{Expr: Expression{Alias: "p95"}, Direction: "desc"},
			{Expr: Expression{Alias: "project"}, Direction: "asc"},
		},
		TimeRange: &TimeRange{Kind: "relative", Value: "24h"},
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("ValidateQuery: %v", err)
	}
}
