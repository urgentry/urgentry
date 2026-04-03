package discovershared

import (
	"fmt"
	"strings"
	"testing"

	"urgentry/internal/discover"
)

type fakeArgBuilder struct {
	args []any
}

func (b *fakeArgBuilder) Add(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *fakeArgBuilder) AddAll(values []string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		b.args = append(b.args, v)
		parts = append(parts, fmt.Sprintf("$%d", len(b.args)))
	}
	return strings.Join(parts, ", ")
}

func TestCompilePredicateEqualOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "=",
		Field: "level",
		Value: "error",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if sql != "e.level = $1" {
		t.Fatalf("sql = %q, want e.level = $1", sql)
	}
	if len(builder.args) != 1 || builder.args[0] != "error" {
		t.Fatalf("args = %v", builder.args)
	}
}

func TestCompilePredicateNotEqualOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "!=",
		Field: "level",
		Value: "info",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if sql != "e.level != $1" {
		t.Fatalf("sql = %q", sql)
	}
}

func TestCompilePredicateContainsOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "contains",
		Field: "message",
		Value: "test%val",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if !strings.Contains(sql, "LIKE") {
		t.Fatalf("sql = %q, want LIKE", sql)
	}
	if !strings.Contains(sql, "ESCAPE") {
		t.Fatalf("sql = %q, want ESCAPE clause", sql)
	}
	// The value should have % escaped
	arg := builder.args[0].(string)
	if !strings.Contains(arg, `\%`) {
		t.Fatalf("arg = %q, want escaped %%", arg)
	}
}

func TestCompilePredicateContainsWithCaseInsensitive(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:             builder,
		FieldExpr:           func(f string) (string, bool) { return "e." + f, true },
		CaseInsensitiveLike: true,
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "contains",
		Field: "message",
		Value: "test",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if !strings.Contains(sql, "ILIKE") {
		t.Fatalf("sql = %q, want ILIKE for case-insensitive", sql)
	}
}

func TestCompilePredicatePrefixOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "prefix",
		Field: "message",
		Value: "start",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if !strings.Contains(sql, "LIKE") {
		t.Fatalf("sql = %q, want LIKE", sql)
	}
}

func TestCompilePredicateComparisonOps(t *testing.T) {
	t.Parallel()

	ops := []struct {
		op   string
		want string
	}{
		{">", "e.duration > $1"},
		{">=", "e.duration >= $1"},
		{"<", "e.duration < $1"},
		{"<=", "e.duration <= $1"},
	}
	for _, tt := range ops {
		t.Run(tt.op, func(t *testing.T) {
			builder := &fakeArgBuilder{}
			dialect := PredicateDialect{
				Builder:   builder,
				FieldExpr: func(f string) (string, bool) { return "e." + f, true },
			}
			sql, err := CompilePredicate(discover.Predicate{
				Op:    tt.op,
				Field: "duration",
				Value: "100",
			}, dialect)
			if err != nil {
				t.Fatalf("CompilePredicate error = %v", err)
			}
			if sql != tt.want {
				t.Fatalf("sql = %q, want %q", sql, tt.want)
			}
		})
	}
}

func TestCompilePredicateNullOps(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "is_null",
		Field: "environment",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if sql != "e.environment = ''" {
		t.Fatalf("sql = %q", sql)
	}

	sql, err = CompilePredicate(discover.Predicate{
		Op:    "not_null",
		Field: "environment",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if sql != "e.environment != ''" {
		t.Fatalf("sql = %q", sql)
	}
}

func TestCompilePredicateInOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op:     "in",
		Field:  "level",
		Values: []string{"error", "warning"},
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate error = %v", err)
	}
	if !strings.Contains(sql, "IN") {
		t.Fatalf("sql = %q, want IN clause", sql)
	}
}

func TestCompilePredicateAndOr(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}

	sql, err := CompilePredicate(discover.Predicate{
		Op: "and",
		Args: []discover.Predicate{
			{Op: "=", Field: "level", Value: "error"},
			{Op: "=", Field: "platform", Value: "go"},
		},
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate AND error = %v", err)
	}
	if !strings.Contains(sql, "AND") {
		t.Fatalf("sql = %q, want AND", sql)
	}
	if strings.Count(sql, "$") != 2 {
		t.Fatalf("sql = %q, want 2 params", sql)
	}

	builder = &fakeArgBuilder{}
	dialect.Builder = builder
	sql, err = CompilePredicate(discover.Predicate{
		Op: "or",
		Args: []discover.Predicate{
			{Op: "=", Field: "level", Value: "error"},
			{Op: "=", Field: "level", Value: "warning"},
		},
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate OR error = %v", err)
	}
	if !strings.Contains(sql, "OR") {
		t.Fatalf("sql = %q, want OR", sql)
	}
}

func TestCompilePredicateNot(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	sql, err := CompilePredicate(discover.Predicate{
		Op: "not",
		Args: []discover.Predicate{
			{Op: "=", Field: "level", Value: "info"},
		},
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate NOT error = %v", err)
	}
	if !strings.HasPrefix(sql, "NOT (") {
		t.Fatalf("sql = %q, want NOT (...)", sql)
	}
}

func TestCompilePredicateUnsupportedField(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(_ string) (string, bool) { return "", false },
	}
	_, err := CompilePredicate(discover.Predicate{
		Op:    "=",
		Field: "bad_field",
		Value: "x",
	}, dialect)
	if err == nil {
		t.Fatal("expected error for unsupported field")
	}
	if !strings.Contains(err.Error(), "unsupported discover field") {
		t.Fatalf("error = %v, want unsupported field message", err)
	}
}

func TestCompilePredicateUnsupportedOp(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
	}
	_, err := CompilePredicate(discover.Predicate{
		Op:    "regex",
		Field: "level",
		Value: ".*",
	}, dialect)
	if err == nil {
		t.Fatal("expected error for unsupported op")
	}
	if !strings.Contains(err.Error(), "unsupported predicate op") {
		t.Fatalf("error = %v, want unsupported op message", err)
	}
}

func TestCompilePredicateOverrideTakesPrecedence(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
		Override: func(pred discover.Predicate) (string, bool, error) {
			if pred.Field == "special" {
				return "custom_sql = 1", true, nil
			}
			return "", false, nil
		},
	}

	// Override handles it
	sql, err := CompilePredicate(discover.Predicate{
		Op:    "=",
		Field: "special",
		Value: "x",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate override error = %v", err)
	}
	if sql != "custom_sql = 1" {
		t.Fatalf("sql = %q, want custom_sql = 1", sql)
	}

	// Non-special still uses normal path
	sql, err = CompilePredicate(discover.Predicate{
		Op:    "=",
		Field: "level",
		Value: "error",
	}, dialect)
	if err != nil {
		t.Fatalf("CompilePredicate fallthrough error = %v", err)
	}
	if sql != "e.level = $1" {
		t.Fatalf("sql = %q, want e.level = $1", sql)
	}
}

func TestCompilePredicateOverrideError(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	dialect := PredicateDialect{
		Builder:   builder,
		FieldExpr: func(f string) (string, bool) { return "e." + f, true },
		Override: func(_ discover.Predicate) (string, bool, error) {
			return "", false, fmt.Errorf("override error")
		},
	}
	_, err := CompilePredicate(discover.Predicate{
		Op:    "=",
		Field: "level",
		Value: "error",
	}, dialect)
	if err == nil {
		t.Fatal("expected override error")
	}
}

func TestTimeRangeClauseAbsolute(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	sql, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind:  "absolute",
		Start: "2026-03-29T00:00:00Z",
		End:   "2026-03-29T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("TimeRangeClause error = %v", err)
	}
	if !strings.Contains(sql, ">=") || !strings.Contains(sql, "<=") {
		t.Fatalf("sql = %q, want >= and <=", sql)
	}
	if len(builder.args) != 2 {
		t.Fatalf("args = %v, want 2 entries", builder.args)
	}
}

func TestTimeRangeClauseRelative(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	sql, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind:  "relative",
		Value: "5m",
	})
	if err != nil {
		t.Fatalf("TimeRangeClause error = %v", err)
	}
	if !strings.Contains(sql, ">=") {
		t.Fatalf("sql = %q, want >=", sql)
	}
}

func TestTimeRangeClauseInvalidKind(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	_, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind: "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unsupported time range kind")
	}
}

func TestTimeRangeClauseInvalidAbsoluteStart(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	_, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind:  "absolute",
		Start: "not-a-date",
		End:   "2026-03-29T23:59:59Z",
	})
	if err == nil {
		t.Fatal("expected error for invalid start date")
	}
}

func TestTimeRangeClauseInvalidAbsoluteEnd(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	_, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind:  "absolute",
		Start: "2026-03-29T00:00:00Z",
		End:   "not-a-date",
	})
	if err == nil {
		t.Fatal("expected error for invalid end date")
	}
}

func TestTimeRangeClauseInvalidRelativeValue(t *testing.T) {
	t.Parallel()

	builder := &fakeArgBuilder{}
	_, err := TimeRangeClause(builder, "e.occurred_at", discover.TimeRange{
		Kind:  "relative",
		Value: "bad",
	})
	if err == nil {
		t.Fatal("expected error for invalid relative range")
	}
}
