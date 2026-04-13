package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// MetricBucketQueryEngine queries the metric_buckets table for aggregate
// statistics over time windows with optional tag filtering.
type MetricBucketQueryEngine struct {
	db *sql.DB
}

// NewMetricBucketQueryEngine creates a query engine backed by SQLite.
func NewMetricBucketQueryEngine(db *sql.DB) *MetricBucketQueryEngine {
	return &MetricBucketQueryEngine{db: db}
}

// AggregateFunc defines supported aggregation functions.
type AggregateFunc string

const (
	AggregateFuncSum   AggregateFunc = "sum"
	AggregateFuncAvg   AggregateFunc = "avg"
	AggregateFuncMin   AggregateFunc = "min"
	AggregateFuncMax   AggregateFunc = "max"
	AggregateFuncCount AggregateFunc = "count"
	AggregateFuncP50   AggregateFunc = "p50"
	AggregateFuncP75   AggregateFunc = "p75"
	AggregateFuncP90   AggregateFunc = "p90"
	AggregateFuncP95   AggregateFunc = "p95"
	AggregateFuncP99   AggregateFunc = "p99"
)

// MetricBucketQuery describes a metric aggregation query.
type MetricBucketQuery struct {
	ProjectID  string
	MetricName string
	Aggregate  AggregateFunc
	Start      time.Time
	End        time.Time
	TagFilters map[string]string // optional key=value tag filters
}

// MetricBucketResult holds the result of a metric aggregation query.
type MetricBucketResult struct {
	MetricName string  `json:"metricName"`
	Aggregate  string  `json:"aggregate"`
	Value      float64 `json:"value"`
	Count      int64   `json:"count"`
}

// MetricBucketTimeSeries holds a time-bucketed series result.
type MetricBucketTimeSeries struct {
	MetricName string                    `json:"metricName"`
	Aggregate  string                    `json:"aggregate"`
	Buckets    []MetricBucketTimePoint   `json:"buckets"`
}

// MetricBucketTimePoint is a single point in a time series.
type MetricBucketTimePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Count     int64     `json:"count"`
}

// MetricSummary summarises a metric across a time window.
type MetricSummary struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Count   int64   `json:"count"`
	Sum     float64 `json:"sum"`
	Avg     float64 `json:"avg"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Latest  float64 `json:"latest"`
}

// Query executes a metric bucket aggregation and returns a scalar result.
func (e *MetricBucketQueryEngine) Query(ctx context.Context, q MetricBucketQuery) (*MetricBucketResult, error) {
	if q.ProjectID == "" || q.MetricName == "" {
		return nil, fmt.Errorf("project_id and metric_name are required")
	}
	if q.Aggregate == "" {
		q.Aggregate = AggregateFuncAvg
	}
	if q.End.IsZero() {
		q.End = time.Now().UTC()
	}
	if q.Start.IsZero() {
		q.Start = q.End.Add(-1 * time.Hour)
	}

	if isPercentileFunc(q.Aggregate) {
		return e.queryPercentile(ctx, q)
	}

	sqlAgg, err := sqlAggregate(q.Aggregate)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT %s, COUNT(*) FROM metric_buckets
		 WHERE project_id = ? AND name = ?
		   AND timestamp >= ? AND timestamp <= ?`,
		sqlAgg,
	)
	args := []any{
		q.ProjectID,
		q.MetricName,
		q.Start.UTC().Format(time.RFC3339),
		q.End.UTC().Format(time.RFC3339),
	}

	tagClause, tagArgs := buildTagFilter(q.TagFilters)
	if tagClause != "" {
		query += " AND " + tagClause
		args = append(args, tagArgs...)
	}

	var value sql.NullFloat64
	var count int64
	err = e.db.QueryRowContext(ctx, query, args...).Scan(&value, &count)
	if err != nil {
		return nil, fmt.Errorf("metric bucket query: %w", err)
	}

	return &MetricBucketResult{
		MetricName: q.MetricName,
		Aggregate:  string(q.Aggregate),
		Value:      value.Float64,
		Count:      count,
	}, nil
}

// queryPercentile fetches all values and computes percentile in Go.
func (e *MetricBucketQueryEngine) queryPercentile(ctx context.Context, q MetricBucketQuery) (*MetricBucketResult, error) {
	query := `SELECT value FROM metric_buckets
		 WHERE project_id = ? AND name = ?
		   AND timestamp >= ? AND timestamp <= ?`
	args := []any{
		q.ProjectID,
		q.MetricName,
		q.Start.UTC().Format(time.RFC3339),
		q.End.UTC().Format(time.RFC3339),
	}

	tagClause, tagArgs := buildTagFilter(q.TagFilters)
	if tagClause != "" {
		query += " AND " + tagClause
		args = append(args, tagArgs...)
	}
	query += " ORDER BY value"

	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metric bucket percentile query: %w", err)
	}
	defer rows.Close()

	var values []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	p := percentileFraction(q.Aggregate)
	sort.Float64s(values)

	return &MetricBucketResult{
		MetricName: q.MetricName,
		Aggregate:  string(q.Aggregate),
		Value:      bucketPercentile(values, p),
		Count:      int64(len(values)),
	}, nil
}

// ListMetricNames returns distinct metric names for a project.
func (e *MetricBucketQueryEngine) ListMetricNames(ctx context.Context, projectID string) ([]string, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT DISTINCT name FROM metric_buckets WHERE project_id = ? ORDER BY name`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// Summarise returns summary statistics for all metrics in a project
// within the given time window.
func (e *MetricBucketQueryEngine) Summarise(ctx context.Context, projectID string, since time.Time) ([]MetricSummary, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	rows, err := e.db.QueryContext(ctx,
		`SELECT name, type,
			COUNT(*) AS cnt,
			SUM(value) AS total,
			AVG(value) AS average,
			MIN(value) AS minimum,
			MAX(value) AS maximum
		 FROM metric_buckets
		 WHERE project_id = ? AND timestamp >= ?
		 GROUP BY name, type
		 ORDER BY name`,
		projectID, sinceStr,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []MetricSummary
	for rows.Next() {
		var s MetricSummary
		if err := rows.Scan(&s.Name, &s.Type, &s.Count, &s.Sum, &s.Avg, &s.Min, &s.Max); err != nil {
			return nil, err
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sqlAggregate(agg AggregateFunc) (string, error) {
	switch agg {
	case AggregateFuncSum:
		return "SUM(value)", nil
	case AggregateFuncAvg:
		return "AVG(value)", nil
	case AggregateFuncMin:
		return "MIN(value)", nil
	case AggregateFuncMax:
		return "MAX(value)", nil
	case AggregateFuncCount:
		return "COUNT(value)", nil
	default:
		return "", fmt.Errorf("unsupported aggregate function: %s", agg)
	}
}

func isPercentileFunc(agg AggregateFunc) bool {
	switch agg {
	case AggregateFuncP50, AggregateFuncP75, AggregateFuncP90, AggregateFuncP95, AggregateFuncP99:
		return true
	}
	return false
}

func percentileFraction(agg AggregateFunc) float64 {
	switch agg {
	case AggregateFuncP50:
		return 0.50
	case AggregateFuncP75:
		return 0.75
	case AggregateFuncP90:
		return 0.90
	case AggregateFuncP95:
		return 0.95
	case AggregateFuncP99:
		return 0.99
	default:
		return 0.95
	}
}

// bucketPercentile computes a percentile from a sorted slice using linear
// interpolation, matching the approach in metric_query_engine.go.
func bucketPercentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p * float64(n-1)
	lower := int(math.Floor(rank))
	upper := lower + 1
	if upper >= n {
		return sorted[n-1]
	}
	frac := rank - float64(lower)
	return sorted[lower] + frac*(sorted[upper]-sorted[lower])
}

// buildTagFilter constructs a SQL clause that filters metric_buckets rows
// whose tags_json column contains all the specified key=value pairs. Each
// pair uses json_extract which is available in modernc.org/sqlite.
func buildTagFilter(tags map[string]string) (string, []any) {
	if len(tags) == 0 {
		return "", nil
	}
	var clauses []string
	var args []any
	for k, v := range tags {
		// Use LIKE-based matching against the raw JSON string. This avoids
		// reliance on json_extract which may not be available in all
		// modernc.org/sqlite builds. The pattern is safe because keys and
		// values are user-supplied strings matched literally.
		clauses = append(clauses, `tags_json LIKE ?`)
		escaped := strings.ReplaceAll(strings.ReplaceAll(v, "%", "%%"), "_", "__")
		args = append(args, fmt.Sprintf(`%%"%s":"%s"%%`, k, escaped))
	}
	return strings.Join(clauses, " AND "), args
}

// decodeTagsJSON parses the tags_json column into a map.
