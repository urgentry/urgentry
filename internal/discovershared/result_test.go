package discovershared

import (
	"math"
	"testing"
	"time"

	"urgentry/internal/discover"
)

func TestSelectNamePrefersAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item discover.SelectItem
		want string
	}{
		{"alias only", discover.SelectItem{Alias: "my_alias", Expr: discover.Expression{Field: "field"}}, "my_alias"},
		{"field only", discover.SelectItem{Expr: discover.Expression{Field: "Duration.Ms"}}, "duration.ms"},
		{"call only", discover.SelectItem{Expr: discover.Expression{Call: "Count"}}, "count"},
		{"alias with spaces", discover.SelectItem{Alias: "  my_alias  ", Expr: discover.Expression{Field: "field"}}, "my_alias"},
		{"empty alias falls to field", discover.SelectItem{Alias: "  ", Expr: discover.Expression{Field: "event.id"}}, "event.id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SelectName(tt.item); got != tt.want {
				t.Fatalf("SelectName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectColumnsBuildsFromQuerySelect(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Select: []discover.SelectItem{
			{Alias: "id", Expr: discover.Expression{Field: "event.id"}},
			{Expr: discover.Expression{Call: "count"}},
		},
	}
	cols := SelectColumns(query)
	if len(cols) != 2 {
		t.Fatalf("column count = %d, want 2", len(cols))
	}
	if cols[0].Name != "id" || cols[1].Name != "count" {
		t.Fatalf("columns = %v", cols)
	}
}

func TestDefaultColumnsForDatasets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dataset  discover.Dataset
		contains string
	}{
		{discover.DatasetIssues, "issue.id"},
		{discover.DatasetLogs, "message"},
		{discover.DatasetTransactions, "duration.ms"},
		{discover.Dataset("custom"), "duration.ms"}, // default case
	}
	for _, tt := range tests {
		t.Run(string(tt.dataset), func(t *testing.T) {
			cols := DefaultColumns(tt.dataset)
			found := false
			for _, col := range cols {
				if col.Name == tt.contains {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("DefaultColumns(%q) missing %q", tt.dataset, tt.contains)
			}
		})
	}
}

func TestUsesAggregateDetectsCallExpressions(t *testing.T) {
	t.Parallel()

	noAgg := discover.Query{
		Select: []discover.SelectItem{
			{Expr: discover.Expression{Field: "event.id"}},
		},
	}
	if UsesAggregate(noAgg) {
		t.Fatal("UsesAggregate = true for non-aggregate query")
	}

	withAgg := discover.Query{
		Select: []discover.SelectItem{
			{Expr: discover.Expression{Call: "count"}},
		},
	}
	if !UsesAggregate(withAgg) {
		t.Fatal("UsesAggregate = false for aggregate query")
	}

	empty := discover.Query{}
	if UsesAggregate(empty) {
		t.Fatal("UsesAggregate = true for empty query")
	}
}

func TestScanLimitDefaultsAndClamping(t *testing.T) {
	t.Parallel()

	// Default limit when none specified
	q := discover.Query{}
	if got := ScanLimit(q); got != 50 {
		t.Fatalf("ScanLimit(default) = %d, want 50", got)
	}

	// Explicit limit
	q = discover.Query{Limit: 10}
	if got := ScanLimit(q); got != 10 {
		t.Fatalf("ScanLimit(10) = %d, want 10", got)
	}

	// Aggregate queries get inflated
	q = discover.Query{
		Limit:  10,
		Select: []discover.SelectItem{{Expr: discover.Expression{Call: "count"}}},
	}
	got := ScanLimit(q)
	if got < 200 || got > 5000 {
		t.Fatalf("ScanLimit(agg) = %d, want 200..5000", got)
	}

	// GroupBy also inflates
	q = discover.Query{
		Limit:   5,
		GroupBy: []discover.Expression{{Field: "level"}},
	}
	got = ScanLimit(q)
	if got < 100 || got > 5000 {
		t.Fatalf("ScanLimit(groupby) = %d, want inflated", got)
	}

	// Max clamp at 5000
	q = discover.Query{
		Limit:  10000,
		Select: []discover.SelectItem{{Expr: discover.Expression{Call: "count"}}},
	}
	if got := ScanLimit(q); got != 5000 {
		t.Fatalf("ScanLimit(huge) = %d, want 5000", got)
	}
}

func TestParseDiscoverInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want time.Duration
		err  bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"2d", 48 * time.Hour, false},
		{"30", 30 * time.Second, false},
		{"", 0, true},
		{"abc", 0, true},
		{"0m", 0, true},
		{"-1m", 0, true},
		{" 10m ", 10 * time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseDiscoverInterval(tt.raw)
			if tt.err && err == nil {
				t.Fatalf("ParseDiscoverInterval(%q) error = nil, want error", tt.raw)
			}
			if !tt.err && err != nil {
				t.Fatalf("ParseDiscoverInterval(%q) error = %v", tt.raw, err)
			}
			if !tt.err && got != tt.want {
				t.Fatalf("ParseDiscoverInterval(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseRelativeRange(t *testing.T) {
	t.Parallel()

	before := time.Now().UTC()
	got, err := ParseRelativeRange("5m")
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("ParseRelativeRange(5m) error = %v", err)
	}
	if got.After(before) || got.Before(after.Add(-6*time.Minute)) {
		t.Fatalf("ParseRelativeRange(5m) = %v, outside expected bounds", got)
	}

	_, err = ParseRelativeRange("")
	if err == nil {
		t.Fatal("ParseRelativeRange(empty) should error")
	}
}

func TestCompareValues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	later := now.Add(time.Second)

	tests := []struct {
		name string
		left any
		right any
		want  int
	}{
		{"time before", now, later, -1},
		{"time after", later, now, 1},
		{"time equal", now, now, 0},
		{"int less", int(1), float64(2), -1},
		{"int greater", int(3), float64(2), 1},
		{"int equal", int(2), float64(2), 0},
		{"int64 less", int64(1), float64(2), -1},
		{"float64 less", float64(1.0), float64(2.0), -1},
		{"float64 equal", float64(3.0), float64(3.0), 0},
		{"string less", "abc", "xyz", -1},
		{"string greater", "xyz", "abc", 1},
		{"string equal", "abc", "abc", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompareValues(tt.left, tt.right); got != tt.want {
				t.Fatalf("CompareValues(%v, %v) = %d, want %d", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []float64
		q      float64
		want   float64
	}{
		{"empty", nil, 0.5, 0},
		{"single", []float64{42}, 0.5, 42},
		{"p50 even", []float64{1, 2, 3, 4}, 0.5, 2},
		{"p95 ten", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.95, 10},
		{"p50 two", []float64{10, 20}, 0.5, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Percentile(tt.values, tt.q)
			if got != tt.want {
				t.Fatalf("Percentile(%v, %f) = %f, want %f", tt.values, tt.q, got, tt.want)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  float64
	}{
		{"float64", float64(3.14), 3.14},
		{"float32", float32(2.5), 2.5},
		{"int", int(42), 42},
		{"int64", int64(99), 99},
		{"string", "3.14", 3.14},
		{"bad string", "abc", 0},
		{"nil", nil, 0},
		{"bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToFloat(tt.input)
			if math.Abs(got-tt.want) > 0.01 {
				t.Fatalf("ToFloat(%v) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}

func TestTargetCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  int64
	}{
		{"int64", int64(42), 42},
		{"int", int(99), 99},
		{"float64", float64(3.7), 3},
		{"nil", nil, 0},
		{"string", "bad", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TargetCount(tt.input); got != tt.want {
				t.Fatalf("TargetCount(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildTableResultWithoutAggregates(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Dataset: discover.DatasetTransactions,
		Limit:   2,
	}
	rows := []discover.TableRow{
		{"event.id": "a", "transaction": "GET /"},
		{"event.id": "b", "transaction": "POST /"},
		{"event.id": "c", "transaction": "DELETE /"},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	if result.ResultSize != 2 {
		t.Fatalf("ResultSize = %d, want 2 (limit clamped)", result.ResultSize)
	}
	if len(result.Columns) == 0 {
		t.Fatal("expected default columns for transactions dataset")
	}
}

func TestBuildTableResultWithCustomSelect(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "id", Expr: discover.Expression{Field: "event.id"}},
		},
	}
	rows := []discover.TableRow{
		{"event.id": "a"},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)
	if len(result.Columns) != 1 || result.Columns[0].Name != "id" {
		t.Fatalf("columns = %v", result.Columns)
	}
}

func TestBuildTableResultWithAggregates(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "level", Expr: discover.Expression{Field: "level"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "level"}},
	}
	rows := []discover.TableRow{
		{"level": "error"},
		{"level": "error"},
		{"level": "warning"},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	if result.ResultSize != 2 {
		t.Fatalf("ResultSize = %d, want 2 groups", result.ResultSize)
	}
	// First group should be "error" (insertion order)
	if result.Rows[0]["level"] != "error" {
		t.Fatalf("first group level = %v", result.Rows[0]["level"])
	}
	if result.Rows[0]["count"] != int64(2) {
		t.Fatalf("error count = %v", result.Rows[0]["count"])
	}
}

func TestBuildTableResultAggregateAvg(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "avg_dur", Expr: discover.Expression{Call: "avg", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
	}
	rows := []discover.TableRow{
		{"duration.ms": float64(100)},
		{"duration.ms": float64(200)},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	if result.ResultSize != 1 {
		t.Fatalf("ResultSize = %d, want 1", result.ResultSize)
	}
	avg, ok := result.Rows[0]["avg_dur"].(float64)
	if !ok || avg != 150 {
		t.Fatalf("avg = %v, want 150", result.Rows[0]["avg_dur"])
	}
}

func TestBuildTableResultAggregateMax(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "max_dur", Expr: discover.Expression{Call: "max", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
	}
	rows := []discover.TableRow{
		{"duration.ms": float64(100)},
		{"duration.ms": float64(300)},
		{"duration.ms": float64(200)},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	if result.Rows[0]["max_dur"] != float64(300) {
		t.Fatalf("max = %v, want 300", result.Rows[0]["max_dur"])
	}
}

func TestBuildTableResultAggregateP50(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "p50", Expr: discover.Expression{Call: "p50", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
	}
	rows := []discover.TableRow{
		{"duration.ms": float64(100)},
		{"duration.ms": float64(200)},
		{"duration.ms": float64(300)},
		{"duration.ms": float64(400)},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	p50 := result.Rows[0]["p50"].(float64)
	if p50 != 200 {
		t.Fatalf("p50 = %v, want 200", p50)
	}
}

func TestBuildTableResultAggregateP95(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "p95", Expr: discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}},
		},
	}
	rows := make([]discover.TableRow, 100)
	for i := range rows {
		rows[i] = discover.TableRow{"duration.ms": float64(i + 1)}
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	p95 := result.Rows[0]["p95"].(float64)
	if p95 != 95 {
		t.Fatalf("p95 = %v, want 95", p95)
	}
}

func TestBuildTableResultSortsAggregatedRows(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Limit: 10,
		Select: []discover.SelectItem{
			{Alias: "level", Expr: discover.Expression{Field: "level"}},
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
		GroupBy: []discover.Expression{{Field: "level"}},
		OrderBy: []discover.OrderBy{
			{Expr: discover.Expression{Alias: "count"}, Direction: "desc"},
		},
	}
	rows := []discover.TableRow{
		{"level": "warning"},
		{"level": "error"},
		{"level": "error"},
		{"level": "error"},
	}
	result := BuildTableResult(query, discover.CostEstimate{}, rows)

	if result.Rows[0]["level"] != "error" || result.Rows[0]["count"] != int64(3) {
		t.Fatalf("expected error group first with count 3, got %v", result.Rows[0])
	}
}

func TestBuildSeriesResultRequiresRollup(t *testing.T) {
	t.Parallel()

	_, err := BuildSeriesResult(discover.Query{}, discover.CostEstimate{}, nil)
	if err == nil {
		t.Fatal("expected error for missing rollup")
	}
}

func TestBuildSeriesResultInvalidInterval(t *testing.T) {
	t.Parallel()

	_, err := BuildSeriesResult(discover.Query{
		Rollup: &discover.Rollup{Interval: "badinterval"},
	}, discover.CostEstimate{}, nil)
	if err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

func TestBuildSeriesResultBucketsCorrectly(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	query := discover.Query{
		Rollup: &discover.Rollup{Interval: "1h"},
		Select: []discover.SelectItem{
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
	}
	rows := []discover.TableRow{
		{"timestamp": base},
		{"timestamp": base.Add(30 * time.Minute)},
		{"timestamp": base.Add(90 * time.Minute)},
	}
	result, err := BuildSeriesResult(query, discover.CostEstimate{}, rows)
	if err != nil {
		t.Fatalf("BuildSeriesResult error = %v", err)
	}
	if len(result.Points) != 2 {
		t.Fatalf("point count = %d, want 2", len(result.Points))
	}
	if result.Points[0].Values["count"] != int64(2) {
		t.Fatalf("first bucket count = %v, want 2", result.Points[0].Values["count"])
	}
	if result.Points[1].Values["count"] != int64(1) {
		t.Fatalf("second bucket count = %v, want 1", result.Points[1].Values["count"])
	}
}

func TestBuildSeriesResultSkipsZeroTimestamps(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Rollup: &discover.Rollup{Interval: "1h"},
		Select: []discover.SelectItem{
			{Alias: "count", Expr: discover.Expression{Call: "count"}},
		},
	}
	rows := []discover.TableRow{
		{"timestamp": time.Time{}},
		{"timestamp": "not a time"},
	}
	result, err := BuildSeriesResult(query, discover.CostEstimate{}, rows)
	if err != nil {
		t.Fatalf("BuildSeriesResult error = %v", err)
	}
	if len(result.Points) != 0 {
		t.Fatalf("point count = %d, want 0 (all skipped)", len(result.Points))
	}
}

func TestRawOrderClauseFallsBackForAggregates(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Select: []discover.SelectItem{
			{Expr: discover.Expression{Call: "count"}},
		},
	}
	got := RawOrderClause(query, "timestamp DESC", nil)
	if got != "timestamp DESC" {
		t.Fatalf("RawOrderClause for aggregate = %q, want fallback", got)
	}
}

func TestRawOrderClauseResolvesFieldAndDirection(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Select: []discover.SelectItem{
			{Alias: "ts", Expr: discover.Expression{Field: "timestamp"}},
		},
		OrderBy: []discover.OrderBy{
			{Expr: discover.Expression{Field: "timestamp"}, Direction: "desc"},
		},
	}
	expr := func(field string) (string, bool) {
		if field == "timestamp" {
			return "e.occurred_at", true
		}
		return "", false
	}
	got := RawOrderClause(query, "fallback", expr)
	if got != "e.occurred_at DESC" {
		t.Fatalf("RawOrderClause = %q, want 'e.occurred_at DESC'", got)
	}
}

func TestRawOrderClauseResolvesAlias(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		Select: []discover.SelectItem{
			{Alias: "ts", Expr: discover.Expression{Field: "timestamp"}},
		},
		OrderBy: []discover.OrderBy{
			{Expr: discover.Expression{Alias: "ts"}, Direction: "asc"},
		},
	}
	expr := func(field string) (string, bool) {
		if field == "timestamp" {
			return "e.occurred_at", true
		}
		return "", false
	}
	got := RawOrderClause(query, "fallback", expr)
	if got != "e.occurred_at ASC" {
		t.Fatalf("RawOrderClause alias = %q, want 'e.occurred_at ASC'", got)
	}
}

func TestRawOrderClauseFallsBackForUnresolvable(t *testing.T) {
	t.Parallel()

	query := discover.Query{
		OrderBy: []discover.OrderBy{
			{Expr: discover.Expression{Field: "unknown"}, Direction: "asc"},
		},
	}
	expr := func(_ string) (string, bool) {
		return "", false
	}
	got := RawOrderClause(query, "fallback", expr)
	if got != "fallback" {
		t.Fatalf("RawOrderClause unresolvable = %q, want fallback", got)
	}
}
