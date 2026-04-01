package discovershared

import (
	"strings"

	"urgentry/internal/discover"
)

// FilterPlanConfig describes backend-specific parameters for the shared
// filter-planning pipeline that both the SQLite and bridge discover paths use.
type FilterPlanConfig struct {
	Builder       ArgBuilder
	FieldExpr     func(string) (string, bool)
	TimestampExpr string // SQL expression for the timestamp column, e.g. "g.last_seen"
	DefaultOrder  string // fallback ORDER BY, e.g. "g.last_seen DESC"

	// Optional bridge-style overrides (nil for SQLite).
	Override            PredicateOverride
	CaseInsensitiveLike bool
}

// FilterPlan is the result of PlanFilter: the WHERE-clause additions,
// ORDER BY clause, and LIMIT placeholder that both backends assemble
// into their final SQL.
type FilterPlan struct {
	// WhereClauses are additional clauses to AND into the WHERE block.
	// Callers already have scope/base clauses; these are the predicate +
	// time-range additions.
	WhereClauses []string

	// OrderClause is the fully-resolved ORDER BY expression (without
	// the "ORDER BY" keyword).
	OrderClause string

	// LimitPlaceholder is the parameterised limit placeholder (e.g. "?"
	// for SQLite or "$3" for Postgres).
	LimitPlaceholder string

	// Limit is the resolved integer limit passed to the query.
	Limit int
}

// PlanFilter applies the shared discover-query filtering pipeline:
//
//  1. Compile the Where predicate (if any)
//  2. Compile the TimeRange clause (if any)
//  3. Resolve the ORDER BY clause
//  4. Clamp and bind the LIMIT parameter
//
// The returned FilterPlan is assembled into backend-specific SQL by
// the caller, keeping SQL generation itself per-backend.
func PlanFilter(query discover.Query, limit int, cfg FilterPlanConfig) (FilterPlan, error) {
	var plan FilterPlan
	plan.Limit = Clamp(limit, 1, 5000)

	// 1. Where predicate
	if query.Where != nil {
		whereSQL, err := CompilePredicate(*query.Where, PredicateDialect{
			Builder:             cfg.Builder,
			FieldExpr:           cfg.FieldExpr,
			Override:            cfg.Override,
			CaseInsensitiveLike: cfg.CaseInsensitiveLike,
		})
		if err != nil {
			return FilterPlan{}, err
		}
		plan.WhereClauses = append(plan.WhereClauses, whereSQL)
	}

	// 2. Time range
	if query.TimeRange != nil {
		clause, err := TimeRangeClause(cfg.Builder, cfg.TimestampExpr, *query.TimeRange)
		if err != nil {
			return FilterPlan{}, err
		}
		plan.WhereClauses = append(plan.WhereClauses, clause)
	}

	// 3. Order
	plan.OrderClause = RawOrderClause(query, cfg.DefaultOrder, cfg.FieldExpr)

	// 4. Limit
	plan.LimitPlaceholder = cfg.Builder.Add(plan.Limit)

	return plan, nil
}

// AssembleSQL combines a base SELECT, initial WHERE clauses, and the
// FilterPlan into a complete SQL statement. This is the final assembly
// step shared by both backends.
func AssembleSQL(base string, baseClauses []string, plan FilterPlan) string {
	allClauses := make([]string, 0, len(baseClauses)+len(plan.WhereClauses))
	allClauses = append(allClauses, baseClauses...)
	allClauses = append(allClauses, plan.WhereClauses...)
	return base + ` WHERE ` + strings.Join(allClauses, ` AND `) +
		` ORDER BY ` + plan.OrderClause +
		` LIMIT ` + plan.LimitPlaceholder
}
