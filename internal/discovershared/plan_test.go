package discovershared

import (
	"strings"
	"testing"

	"urgentry/internal/discover"
)

func TestPlanFilterNoWhereNoTimeRange(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	plan, err := PlanFilter(discover.Query{Limit: 25}, 50, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "t." + f, true },
		TimestampExpr: "t.created_at",
		DefaultOrder:  "t.created_at DESC",
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if len(plan.WhereClauses) != 0 {
		t.Fatalf("WhereClauses = %v, want empty", plan.WhereClauses)
	}
	if plan.OrderClause != "t.created_at DESC" {
		t.Fatalf("OrderClause = %q, want default", plan.OrderClause)
	}
	if plan.LimitPlaceholder != "?" {
		t.Fatalf("LimitPlaceholder = %q, want ?", plan.LimitPlaceholder)
	}
	if plan.Limit != 50 {
		t.Fatalf("Limit = %d, want 50", plan.Limit)
	}
}

func TestPlanFilterWithWhereAndTimeRange(t *testing.T) {
	t.Parallel()

	builder := &PostgresArgBuilder{}
	query := discover.Query{
		Limit: 10,
		Where: &discover.Predicate{
			Op:    "=",
			Field: "level",
			Value: "error",
		},
		TimeRange: &discover.TimeRange{
			Kind:  "absolute",
			Start: "2026-03-29T00:00:00Z",
			End:   "2026-03-29T23:59:59Z",
		},
	}
	plan, err := PlanFilter(query, 10, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "e." + f, true },
		TimestampExpr: "e.occurred_at",
		DefaultOrder:  "e.occurred_at DESC",
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if len(plan.WhereClauses) != 2 {
		t.Fatalf("WhereClauses count = %d, want 2", len(plan.WhereClauses))
	}
	// First clause is the predicate
	if !strings.Contains(plan.WhereClauses[0], "e.level") {
		t.Fatalf("WhereClauses[0] = %q, want level predicate", plan.WhereClauses[0])
	}
	// Second clause is the time range
	if !strings.Contains(plan.WhereClauses[1], "e.occurred_at") {
		t.Fatalf("WhereClauses[1] = %q, want time range", plan.WhereClauses[1])
	}
	// Postgres placeholder format
	if !strings.HasPrefix(plan.LimitPlaceholder, "$") {
		t.Fatalf("LimitPlaceholder = %q, want $N format", plan.LimitPlaceholder)
	}
}

func TestPlanFilterPredicateError(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	query := discover.Query{
		Where: &discover.Predicate{
			Op:    "=",
			Field: "unknown",
			Value: "x",
		},
	}
	_, err := PlanFilter(query, 50, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(_ string) (string, bool) { return "", false },
		TimestampExpr: "t.ts",
		DefaultOrder:  "t.ts DESC",
	})
	if err == nil {
		t.Fatal("expected error for unsupported field")
	}
}

func TestPlanFilterTimeRangeError(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	query := discover.Query{
		TimeRange: &discover.TimeRange{Kind: "bogus"},
	}
	_, err := PlanFilter(query, 50, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "t." + f, true },
		TimestampExpr: "t.ts",
		DefaultOrder:  "t.ts DESC",
	})
	if err == nil {
		t.Fatal("expected error for invalid time range")
	}
}

func TestPlanFilterClampsLimit(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	plan, err := PlanFilter(discover.Query{}, 10000, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "t." + f, true },
		TimestampExpr: "t.ts",
		DefaultOrder:  "t.ts DESC",
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if plan.Limit != 5000 {
		t.Fatalf("Limit = %d, want 5000 (clamped)", plan.Limit)
	}
}

func TestPlanFilterClampsLimitMin(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	plan, err := PlanFilter(discover.Query{}, 0, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "t." + f, true },
		TimestampExpr: "t.ts",
		DefaultOrder:  "t.ts DESC",
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if plan.Limit != 1 {
		t.Fatalf("Limit = %d, want 1 (clamped min)", plan.Limit)
	}
}

func TestPlanFilterWithOverrideAndCaseInsensitive(t *testing.T) {
	t.Parallel()

	builder := &PostgresArgBuilder{}
	query := discover.Query{
		Where: &discover.Predicate{
			Op:    "contains",
			Field: "message",
			Value: "test",
		},
	}
	plan, err := PlanFilter(query, 25, FilterPlanConfig{
		Builder:             builder,
		FieldExpr:           func(f string) (string, bool) { return "l." + f, true },
		TimestampExpr:       "l.timestamp",
		DefaultOrder:        "l.timestamp DESC",
		CaseInsensitiveLike: true,
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if !strings.Contains(plan.WhereClauses[0], "ILIKE") {
		t.Fatalf("WhereClauses[0] = %q, want ILIKE for case-insensitive", plan.WhereClauses[0])
	}
}

func TestAssembleSQLCombinesAllParts(t *testing.T) {
	t.Parallel()

	plan := FilterPlan{
		WhereClauses:     []string{"level = ?", "ts >= ?"},
		OrderClause:      "ts DESC",
		LimitPlaceholder: "?",
		Limit:            50,
	}
	sql := AssembleSQL("SELECT * FROM events", []string{"1=1"}, plan)
	want := "SELECT * FROM events WHERE 1=1 AND level = ? AND ts >= ? ORDER BY ts DESC LIMIT ?"
	if sql != want {
		t.Fatalf("AssembleSQL =\n  %q\nwant\n  %q", sql, want)
	}
}

func TestAssembleSQLNoFilterClauses(t *testing.T) {
	t.Parallel()

	plan := FilterPlan{
		OrderClause:      "id ASC",
		LimitPlaceholder: "$1",
		Limit:            10,
	}
	sql := AssembleSQL("SELECT * FROM t", []string{"org_id = $1"}, plan)
	if !strings.Contains(sql, "WHERE org_id = $1") {
		t.Fatalf("SQL = %q, want base clauses only in WHERE", sql)
	}
	if !strings.Contains(sql, "ORDER BY id ASC") {
		t.Fatalf("SQL = %q, want ORDER BY", sql)
	}
}

func TestPlanFilterOrderByResolvesFromQuery(t *testing.T) {
	t.Parallel()

	builder := &SQLiteArgBuilder{}
	query := discover.Query{
		OrderBy: []discover.OrderBy{
			{Expr: discover.Expression{Field: "timestamp"}, Direction: "asc"},
		},
	}
	plan, err := PlanFilter(query, 25, FilterPlanConfig{
		Builder:       builder,
		FieldExpr:     func(f string) (string, bool) { return "e." + f, true },
		TimestampExpr: "e.ts",
		DefaultOrder:  "e.ts DESC",
	})
	if err != nil {
		t.Fatalf("PlanFilter error = %v", err)
	}
	if plan.OrderClause != "e.timestamp ASC" {
		t.Fatalf("OrderClause = %q, want 'e.timestamp ASC'", plan.OrderClause)
	}
}
