package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"urgentry/internal/discovershared"
)

// ---------------------------------------------------------------------------
// Performance Spans Page — /performance/spans/
// ---------------------------------------------------------------------------

type performanceSpansPageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	DBSpans      []spanSummaryRow
	HTTPSpans    []spanSummaryRow
	OtherSpans   []spanSummaryRow
	Error        string
}

type spanSummaryRow struct {
	Op          string
	Description string
	Count       int64
	AvgMS       string
	P95MS       string
	TotalMS     string
}

func (h *Handler) performanceSpansPage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}

	data := performanceSpansPageData{
		Title:        "Span Summary",
		Nav:          "performance",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}

	timeRange := "24h"
	dur, err := discovershared.ParseDiscoverInterval(timeRange)
	if err != nil {
		data.Error = "Failed to parse time range."
		h.render(w, "performance-spans.html", data)
		return
	}
	since := time.Now().UTC().Add(-dur).Format(time.RFC3339)

	// Aggregate spans grouped by op + description. We compute count, avg,
	// and total_time directly. P95 is approximated by ordering and using
	// a subquery offset.
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT
			COALESCE(op, '(none)') AS span_op,
			COALESCE(description, '(none)') AS span_desc,
			COUNT(*) AS cnt,
			AVG(duration_ms) AS avg_ms,
			SUM(duration_ms) AS total_ms
		 FROM spans
		 WHERE start_timestamp >= ?
		 GROUP BY span_op, span_desc
		 ORDER BY total_ms DESC
		 LIMIT 200`,
		since,
	)
	if err != nil {
		data.Error = "Failed to query spans: " + err.Error()
		h.render(w, "performance-spans.html", data)
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

	// Compute approximate p95 per (op, description) group via a second query.
	// We collect all groups and do a single pass.
	p95Map := make(map[string]float64) // key: "op\x00desc"
	for _, rr := range allRows {
		offset := int64(math.Floor(float64(rr.count) * 0.95))
		if offset > 0 {
			offset--
		}
		var p95 float64
		err := h.db.QueryRowContext(r.Context(),
			`SELECT duration_ms FROM spans
			 WHERE COALESCE(op, '(none)') = ?
			   AND COALESCE(description, '(none)') = ?
			   AND start_timestamp >= ?
			 ORDER BY duration_ms ASC
			 LIMIT 1 OFFSET ?`,
			rr.op, rr.desc, since, offset,
		).Scan(&p95)
		if err == nil {
			p95Map[rr.op+"\x00"+rr.desc] = p95
		}
	}

	// Build summary rows, categorized by op prefix.
	for _, rr := range allRows {
		row := spanSummaryRow{
			Op:          rr.op,
			Description: truncate(rr.desc, 120),
			Count:       rr.count,
			AvgMS:       fmt.Sprintf("%.1f", rr.avgMS),
			TotalMS:     fmt.Sprintf("%.0f", rr.totalMS),
		}
		if v, ok := p95Map[rr.op+"\x00"+rr.desc]; ok {
			row.P95MS = fmt.Sprintf("%.1f", v)
		} else {
			row.P95MS = "-"
		}

		switch {
		case isDBOp(rr.op):
			data.DBSpans = append(data.DBSpans, row)
		case isHTTPOp(rr.op):
			data.HTTPSpans = append(data.HTTPSpans, row)
		default:
			data.OtherSpans = append(data.OtherSpans, row)
		}
	}

	// Sort each category by total time descending (already ordered by query,
	// but re-sort within categories to be safe).
	sortSpans := func(rows []spanSummaryRow) {
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].TotalMS > rows[j].TotalMS
		})
	}
	sortSpans(data.DBSpans)
	sortSpans(data.HTTPSpans)
	sortSpans(data.OtherSpans)

	// Cap to top 50 per category.
	if len(data.DBSpans) > 50 {
		data.DBSpans = data.DBSpans[:50]
	}
	if len(data.HTTPSpans) > 50 {
		data.HTTPSpans = data.HTTPSpans[:50]
	}
	if len(data.OtherSpans) > 50 {
		data.OtherSpans = data.OtherSpans[:50]
	}

	h.render(w, "performance-spans.html", data)
}

func isDBOp(op string) bool {
	for _, prefix := range []string{"db", "db.", "cache", "cache.", "redis", "sql"} {
		if op == prefix || len(op) > len(prefix) && op[:len(prefix)+1] == prefix+"." {
			return true
		}
		if op == prefix {
			return true
		}
	}
	return false
}

func isHTTPOp(op string) bool {
	for _, prefix := range []string{"http", "http.", "grpc", "grpc."} {
		if op == prefix || len(op) > len(prefix) && op[:len(prefix)+1] == prefix+"." {
			return true
		}
		if op == prefix {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
