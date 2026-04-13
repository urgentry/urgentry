package discoverharness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"urgentry/internal/discover"
)

func TestLoadCasesReturnsNonEmptyCorpus(t *testing.T) {
	t.Parallel()

	cases, err := LoadCases()
	if err != nil {
		t.Fatalf("LoadCases() error = %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("LoadCases() returned empty corpus")
	}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatal("corpus case has empty name")
		}
		if c.Mode == "" {
			t.Fatal("corpus case has empty mode")
		}
	}
}

func TestSnapshotTablePreservesColumnsAndNormalizesValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	result := discover.TableResult{
		Columns: []discover.Column{{Name: "event.id"}, {Name: "timestamp"}, {Name: "count"}},
		Rows: []discover.TableRow{
			{"event.id": "evt-1", "timestamp": now, "count": int64(42)},
			{"event.id": "evt-2", "timestamp": time.Time{}, "count": int(7)},
		},
	}
	snap := SnapshotTable(result)

	if len(snap.Columns) != 3 {
		t.Fatalf("column count = %d, want 3", len(snap.Columns))
	}
	if snap.Columns[0] != "event.id" || snap.Columns[1] != "timestamp" || snap.Columns[2] != "count" {
		t.Fatalf("columns = %v", snap.Columns)
	}
	if len(snap.Rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(snap.Rows))
	}

	// time.Time normalizes to RFC3339
	if snap.Rows[0]["timestamp"] != "2026-03-29T12:00:00Z" {
		t.Fatalf("row[0].timestamp = %v, want RFC3339", snap.Rows[0]["timestamp"])
	}
	// zero time normalizes to empty string
	if snap.Rows[1]["timestamp"] != "" {
		t.Fatalf("row[1].timestamp = %v, want empty string", snap.Rows[1]["timestamp"])
	}
	// int64 normalizes to float64
	if snap.Rows[0]["count"] != float64(42) {
		t.Fatalf("row[0].count = %v (%T), want float64(42)", snap.Rows[0]["count"], snap.Rows[0]["count"])
	}
	// int normalizes to float64
	if snap.Rows[1]["count"] != float64(7) {
		t.Fatalf("row[1].count = %v (%T), want float64(7)", snap.Rows[1]["count"], snap.Rows[1]["count"])
	}
}

func TestSnapshotSeriesNormalizesTimeBucketsAndValues(t *testing.T) {
	t.Parallel()

	bucket := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	result := discover.SeriesResult{
		Columns: []discover.Column{{Name: "count"}},
		Points: []discover.SeriesPoint{
			{Bucket: bucket, Values: map[string]any{"count": int32(10)}},
			{Bucket: bucket.Add(time.Hour), Values: map[string]any{"count": float32(20.5)}},
		},
	}
	snap := SnapshotSeries(result)

	if len(snap.Columns) != 1 || snap.Columns[0] != "count" {
		t.Fatalf("columns = %v", snap.Columns)
	}
	if len(snap.Points) != 2 {
		t.Fatalf("point count = %d, want 2", len(snap.Points))
	}
	if snap.Points[0].Bucket != "2026-03-29T12:00:00Z" {
		t.Fatalf("points[0].bucket = %q", snap.Points[0].Bucket)
	}
	if snap.Points[1].Bucket != "2026-03-29T13:00:00Z" {
		t.Fatalf("points[1].bucket = %q", snap.Points[1].Bucket)
	}
	// int32 -> float64
	if snap.Points[0].Values["count"] != float64(10) {
		t.Fatalf("points[0].count = %v (%T)", snap.Points[0].Values["count"], snap.Points[0].Values["count"])
	}
	// float32 -> float64
	if v, ok := snap.Points[1].Values["count"].(float64); !ok || v < 20.4 || v > 20.6 {
		t.Fatalf("points[1].count = %v (%T)", snap.Points[1].Values["count"], snap.Points[1].Values["count"])
	}
}

func TestSnapshotSizeReturnsJSONLength(t *testing.T) {
	t.Parallel()

	size := SnapshotSize(map[string]string{"key": "value"})
	if size == 0 {
		t.Fatal("SnapshotSize returned 0 for non-empty map")
	}
	data, _ := json.Marshal(map[string]string{"key": "value"})
	if size != len(data) {
		t.Fatalf("SnapshotSize = %d, want %d", size, len(data))
	}
}

func TestSnapshotSizeHandlesUnmarshalableTypes(t *testing.T) {
	t.Parallel()

	// Channels can't be marshalled to JSON
	size := SnapshotSize(make(chan struct{}))
	if size != 0 {
		t.Fatalf("SnapshotSize for unmarshalable = %d, want 0", size)
	}
}

func TestNormalizeValueCoversAllIntTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  float64
	}{
		{"int", int(5), float64(5)},
		{"int64", int64(5), float64(5)},
		{"int32", int32(5), float64(5)},
		{"float32", float32(5.5), float64(5.5)},
		{"float64", float64(5.5), float64(5.5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeValue(tt.input)
			if v, ok := got.(float64); !ok || v != tt.want {
				t.Fatalf("normalizeValue(%v) = %v (%T), want %v", tt.input, got, got, tt.want)
			}
		})
	}
}

func TestNormalizeValuePassthroughForStrings(t *testing.T) {
	t.Parallel()

	got := normalizeValue("hello")
	if got != "hello" {
		t.Fatalf("normalizeValue(string) = %v, want hello", got)
	}
}

func TestRunCaseReturnsErrorForUnsupportedMode(t *testing.T) {
	t.Parallel()

	err := RunCase(context.Background(), &fakeExecutor{}, Case{
		Name: "bad-mode",
		Mode: "unknown",
	})
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

func TestRunCaseTableModeWithMissingExpectationErrors(t *testing.T) {
	t.Parallel()

	err := RunCase(context.Background(), &fakeExecutor{}, Case{
		Name:        "no-expect",
		Mode:        "table",
		ExpectTable: nil,
	})
	if err == nil {
		t.Fatal("expected error for missing table expectation")
	}
}

func TestRunCaseSeriesModeWithMissingExpectationErrors(t *testing.T) {
	t.Parallel()

	err := RunCase(context.Background(), &fakeExecutor{}, Case{
		Name:         "no-expect",
		Mode:         "series",
		ExpectSeries: nil,
	})
	if err == nil {
		t.Fatal("expected error for missing series expectation")
	}
}

func TestRunCaseTableModeSnapshotMismatch(t *testing.T) {
	t.Parallel()

	err := RunCase(context.Background(), &fakeExecutor{}, Case{
		Name: "mismatch",
		Mode: "table",
		ExpectTable: &TableSnapshot{
			Columns: []string{"id"},
			Rows:    []map[string]any{{"id": "different"}},
		},
	})
	if err == nil {
		t.Fatal("expected snapshot mismatch error")
	}
}

func TestRunCaseExplainContainsChecksSQL(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{
		explainSQL: "SELECT * FROM events WHERE project_id = ?",
	}

	// contains passes
	err := RunCase(context.Background(), exec, Case{
		Name:            "explain-ok",
		Mode:            "table",
		ExpectTable:     &TableSnapshot{Columns: []string{}, Rows: []map[string]any{}},
		ExplainContains: []string{"FROM events"},
	})
	if err != nil {
		t.Fatalf("RunCase explain pass: %v", err)
	}

	// contains fails
	err = RunCase(context.Background(), exec, Case{
		Name:            "explain-fail",
		Mode:            "table",
		ExpectTable:     &TableSnapshot{Columns: []string{}, Rows: []map[string]any{}},
		ExplainContains: []string{"FROM nonexistent"},
	})
	if err == nil {
		t.Fatal("expected explain mismatch error")
	}
}

func TestRunCaseSeriesMode(t *testing.T) {
	t.Parallel()

	bucket := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{
		seriesResult: discover.SeriesResult{
			Columns: []discover.Column{{Name: "count"}},
			Points: []discover.SeriesPoint{
				{Bucket: bucket, Values: map[string]any{"count": float64(5)}},
			},
		},
	}

	err := RunCase(context.Background(), exec, Case{
		Name: "series-ok",
		Mode: "series",
		ExpectSeries: &SeriesSnapshot{
			Columns: []string{"count"},
			Points: []SeriesPoint{
				{Bucket: "2026-03-29T12:00:00Z", Values: map[string]any{"count": float64(5)}},
			},
		},
	})
	if err != nil {
		t.Fatalf("RunCase series: %v", err)
	}
}

func TestRunCaseTableExecutionError(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{tableErr: fmt.Errorf("table boom")}
	err := RunCase(context.Background(), exec, Case{
		Name: "exec-err",
		Mode: "table",
		ExpectTable: &TableSnapshot{
			Columns: []string{},
			Rows:    []map[string]any{},
		},
	})
	if err == nil {
		t.Fatal("expected execution error to propagate")
	}
}

func TestRunCaseSeriesExecutionError(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{seriesErr: fmt.Errorf("series boom")}
	err := RunCase(context.Background(), exec, Case{
		Name: "exec-err",
		Mode: "series",
		ExpectSeries: &SeriesSnapshot{
			Columns: []string{},
			Points:  []SeriesPoint{},
		},
	})
	if err == nil {
		t.Fatal("expected execution error to propagate")
	}
}

type fakeExecutor struct {
	tableResult  discover.TableResult
	seriesResult discover.SeriesResult
	explainSQL   string
	tableErr     error
	seriesErr    error
}

func (f *fakeExecutor) ExecuteTable(_ context.Context, _ discover.Query) (discover.TableResult, error) {
	if f.tableErr != nil {
		return discover.TableResult{}, f.tableErr
	}
	return f.tableResult, nil
}

func (f *fakeExecutor) ExecuteSeries(_ context.Context, _ discover.Query) (discover.SeriesResult, error) {
	if f.seriesErr != nil {
		return discover.SeriesResult{}, f.seriesErr
	}
	return f.seriesResult, nil
}

func (f *fakeExecutor) Explain(_ discover.Query) (discover.ExplainPlan, error) {
	return discover.ExplainPlan{SQL: f.explainSQL}, nil
}
