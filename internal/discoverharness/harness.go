package discoverharness

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"urgentry/internal/discover"
)

//go:embed testdata/query_corpus.json
var corpusFS embed.FS

type Executor interface {
	ExecuteTable(ctx context.Context, query discover.Query) (discover.TableResult, error)
	ExecuteSeries(ctx context.Context, query discover.Query) (discover.SeriesResult, error)
	Explain(query discover.Query) (discover.ExplainPlan, error)
}

type Case struct {
	Name            string          `json:"name"`
	Mode            string          `json:"mode"`
	Query           discover.Query  `json:"query"`
	ExpectTable     *TableSnapshot  `json:"expect_table,omitempty"`
	ExpectSeries    *SeriesSnapshot `json:"expect_series,omitempty"`
	ExplainContains []string        `json:"explain_contains,omitempty"`
	Benchmark       bool            `json:"benchmark,omitempty"`
}

type TableSnapshot struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

type SeriesSnapshot struct {
	Columns []string      `json:"columns"`
	Points  []SeriesPoint `json:"points"`
}

type SeriesPoint struct {
	Bucket string         `json:"bucket"`
	Values map[string]any `json:"values"`
}

func LoadCases() ([]Case, error) {
	data, err := corpusFS.ReadFile("testdata/query_corpus.json")
	if err != nil {
		return nil, fmt.Errorf("read query corpus: %w", err)
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode query corpus: %w", err)
	}
	return cases, nil
}

func RunCase(ctx context.Context, exec Executor, item Case) error {
	switch strings.ToLower(strings.TrimSpace(item.Mode)) {
	case "table":
		result, err := exec.ExecuteTable(ctx, item.Query)
		if err != nil {
			return err
		}
		actual := SnapshotTable(result)
		if item.ExpectTable == nil {
			return fmt.Errorf("%s: missing table expectation", item.Name)
		}
		if err := compareSnapshot(item.ExpectTable, actual); err != nil {
			return fmt.Errorf("%s: %w", item.Name, err)
		}
	case "series":
		result, err := exec.ExecuteSeries(ctx, item.Query)
		if err != nil {
			return err
		}
		actual := SnapshotSeries(result)
		if item.ExpectSeries == nil {
			return fmt.Errorf("%s: missing series expectation", item.Name)
		}
		if err := compareSnapshot(item.ExpectSeries, actual); err != nil {
			return fmt.Errorf("%s: %w", item.Name, err)
		}
	default:
		return fmt.Errorf("%s: unsupported mode %q", item.Name, item.Mode)
	}
	if len(item.ExplainContains) > 0 {
		plan, err := exec.Explain(item.Query)
		if err != nil {
			return err
		}
		for _, want := range item.ExplainContains {
			if !strings.Contains(plan.SQL, want) {
				return fmt.Errorf("%s: explain plan missing %q in %q", item.Name, want, plan.SQL)
			}
		}
	}
	return nil
}

func SnapshotTable(result discover.TableResult) *TableSnapshot {
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		item := map[string]any{}
		for key, value := range row {
			item[key] = normalizeValue(value)
		}
		rows = append(rows, item)
	}
	cols := make([]string, 0, len(result.Columns))
	for _, col := range result.Columns {
		cols = append(cols, col.Name)
	}
	return &TableSnapshot{Columns: cols, Rows: rows}
}

func SnapshotSeries(result discover.SeriesResult) *SeriesSnapshot {
	cols := make([]string, 0, len(result.Columns))
	for _, col := range result.Columns {
		cols = append(cols, col.Name)
	}
	points := make([]SeriesPoint, 0, len(result.Points))
	for _, point := range result.Points {
		values := map[string]any{}
		for key, value := range point.Values {
			values[key] = normalizeValue(value)
		}
		points = append(points, SeriesPoint{
			Bucket: point.Bucket.UTC().Format(time.RFC3339),
			Values: values,
		})
	}
	return &SeriesSnapshot{Columns: cols, Points: points}
}

func SnapshotSize(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

func compareSnapshot(expected, actual any) error {
	if reflect.DeepEqual(expected, actual) {
		return nil
	}
	want, _ := json.MarshalIndent(expected, "", "  ")
	got, _ := json.MarshalIndent(actual, "", "  ")
	return fmt.Errorf("snapshot mismatch\nwant:\n%s\n\ngot:\n%s", string(want), string(got))
}

func normalizeValue(value any) any {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return ""
		}
		return v.UTC().Format(time.RFC3339)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	default:
		return v
	}
}
