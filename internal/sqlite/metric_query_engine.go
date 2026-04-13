package sqlite

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"
)

// MetricQueryEngine implements alert.MetricQueryEngine against the SQLite
// events and transactions tables.
type MetricQueryEngine struct {
	db *sql.DB
}

// NewMetricQueryEngine creates a new SQLite-backed metric query engine.
func NewMetricQueryEngine(db *sql.DB) *MetricQueryEngine {
	return &MetricQueryEngine{db: db}
}

// CountEvents returns the count of error events for a project since the given
// time, optionally filtered by environment.
func (e *MetricQueryEngine) CountEvents(ctx context.Context, projectID string, since time.Time, environment string) (int64, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	if environment != "" {
		var count int64
		err := e.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events
			  WHERE project_id = ? AND COALESCE(event_type, 'error') = 'error'
			    AND ingested_at >= ? AND environment = ?`,
			projectID, sinceStr, environment,
		).Scan(&count)
		return count, err
	}
	var count int64
	err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		  WHERE project_id = ? AND COALESCE(event_type, 'error') = 'error'
		    AND ingested_at >= ?`,
		projectID, sinceStr,
	).Scan(&count)
	return count, err
}

// CountTransactions returns the count of transactions for a project since the
// given time, optionally filtered by environment.
func (e *MetricQueryEngine) CountTransactions(ctx context.Context, projectID string, since time.Time, environment string) (int64, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	if environment != "" {
		var count int64
		err := e.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM transactions
			  WHERE project_id = ? AND created_at >= ? AND environment = ?`,
			projectID, sinceStr, environment,
		).Scan(&count)
		return count, err
	}
	var count int64
	err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions
		  WHERE project_id = ? AND created_at >= ?`,
		projectID, sinceStr,
	).Scan(&count)
	return count, err
}

// P95Latency returns the 95th-percentile duration_ms for transactions in the
// given time window. SQLite doesn't have a native percentile function, so we
// fetch all durations and compute it in Go. Returns 0 when no transactions
// exist.
func (e *MetricQueryEngine) P95Latency(ctx context.Context, projectID string, since time.Time, environment string) (float64, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	var query string
	var args []any
	if environment != "" {
		query = `SELECT duration_ms FROM transactions
		          WHERE project_id = ? AND created_at >= ? AND environment = ?
		          ORDER BY duration_ms`
		args = []any{projectID, sinceStr, environment}
	} else {
		query = `SELECT duration_ms FROM transactions
		          WHERE project_id = ? AND created_at >= ?
		          ORDER BY duration_ms`
		args = []any{projectID, sinceStr}
	}

	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var durations []float64
	for rows.Next() {
		var d float64
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		durations = append(durations, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(durations) == 0 {
		return 0, nil
	}

	sort.Float64s(durations)
	return percentile(durations, 0.95), nil
}

// FailureRate returns the ratio of failed transactions (status not in
// ("ok","")) to total transactions. Returns 0 when no transactions exist.
func (e *MetricQueryEngine) FailureRate(ctx context.Context, projectID string, since time.Time, environment string) (float64, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	var total, failed int64
	if environment != "" {
		err := e.db.QueryRowContext(ctx,
			`SELECT COUNT(*),
			        SUM(CASE WHEN LOWER(COALESCE(status, '')) NOT IN ('ok', '') THEN 1 ELSE 0 END)
			   FROM transactions
			  WHERE project_id = ? AND created_at >= ? AND environment = ?`,
			projectID, sinceStr, environment,
		).Scan(&total, &failed)
		if err != nil {
			return 0, err
		}
	} else {
		err := e.db.QueryRowContext(ctx,
			`SELECT COUNT(*),
			        SUM(CASE WHEN LOWER(COALESCE(status, '')) NOT IN ('ok', '') THEN 1 ELSE 0 END)
			   FROM transactions
			  WHERE project_id = ? AND created_at >= ?`,
			projectID, sinceStr,
		).Scan(&total, &failed)
		if err != nil {
			return 0, err
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(failed) / float64(total), nil
}

// CustomMetricValue returns the average value for a named custom metric
// from the metric_buckets table in the given time window. Returns 0 if there
// are no matching buckets.
func (e *MetricQueryEngine) CustomMetricValue(ctx context.Context, projectID string, metricName string, since time.Time, environment string) (float64, error) {
	sinceStr := since.UTC().Format(time.RFC3339)

	query := `SELECT AVG(value) FROM metric_buckets
	          WHERE project_id = ? AND name = ? AND timestamp >= ?`
	args := []any{projectID, metricName, sinceStr}

	if environment != "" {
		query += ` AND tags_json LIKE ?`
		args = append(args, `%"environment":"`+environment+`"%`)
	}

	var avg sql.NullFloat64
	err := e.db.QueryRowContext(ctx, query, args...).Scan(&avg)
	if err != nil {
		return 0, err
	}
	return avg.Float64, nil
}

// percentile computes the p-th percentile of a sorted slice using linear
// interpolation (the "inclusive" method).
func percentile(sorted []float64, p float64) float64 {
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
