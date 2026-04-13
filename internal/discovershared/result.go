package discovershared

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/store"
)

// ExplainMode returns "series" when the query has a rollup, otherwise "table".
func ExplainMode(query discover.Query) string {
	if query.Rollup != nil {
		return "series"
	}
	return "table"
}

// IssueRow converts a store.DiscoverIssue into the canonical discover.TableRow
// shared by both the SQLite and bridge backends.
func IssueRow(item store.DiscoverIssue) discover.TableRow {
	return discover.TableRow{
		"issue.id":       item.ID,
		"project.id":     item.ProjectID,
		"project":        item.ProjectSlug,
		"release":        item.Release,
		"environment":    item.Environment,
		"title":          item.Title,
		"culprit":        item.Culprit,
		"level":          item.Level,
		"status":         item.Status,
		"first_seen":     item.FirstSeen,
		"last_seen":      item.LastSeen,
		"timestamp":      item.LastSeen,
		"count":          item.Count,
		"issue.short_id": item.ShortID,
		"assignee":       item.Assignee,
		"event.type":     "error",
	}
}

// IssueRows maps a slice of store.DiscoverIssue to discover.TableRow.
func IssueRows(items []store.DiscoverIssue) []discover.TableRow {
	rows := make([]discover.TableRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, IssueRow(item))
	}
	return rows
}

// LogRow converts a store.DiscoverLog into the canonical discover.TableRow.
func LogRow(item store.DiscoverLog) discover.TableRow {
	return discover.TableRow{
		"event.id":    item.EventID,
		"project.id":  item.ProjectID,
		"project":     item.ProjectSlug,
		"title":       item.Title,
		"message":     item.Message,
		"level":       item.Level,
		"platform":    item.Platform,
		"culprit":     item.Culprit,
		"environment": item.Environment,
		"release":     item.Release,
		"logger":      item.Logger,
		"trace.id":    item.TraceID,
		"span.id":     item.SpanID,
		"timestamp":   item.Timestamp,
		"event.type":  "log",
		"count":       int64(1),
	}
}

// LogRows maps a slice of store.DiscoverLog to discover.TableRow.
func LogRows(items []store.DiscoverLog) []discover.TableRow {
	rows := make([]discover.TableRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, LogRow(item))
	}
	return rows
}

// TransactionRow converts a store.DiscoverTransaction into the canonical discover.TableRow.
func TransactionRow(item store.DiscoverTransaction) discover.TableRow {
	return discover.TableRow{
		"event.id":    item.EventID,
		"project.id":  item.ProjectID,
		"project":     item.ProjectSlug,
		"transaction": item.Transaction,
		"op":          item.Op,
		"status":      item.Status,
		"platform":    item.Platform,
		"environment": item.Environment,
		"release":     item.Release,
		"trace.id":    item.TraceID,
		"span.id":     item.SpanID,
		"timestamp":   item.Timestamp,
		"duration.ms": item.DurationMS,
		"event.type":  "transaction",
		"count":       int64(1),
	}
}

// TransactionRows maps a slice of store.DiscoverTransaction to discover.TableRow.
func TransactionRows(items []store.DiscoverTransaction) []discover.TableRow {
	rows := make([]discover.TableRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, TransactionRow(item))
	}
	return rows
}

func BuildTableResult(query discover.Query, cost discover.CostEstimate, rows []discover.TableRow) discover.TableResult {
	if UsesAggregate(query) || len(query.GroupBy) > 0 {
		return aggregateTable(query, cost, rows)
	}
	return defaultTable(query, cost, rows)
}

func BuildSeriesResult(query discover.Query, cost discover.CostEstimate, rows []discover.TableRow) (discover.SeriesResult, error) {
	if query.Rollup == nil {
		return discover.SeriesResult{}, discover.ValidationErrors{{
			Code:    "missing_rollup",
			Path:    "rollup",
			Message: "series execution requires a rollup",
		}}
	}
	interval, err := ParseDiscoverInterval(query.Rollup.Interval)
	if err != nil {
		return discover.SeriesResult{}, discover.ValidationErrors{{
			Code:    "invalid_rollup",
			Path:    "rollup.interval",
			Message: err.Error(),
		}}
	}
	type bucket struct {
		at     time.Time
		values map[string]any
	}
	buckets := map[int64]*bucket{}
	for _, row := range rows {
		rawTS, ok := row["timestamp"].(time.Time)
		if !ok || rawTS.IsZero() {
			continue
		}
		start := rawTS.UTC().Unix() / int64(interval.Seconds()) * int64(interval.Seconds())
		item := buckets[start]
		if item == nil {
			item = &bucket{at: time.Unix(start, 0).UTC(), values: map[string]any{}}
			buckets[start] = item
		}
		applyAggregates(item.values, query.Select, row)
	}
	keys := make([]int64, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	points := make([]discover.SeriesPoint, 0, len(keys))
	for _, key := range keys {
		item := buckets[key]
		finalizeAggregates(item.values)
		points = append(points, discover.SeriesPoint{
			Bucket: item.at,
			Values: item.values,
		})
	}
	return discover.SeriesResult{
		Columns: SelectColumns(query),
		Points:  points,
		Cost:    cost,
	}, nil
}

func SelectColumns(query discover.Query) []discover.Column {
	columns := make([]discover.Column, 0, len(query.Select))
	for _, item := range query.Select {
		columns = append(columns, discover.Column{Name: SelectName(item)})
	}
	return columns
}

func SelectName(item discover.SelectItem) string {
	if strings.TrimSpace(item.Alias) != "" {
		return strings.TrimSpace(item.Alias)
	}
	if strings.TrimSpace(item.Expr.Field) != "" {
		return strings.ToLower(strings.TrimSpace(item.Expr.Field))
	}
	return strings.ToLower(strings.TrimSpace(item.Expr.Call))
}

func DefaultColumns(dataset discover.Dataset) []discover.Column {
	switch dataset {
	case discover.DatasetIssues:
		return []discover.Column{{Name: "issue.id"}, {Name: "project.id"}, {Name: "project"}, {Name: "title"}, {Name: "culprit"}, {Name: "level"}, {Name: "status"}, {Name: "first_seen"}, {Name: "last_seen"}, {Name: "count"}, {Name: "issue.short_id"}, {Name: "assignee"}}
	case discover.DatasetLogs:
		return []discover.Column{{Name: "event.id"}, {Name: "project.id"}, {Name: "project"}, {Name: "title"}, {Name: "message"}, {Name: "level"}, {Name: "platform"}, {Name: "culprit"}, {Name: "environment"}, {Name: "release"}, {Name: "logger"}, {Name: "trace.id"}, {Name: "span.id"}, {Name: "timestamp"}}
	default:
		return []discover.Column{{Name: "event.id"}, {Name: "project.id"}, {Name: "project"}, {Name: "transaction"}, {Name: "op"}, {Name: "status"}, {Name: "environment"}, {Name: "release"}, {Name: "trace.id"}, {Name: "span.id"}, {Name: "timestamp"}, {Name: "duration.ms"}}
	}
}

func UsesAggregate(query discover.Query) bool {
	for _, item := range query.Select {
		if strings.TrimSpace(item.Expr.Call) != "" {
			return true
		}
	}
	return false
}

func ScanLimit(query discover.Query) int {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if UsesAggregate(query) || len(query.GroupBy) > 0 || query.Rollup != nil {
		limit = max(limit*20, 500)
	}
	return min(limit, 5000)
}

func ParseDiscoverInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("rollup interval is required")
	}
	multiplier := time.Second
	switch suffix := raw[len(raw)-1]; suffix {
	case 'm':
		multiplier = time.Minute
		raw = raw[:len(raw)-1]
	case 'h':
		multiplier = time.Hour
		raw = raw[:len(raw)-1]
	case 'd':
		multiplier = 24 * time.Hour
		raw = raw[:len(raw)-1]
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid rollup interval")
	}
	return time.Duration(value) * multiplier, nil
}

func ParseRelativeRange(raw string) (time.Time, error) {
	dur, err := ParseDiscoverInterval(raw)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(-dur), nil
}

func RawOrderClause(query discover.Query, fallback string, expr func(string) (string, bool)) string {
	if UsesAggregate(query) || len(query.GroupBy) > 0 || len(query.OrderBy) == 0 {
		return fallback
	}
	parts := make([]string, 0, len(query.OrderBy))
	for _, item := range query.OrderBy {
		sqlExpr := rawOrderExpr(query, item, expr)
		if sqlExpr == "" {
			continue
		}
		direction := "ASC"
		if strings.EqualFold(item.Direction, "desc") {
			direction = "DESC"
		}
		parts = append(parts, sqlExpr+" "+direction)
	}
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, ", ")
}

func TimeRangeClause(builder ArgBuilder, expr string, value discover.TimeRange) (string, error) {
	switch value.Kind {
	case "absolute":
		start, err := time.Parse(time.RFC3339, value.Start)
		if err != nil {
			return "", err
		}
		end, err := time.Parse(time.RFC3339, value.End)
		if err != nil {
			return "", err
		}
		return expr + ` >= ` + builder.Add(start.UTC()) + ` AND ` + expr + ` <= ` + builder.Add(end.UTC()), nil
	case "relative":
		start, err := ParseRelativeRange(value.Value)
		if err != nil {
			return "", err
		}
		return expr + ` >= ` + builder.Add(start.UTC()), nil
	default:
		return "", fmt.Errorf("unsupported time range kind %q", value.Kind)
	}
}

func CompareValues(left, right any) int {
	switch l := left.(type) {
	case time.Time:
		r, _ := right.(time.Time)
		switch {
		case l.Before(r):
			return -1
		case l.After(r):
			return 1
		default:
			return 0
		}
	case int:
		r := ToFloat(right)
		switch {
		case float64(l) < r:
			return -1
		case float64(l) > r:
			return 1
		default:
			return 0
		}
	case int64:
		r := ToFloat(right)
		switch {
		case float64(l) < r:
			return -1
		case float64(l) > r:
			return 1
		default:
			return 0
		}
	case float64:
		r := ToFloat(right)
		switch {
		case l < r:
			return -1
		case l > r:
			return 1
		default:
			return 0
		}
	default:
		ls := fmt.Sprintf("%v", left)
		rs := fmt.Sprintf("%v", right)
		switch {
		case ls < rs:
			return -1
		case ls > rs:
			return 1
		default:
			return 0
		}
	}
}

func Percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*q)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func ToFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

func TargetCount(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func defaultTable(query discover.Query, cost discover.CostEstimate, rows []discover.TableRow) discover.TableResult {
	columns := DefaultColumns(query.Dataset)
	if len(query.Select) > 0 {
		columns = SelectColumns(query)
	}
	items := make([]discover.TableRow, 0, min(query.Limit, len(rows)))
	for i, row := range rows {
		if i >= query.Limit {
			break
		}
		items = append(items, projectRow(row, columns))
	}
	return discover.TableResult{
		Columns:    columns,
		Rows:       items,
		ResultSize: len(items),
		Cost:       cost,
	}
}

func aggregateTable(query discover.Query, cost discover.CostEstimate, rows []discover.TableRow) discover.TableResult {
	type group struct {
		key    string
		values discover.TableRow
	}
	groups := map[string]*group{}
	var order []string
	for _, row := range rows {
		key := aggregateKey(query.GroupBy, row)
		item := groups[key]
		if item == nil {
			item = &group{key: key, values: discover.TableRow{}}
			for _, expr := range query.GroupBy {
				field := strings.ToLower(strings.TrimSpace(expr.Field))
				item.values[field] = row[field]
			}
			groups[key] = item
			order = append(order, key)
		}
		applyAggregates(item.values, query.Select, row)
	}
	items := make([]discover.TableRow, 0, len(groups))
	for _, key := range order {
		item := groups[key]
		finalizeAggregates(item.values)
		items = append(items, item.values)
	}
	sortAggregatedRows(items, query.OrderBy)
	if len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return discover.TableResult{
		Columns:    SelectColumns(query),
		Rows:       items,
		ResultSize: len(items),
		Cost:       cost,
	}
}

func applyAggregates(target discover.TableRow, selects []discover.SelectItem, row discover.TableRow) {
	for _, item := range selects {
		name := SelectName(item)
		switch item.Expr.Call {
		case "":
			field := strings.ToLower(strings.TrimSpace(item.Expr.Field))
			target[name] = row[field]
		case "count":
			current, _ := target[name].(int64)
			target[name] = current + 1
		case "avg":
			sumKey := name + ".__sum"
			countKey := name + ".__count"
			target[sumKey] = ToFloat(target[sumKey]) + ToFloat(row[item.Expr.Args[0].Field])
			target[countKey] = TargetCount(target[countKey]) + 1
		case "max":
			value := ToFloat(row[item.Expr.Args[0].Field])
			if existing, ok := target[name]; !ok || value > ToFloat(existing) {
				target[name] = value
			}
		case "p50", "p95":
			listKey := name + ".__values"
			values, _ := target[listKey].([]float64)
			target[listKey] = append(values, ToFloat(row[item.Expr.Args[0].Field]))
			target[name+".__agg"] = item.Expr.Call
		}
	}
}

func finalizeAggregates(values discover.TableRow) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for _, key := range keys {
		switch {
		case strings.HasSuffix(key, ".__sum"):
			name := strings.TrimSuffix(key, ".__sum")
			count := TargetCount(values[name+".__count"])
			if count > 0 {
				values[name] = ToFloat(values[key]) / float64(count)
			}
			delete(values, key)
			delete(values, name+".__count")
		case strings.HasSuffix(key, ".__values"):
			name := strings.TrimSuffix(key, ".__values")
			points, _ := values[key].([]float64)
			sort.Float64s(points)
			switch {
			case points == nil:
				values[name] = float64(0)
			case values[name+".__agg"] == "p50":
				values[name] = Percentile(points, 0.50)
			case values[name+".__agg"] == "p95":
				values[name] = Percentile(points, 0.95)
			}
			delete(values, key)
			delete(values, name+".__agg")
		}
	}
}

func sortAggregatedRows(rows []discover.TableRow, orderBy []discover.OrderBy) {
	if len(orderBy) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, item := range orderBy {
			left := orderValue(rows[i], item.Expr)
			right := orderValue(rows[j], item.Expr)
			cmp := CompareValues(left, right)
			if cmp == 0 {
				continue
			}
			if item.Direction == "desc" {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

func orderValue(row discover.TableRow, expr discover.Expression) any {
	if expr.Alias != "" {
		return row[expr.Alias]
	}
	return row[strings.ToLower(strings.TrimSpace(expr.Field))]
}

func aggregateKey(groupBy []discover.Expression, row discover.TableRow) string {
	if len(groupBy) == 0 {
		return "__all__"
	}
	parts := make([]string, 0, len(groupBy))
	for _, expr := range groupBy {
		parts = append(parts, fmt.Sprintf("%v", row[strings.ToLower(strings.TrimSpace(expr.Field))]))
	}
	return strings.Join(parts, "\x1f")
}

func rawOrderExpr(query discover.Query, order discover.OrderBy, expr func(string) (string, bool)) string {
	field := strings.ToLower(strings.TrimSpace(order.Expr.Field))
	if field == "" && strings.TrimSpace(order.Expr.Alias) != "" {
		alias := strings.TrimSpace(order.Expr.Alias)
		for _, item := range query.Select {
			if strings.TrimSpace(item.Alias) == alias && strings.TrimSpace(item.Expr.Field) != "" {
				field = strings.ToLower(strings.TrimSpace(item.Expr.Field))
				break
			}
		}
	}
	if field == "" {
		return ""
	}
	sqlExpr, ok := expr(field)
	if !ok {
		return ""
	}
	return sqlExpr
}

func projectRow(row discover.TableRow, cols []discover.Column) discover.TableRow {
	item := discover.TableRow{}
	for _, col := range cols {
		item[col.Name] = row[col.Name]
	}
	return item
}
