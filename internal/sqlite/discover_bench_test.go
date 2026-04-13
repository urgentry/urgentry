package sqlite

import (
	"context"
	"testing"

	"urgentry/internal/discover"
	"urgentry/internal/discoverharness"
)

func BenchmarkDiscoverEngineTransactionsAggregate(b *testing.B) {
	db := openStoreTestDB(b)
	seedDiscoverEngineTestData(b, db)
	engine := NewDiscoverEngine(db)
	query := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetTransactions,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
			ProjectIDs:   []string{"proj-a", "proj-b"},
		},
		Select: []discover.SelectItem{
			{Alias: "transaction", Expr: discover.Expression{Field: "transaction"}},
			{Alias: "p95", Expr: discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
		GroupBy: []discover.Expression{{Field: "transaction"}},
		OrderBy: []discover.OrderBy{{Expr: discover.Expression{Alias: "p95"}, Direction: "desc"}},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T10:00:00Z",
			End:   "2026-03-29T13:00:00Z",
		},
		Limit: 10,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.ExecuteTable(context.Background(), query); err != nil {
			b.Fatalf("ExecuteTable: %v", err)
		}
	}
}

func BenchmarkDiscoverEngineIssuesSearch(b *testing.B) {
	db := openStoreTestDB(b)
	seedDiscoverEngineTestData(b, db)
	engine := NewDiscoverEngine(db)
	query := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetIssues,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "acme",
		},
		Where: &discover.Predicate{
			Op:    "=",
			Field: "status",
			Value: "unresolved",
		},
		Limit: 25,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.ExecuteTable(context.Background(), query); err != nil {
			b.Fatalf("ExecuteTable: %v", err)
		}
	}
}

func BenchmarkDiscoverHarnessCorpus(b *testing.B) {
	cases, err := discoverharness.LoadCases()
	if err != nil {
		b.Fatalf("LoadCases: %v", err)
	}
	for _, item := range cases {
		if !item.Benchmark {
			continue
		}
		b.Run(item.Name, func(b *testing.B) {
			db := openStoreTestDB(b)
			seedDiscoverEngineTestData(b, db)
			engine := NewDiscoverEngine(db)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch item.Mode {
				case "series":
					result, err := engine.ExecuteSeries(context.Background(), item.Query)
					if err != nil {
						b.Fatalf("ExecuteSeries: %v", err)
					}
					b.ReportMetric(float64(len(result.Points)), "points")
					b.ReportMetric(float64(discoverharness.SnapshotSize(discoverharness.SnapshotSeries(result))), "result_bytes")
				default:
					result, err := engine.ExecuteTable(context.Background(), item.Query)
					if err != nil {
						b.Fatalf("ExecuteTable: %v", err)
					}
					b.ReportMetric(float64(len(result.Rows)), "rows")
					b.ReportMetric(float64(discoverharness.SnapshotSize(discoverharness.SnapshotTable(result))), "result_bytes")
				}
			}
		})
	}
}
