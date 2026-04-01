package analyticssnapshot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type Result struct {
	Type      string
	Columns   []string
	Rows      [][]string
	StatLabel string
	StatValue string
}

func BodyFromResult(result Result, query discover.Query, sourceLabel, dataset, visualization string, filters []string) sqlite.SnapshotBody {
	costLabel, queryJSON := Explain(query)
	return sqlite.SnapshotBody{
		ViewType:      result.Type,
		Columns:       append([]string(nil), result.Columns...),
		Rows:          appendRows(result.Rows),
		StatLabel:     result.StatLabel,
		StatValue:     result.StatValue,
		SourceLabel:   sourceLabel,
		Dataset:       dataset,
		Visualization: visualization,
		Filters:       append([]string(nil), filters...),
		CostLabel:     costLabel,
		QueryJSON:     queryJSON,
	}
}

func Explain(query discover.Query) (string, string) {
	normalized, cost, err := discover.ValidateQuery(query)
	if err == nil {
		query = normalized
	}
	queryJSON := ""
	if raw, marshalErr := json.MarshalIndent(query, "", "  "); marshalErr == nil {
		queryJSON = string(raw)
	}
	if cost.Class == "" {
		return "", queryJSON
	}
	return fmt.Sprintf("Estimated planner cost: %s (%d)", cost.Class, cost.Score), queryJSON
}

func FromTable(result discover.TableResult) Result {
	view := Result{Type: "table"}
	for _, col := range result.Columns {
		view.Columns = append(view.Columns, col.Name)
	}
	for _, row := range result.Rows {
		rendered := make([]string, 0, len(result.Columns))
		for _, col := range result.Columns {
			rendered = append(rendered, formatValue(row[col.Name]))
		}
		view.Rows = append(view.Rows, rendered)
	}
	return view
}

func FromSeries(result discover.SeriesResult) Result {
	view := Result{
		Type:    "series",
		Columns: []string{"bucket"},
	}
	valueColumns := make([]string, 0, len(result.Columns))
	for _, col := range result.Columns {
		valueColumns = append(valueColumns, col.Name)
		view.Columns = append(view.Columns, col.Name)
	}
	for _, point := range result.Points {
		row := []string{point.Bucket.UTC().Format(time.RFC3339)}
		for _, col := range valueColumns {
			row = append(row, formatValue(point.Values[col]))
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func FromStat(result discover.TableResult) Result {
	view := Result{Type: "stat"}
	if len(result.Columns) == 0 || len(result.Rows) == 0 {
		return view
	}
	view.StatLabel = result.Columns[0].Name
	view.StatValue = formatValue(result.Rows[0][result.Columns[0].Name])
	return view
}

func appendRows(rows [][]string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, append([]string(nil), row...))
	}
	return out
}

func formatValue(value any) string {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return ""
		}
		return v.UTC().Format(time.RFC3339)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', 2, 64)
	case float32:
		f := float64(v)
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', 2, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprintf("%v", value)
	}
}
