package web

import (
	"fmt"
	"net/http"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discovershared"
)

// ---------------------------------------------------------------------------
// Explore Umbrella Page — /explore/
// ---------------------------------------------------------------------------

type explorePageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
}

func (h *Handler) explorePage(w http.ResponseWriter, r *http.Request) {
	data := explorePageData{
		Title:        "Explore",
		Nav:          "explore",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}
	h.render(w, "explore.html", data)
}

// ---------------------------------------------------------------------------
// Insights Hub Page — /insights/
// ---------------------------------------------------------------------------

type insightsPageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
}

func (h *Handler) insightsPage(w http.ResponseWriter, r *http.Request) {
	data := insightsPageData{
		Title:        "Insights",
		Nav:          "insights",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}
	h.render(w, "insights.html", data)
}

// ---------------------------------------------------------------------------
// Insights HTTP Page — /insights/http/
// ---------------------------------------------------------------------------

type insightsHTTPRow struct {
	Transaction string
	Count       string
	P50         string
	P95         string
	Avg         string
	ErrorRate   string
}

type insightsHTTPPageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Rows         []insightsHTTPRow
	Error        string
}

func (h *Handler) insightsHTTPPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}

	data := insightsHTTPPageData{
		Title:        "HTTP Performance",
		Nav:          "insights",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}

	timeRange := "24h"

	// Top transactions by p95 latency using the discover query service.
	topQuery, err := buildDiscoverQuery(scope.OrganizationSlug, discoverBuilderState{
		Dataset:       string(discover.DatasetTransactions),
		Aggregate:     "count, p50(duration.ms), p95(duration.ms), avg(duration.ms)",
		GroupBy:       "transaction",
		OrderBy:       "-p95",
		Visualization: "table",
		TimeRange:     timeRange,
	}, 25)
	if err != nil {
		data.Error = "Failed to build HTTP insights query: " + err.Error()
		h.render(w, "insights-http.html", data)
		return
	}
	result, err := h.queries.ExecuteTable(r.Context(), topQuery)
	if err != nil {
		data.Error = "Failed to query HTTP transactions: " + err.Error()
		h.render(w, "insights-http.html", data)
		return
	}

	// Compute per-transaction error rates via direct SQL.
	dur, parseErr := discovershared.ParseDiscoverInterval(timeRange)
	errorRateByName := make(map[string]float64)
	if parseErr == nil {
		since := time.Now().UTC().Add(-dur).Format(time.RFC3339)
		rows, qErr := h.db.QueryContext(r.Context(),
			`SELECT transaction_name,
				COUNT(*) AS total,
				SUM(CASE WHEN status NOT IN ('ok', 'cancelled', '') THEN 1 ELSE 0 END) AS errors
			 FROM transactions
			 WHERE start_timestamp >= ?
			 GROUP BY transaction_name`,
			since,
		)
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				var total, errors int64
				if err := rows.Scan(&name, &total, &errors); err != nil {
					continue
				}
				if total > 0 {
					errorRateByName[name] = float64(errors) / float64(total) * 100
				}
			}
		}
	}

	for _, row := range result.Rows {
		txName := formatDiscoverValue(row["transaction"])
		hr := insightsHTTPRow{
			Transaction: txName,
			Count:       formatDiscoverValue(row["count"]),
			P50:         formatDiscoverValue(row["p50"]),
			P95:         formatDiscoverValue(row["p95"]),
			Avg:         formatDiscoverValue(row["avg"]),
		}
		if rate, ok := errorRateByName[txName]; ok {
			hr.ErrorRate = fmt.Sprintf("%.1f%%", rate)
		} else {
			hr.ErrorRate = "0.0%"
		}
		data.Rows = append(data.Rows, hr)
	}

	h.render(w, "insights-http.html", data)
}

// ---------------------------------------------------------------------------
// Insights Database Page — /insights/database/
// ---------------------------------------------------------------------------

type insightsDBRow struct {
	Op          string
	Description string
	Count       int64
	AvgMS       string
	P95MS       string
	TotalMS     string
}

type insightsDBPageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Rows         []insightsDBRow
	Error        string
}

func (h *Handler) insightsDatabasePage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}

	data := insightsDBPageData{
		Title:        "Database Insights",
		Nav:          "insights",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}

	timeRange := "24h"
	dur, err := discovershared.ParseDiscoverInterval(timeRange)
	if err != nil {
		data.Error = "Failed to parse time range."
		h.render(w, "insights-database.html", data)
		return
	}
	since := time.Now().UTC().Add(-dur).Format(time.RFC3339)

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT
			COALESCE(op, '(none)') AS span_op,
			COALESCE(description, '(none)') AS span_desc,
			COUNT(*) AS cnt,
			AVG(duration_ms) AS avg_ms,
			SUM(duration_ms) AS total_ms
		 FROM spans
		 WHERE start_timestamp >= ?
		   AND (LOWER(COALESCE(op, '')) LIKE 'db%'
		     OR LOWER(COALESCE(op, '')) LIKE 'sql%'
		     OR LOWER(COALESCE(op, '')) LIKE 'cache%'
		     OR LOWER(COALESCE(op, '')) LIKE 'redis%')
		 GROUP BY span_op, span_desc
		 ORDER BY total_ms DESC
		 LIMIT 100`,
		since,
	)
	if err != nil {
		data.Error = "Failed to query database spans: " + err.Error()
		h.render(w, "insights-database.html", data)
		return
	}
	defer rows.Close()

	type rawRow struct {
		op      string
		desc    string
		count   int64
		avgMS   float64
		totalMS float64
	}
	var allRows []rawRow
	for rows.Next() {
		var rr rawRow
		if err := rows.Scan(&rr.op, &rr.desc, &rr.count, &rr.avgMS, &rr.totalMS); err != nil {
			continue
		}
		allRows = append(allRows, rr)
	}

	// Compute approximate p95 per group.
	for _, rr := range allRows {
		offset := int64(float64(rr.count) * 0.95)
		if offset > 0 {
			offset--
		}
		var p95 float64
		p95Err := h.db.QueryRowContext(r.Context(),
			`SELECT duration_ms FROM spans
			 WHERE COALESCE(op, '(none)') = ?
			   AND COALESCE(description, '(none)') = ?
			   AND start_timestamp >= ?
			 ORDER BY duration_ms ASC
			 LIMIT 1 OFFSET ?`,
			rr.op, rr.desc, since, offset,
		).Scan(&p95)

		dbRow := insightsDBRow{
			Op:          rr.op,
			Description: truncate(rr.desc, 120),
			Count:       rr.count,
			AvgMS:       fmt.Sprintf("%.1f", rr.avgMS),
			TotalMS:     fmt.Sprintf("%.0f", rr.totalMS),
		}
		if p95Err == nil {
			dbRow.P95MS = fmt.Sprintf("%.1f", p95)
		} else {
			dbRow.P95MS = "-"
		}
		data.Rows = append(data.Rows, dbRow)
	}

	h.render(w, "insights-database.html", data)
}

// ---------------------------------------------------------------------------
// Performance Summary Page — /performance/summary/
// ---------------------------------------------------------------------------

type performanceSummarySpan struct {
	Op          string
	Description string
	Count       int64
	AvgMS       string
	P95MS       string
	TotalMS     string
}

type performanceSummaryPageData struct {
	Title           string
	Nav             string
	Environment     string
	Environments    []string
	TransactionName string
	Count           string
	P50             string
	P95             string
	Avg             string
	MaxDuration     string
	Apdex           string
	ApdexRank       string
	Spans           []performanceSummarySpan
	Error           string
}

func (h *Handler) performanceSummaryPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}

	txName := r.URL.Query().Get("transaction")
	data := performanceSummaryPageData{
		Title:           "Transaction Summary",
		Nav:             "performance",
		Environment:     readSelectedEnvironment(r),
		Environments:    h.loadEnvironments(r.Context()),
		TransactionName: txName,
	}
	if txName == "" {
		data.Error = "Pick a transaction from Performance to see its summary."
		h.render(w, "performance-summary.html", data)
		return
	}

	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}

	timeRange := "24h"

	// Aggregate stats for the specific transaction.
	statsQuery, err := buildDiscoverQuery(scope.OrganizationSlug, discoverBuilderState{
		Dataset:       string(discover.DatasetTransactions),
		Filter:        "transaction:" + txName,
		Aggregate:     "count, p50(duration.ms), p95(duration.ms), avg(duration.ms), max(duration.ms)",
		Visualization: "table",
		TimeRange:     timeRange,
	}, 1)
	if err != nil {
		data.Error = "Failed to build transaction stats query: " + err.Error()
		h.render(w, "performance-summary.html", data)
		return
	}
	statsResult, err := h.queries.ExecuteTable(r.Context(), statsQuery)
	if err != nil {
		data.Error = "Failed to query transaction stats: " + err.Error()
		h.render(w, "performance-summary.html", data)
		return
	}
	if len(statsResult.Rows) > 0 {
		row := statsResult.Rows[0]
		data.Count = formatDiscoverValue(row["count"])
		data.P50 = formatDiscoverValue(row["p50"])
		data.P95 = formatDiscoverValue(row["p95"])
		data.Avg = formatDiscoverValue(row["avg"])
		data.MaxDuration = formatDiscoverValue(row["max"])
	}

	// Compute Apdex via direct SQL.
	dur, parseErr := discovershared.ParseDiscoverInterval(timeRange)
	if parseErr == nil {
		since := time.Now().UTC().Add(-dur).Format(time.RFC3339)
		frustrated := apdexThresholdMs * 4
		var total, satisfied, tolerating int64
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) AS total,
				SUM(CASE WHEN duration_ms < ? THEN 1 ELSE 0 END) AS satisfied,
				SUM(CASE WHEN duration_ms >= ? AND duration_ms < ? THEN 1 ELSE 0 END) AS tolerating
			 FROM transactions
			 WHERE transaction_name = ? AND start_timestamp >= ?`,
			apdexThresholdMs, apdexThresholdMs, frustrated, txName, since,
		).Scan(&total, &satisfied, &tolerating)
		if total > 0 {
			score := (float64(satisfied) + float64(tolerating)/2) / float64(total)
			data.Apdex = fmt.Sprintf("%.2f", score)
			data.ApdexRank = apdexRank(score)
		}

		// Span breakdown for this transaction.
		spanRows, qErr := h.db.QueryContext(r.Context(),
			`SELECT
				COALESCE(s.op, '(none)') AS span_op,
				COALESCE(s.description, '(none)') AS span_desc,
				COUNT(*) AS cnt,
				AVG(s.duration_ms) AS avg_ms,
				SUM(s.duration_ms) AS total_ms
			 FROM spans s
			 JOIN transactions t ON t.trace_id = s.trace_id
			 WHERE t.transaction_name = ?
			   AND t.start_timestamp >= ?
			 GROUP BY span_op, span_desc
			 ORDER BY total_ms DESC
			 LIMIT 50`,
			txName, since,
		)
		if qErr == nil {
			defer spanRows.Close()
			type rawSpan struct {
				op      string
				desc    string
				count   int64
				avgMS   float64
				totalMS float64
			}
			var rawSpans []rawSpan
			for spanRows.Next() {
				var rs rawSpan
				if err := spanRows.Scan(&rs.op, &rs.desc, &rs.count, &rs.avgMS, &rs.totalMS); err != nil {
					continue
				}
				rawSpans = append(rawSpans, rs)
			}
			for _, rs := range rawSpans {
				offset := int64(float64(rs.count) * 0.95)
				if offset > 0 {
					offset--
				}
				var p95 float64
				p95Err := h.db.QueryRowContext(r.Context(),
					`SELECT s.duration_ms FROM spans s
					 JOIN transactions t ON t.trace_id = s.trace_id
					 WHERE t.transaction_name = ?
					   AND COALESCE(s.op, '(none)') = ?
					   AND COALESCE(s.description, '(none)') = ?
					   AND t.start_timestamp >= ?
					 ORDER BY s.duration_ms ASC
					 LIMIT 1 OFFSET ?`,
					txName, rs.op, rs.desc, since, offset,
				).Scan(&p95)
				sp := performanceSummarySpan{
					Op:          rs.op,
					Description: truncate(rs.desc, 120),
					Count:       rs.count,
					AvgMS:       fmt.Sprintf("%.1f", rs.avgMS),
					TotalMS:     fmt.Sprintf("%.0f", rs.totalMS),
				}
				if p95Err == nil {
					sp.P95MS = fmt.Sprintf("%.1f", p95)
				} else {
					sp.P95MS = "-"
				}
				data.Spans = append(data.Spans, sp)
			}
		}
	}

	h.render(w, "performance-summary.html", data)
}
