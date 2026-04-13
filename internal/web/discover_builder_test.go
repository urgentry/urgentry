package web

import (
	"testing"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

func TestBuildDiscoverQuerySupportsMultipleAggregatesAndSorts(t *testing.T) {
	query, err := buildDiscoverQuery("acme", discoverBuilderState{
		Dataset:       "transactions",
		Visualization: "table",
		Aggregate:     "count, p95(duration.ms)",
		GroupBy:       "project, transaction",
		OrderBy:       "-p95, project",
		TimeRange:     "24h",
	}, 50)
	if err != nil {
		t.Fatalf("buildDiscoverQuery: %v", err)
	}
	if len(query.GroupBy) != 2 || query.GroupBy[0].Field != "project" || query.GroupBy[1].Field != "transaction" {
		t.Fatalf("unexpected group by: %+v", query.GroupBy)
	}
	if len(query.Select) != 4 {
		t.Fatalf("select count = %d, want 4", len(query.Select))
	}
	if query.Select[2].Alias != "count" || query.Select[3].Alias != "p95" {
		t.Fatalf("unexpected aggregate aliases: %+v", query.Select)
	}
	if len(query.OrderBy) != 2 || query.OrderBy[0].Expr.Alias != "p95" || query.OrderBy[0].Direction != "desc" || query.OrderBy[1].Expr.Alias != "project" {
		t.Fatalf("unexpected order by: %+v", query.OrderBy)
	}
}

func TestBuildDiscoverQueryUsesColumnsForRawTables(t *testing.T) {
	query, err := buildDiscoverQuery("acme", discoverBuilderState{
		Dataset:       "logs",
		Visualization: "table",
		Columns:       "timestamp, project, logger, message",
		OrderBy:       "-timestamp",
		TimeRange:     "24h",
	}, 50)
	if err != nil {
		t.Fatalf("buildDiscoverQuery: %v", err)
	}
	if len(query.Select) != 4 {
		t.Fatalf("select count = %d, want 4", len(query.Select))
	}
	if query.Select[0].Expr.Field != "timestamp" || query.Select[3].Expr.Field != "message" {
		t.Fatalf("unexpected raw selects: %+v", query.Select)
	}
	if len(query.OrderBy) != 1 || query.OrderBy[0].Expr.Alias != "timestamp" || query.OrderBy[0].Direction != "desc" {
		t.Fatalf("unexpected order by: %+v", query.OrderBy)
	}
}

func TestDiscoverStateFromSavedRoundTripsExpandedGrammar(t *testing.T) {
	state := discoverStateFromSaved(sqlite.SavedSearch{
		ID:         "saved-1",
		Name:       "Slow transactions",
		Visibility: sqlite.SavedSearchVisibilityOrganization,
		QueryDoc: discover.Query{
			Version: discover.CurrentVersion,
			Dataset: discover.DatasetTransactions,
			Scope: discover.Scope{
				Kind:         discover.ScopeKindOrganization,
				Organization: "acme",
			},
			Select: []discover.SelectItem{
				{Alias: "project", Expr: discover.Expression{Field: "project"}},
				{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
				{Alias: "count", Expr: discover.Expression{Call: "count"}},
				{Alias: "p95", Expr: discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}},
			},
			GroupBy: []discover.Expression{{Field: "project"}, {Field: "transaction"}},
			OrderBy: []discover.OrderBy{
				{Expr: discover.Expression{Alias: "p95"}, Direction: "desc"},
				{Expr: discover.Expression{Alias: "project"}, Direction: "asc"},
			},
			TimeRange: &discover.TimeRange{Kind: "relative", Value: "24h"},
		},
	}, "transactions", "/discover/")
	if state.Aggregate != "count, p95(duration.ms)" {
		t.Fatalf("aggregate = %q", state.Aggregate)
	}
	if state.GroupBy != "project, transaction" {
		t.Fatalf("group_by = %q", state.GroupBy)
	}
	if state.OrderBy != "-p95, project" {
		t.Fatalf("order_by = %q", state.OrderBy)
	}
	if state.Visualization != "table" {
		t.Fatalf("visualization = %q", state.Visualization)
	}
}
