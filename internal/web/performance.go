package web

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/discovershared"
)

// ---------------------------------------------------------------------------
// Performance Overview Page
// ---------------------------------------------------------------------------

type performancePageData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	Throughput   string
	OverallP50   string
	OverallP95   string
	OverallMax   string
	Transactions []performanceTransactionRow
	WebVitals    []webVitalSummary
	Error        string
}

// webVitalSummary holds aggregate Web Vital scores for a page/transaction.
type webVitalSummary struct {
	Page       string
	LCP        string // formatted value
	LCPRating  string // "good", "needs-improvement", "poor"
	CLS        string
	CLSRating  string
	INP        string
	INPRating  string
	TTFB       string
	TTFBRating string
	FCP        string
	FCPRating  string
	Count      int
}

type performanceTransactionRow struct {
	Name      string
	Count     string
	P95       string
	Avg       string
	PrevCount string
	PrevP95   string
	PrevAvg   string
	DeltaP95  string // e.g. "+12.3%" or "-5.1%"
	DeltaAvg  string
	Apdex     string // e.g. "0.92"
	ApdexRank string // "excellent", "good", "fair", "poor", "unacceptable"
}

// previousTimeRange returns the absolute time window that precedes the given
// relative range. For example, "24h" returns the window from 48h ago to 24h ago.
func previousTimeRange(relativeRange string) (*discover.TimeRange, error) {
	dur, err := discovershared.ParseDiscoverInterval(relativeRange)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	end := now.Add(-dur)
	start := end.Add(-dur)
	return &discover.TimeRange{
		Kind:  "absolute",
		Start: start.Format(time.RFC3339),
		End:   end.Format(time.RFC3339),
	}, nil
}

// computeDelta returns a formatted percentage change string like "+12.3%" or
// "-5.1%". Returns "" if either value is not a valid number.
func computeDelta(current, previous string) string {
	cur, err := strconv.ParseFloat(current, 64)
	if err != nil || cur == 0 {
		return ""
	}
	prev, err := strconv.ParseFloat(previous, 64)
	if err != nil || prev == 0 {
		return ""
	}
	pct := ((cur - prev) / prev) * 100
	if math.Abs(pct) < 0.05 {
		return "0.0%"
	}
	if pct > 0 {
		return fmt.Sprintf("+%.1f%%", pct)
	}
	return fmt.Sprintf("%.1f%%", pct)
}

// computeApdex queries the transactions table directly to compute Apdex
// for each named transaction. Apdex = (satisfied + tolerating/2) / total.
// satisfied: duration_ms < threshold, tolerating: threshold <= duration_ms < 4*threshold.
const apdexThresholdMs = 300

func apdexRank(score float64) string {
	switch {
	case score >= 0.94:
		return "excellent"
	case score >= 0.85:
		return "good"
	case score >= 0.70:
		return "fair"
	case score >= 0.50:
		return "poor"
	default:
		return "unacceptable"
	}
}

func (h *Handler) performancePage(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	scope, err := h.defaultPageScope(r.Context())
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve default organization scope.")
		return
	}

	data := performancePageData{
		Title:        "Performance",
		Nav:          "performance",
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(r.Context()),
	}

	timeRange := "24h"

	// 1) Top 10 transactions by p95, grouped by transaction name.
	topQuery, err := buildDiscoverQuery(scope.OrganizationSlug, discoverBuilderState{
		Dataset:       string(discover.DatasetTransactions),
		Aggregate:     "count, p95(duration.ms), avg(duration.ms)",
		GroupBy:       "transaction",
		OrderBy:       "-p95",
		Visualization: "table",
		TimeRange:     timeRange,
	}, 10)
	if err != nil {
		data.Error = "Failed to build top transactions query: " + err.Error()
		h.render(w, "performance.html", data)
		return
	}
	topResult, err := h.queries.ExecuteTable(r.Context(), topQuery)
	if err != nil {
		data.Error = "Failed to query top transactions: " + err.Error()
		h.render(w, "performance.html", data)
		return
	}
	for _, row := range topResult.Rows {
		data.Transactions = append(data.Transactions, performanceTransactionRow{
			Name:  formatDiscoverValue(row["transaction"]),
			Count: formatDiscoverValue(row["count"]),
			P95:   formatDiscoverValue(row["p95"]),
			Avg:   formatDiscoverValue(row["avg"]),
		})
	}

	// 2) Previous-period comparison: same query but for the preceding time window.
	prevTR, err := previousTimeRange(timeRange)
	if err == nil {
		prevQuery, err := buildDiscoverQuery(scope.OrganizationSlug, discoverBuilderState{
			Dataset:       string(discover.DatasetTransactions),
			Aggregate:     "count, p95(duration.ms), avg(duration.ms)",
			GroupBy:       "transaction",
			OrderBy:       "-p95",
			Visualization: "table",
			TimeRange:     timeRange, // will be overwritten below
		}, 10)
		if err == nil {
			prevQuery.TimeRange = prevTR
			prevResult, err := h.queries.ExecuteTable(r.Context(), prevQuery)
			if err == nil {
				prevByName := make(map[string]discover.TableRow, len(prevResult.Rows))
				for _, row := range prevResult.Rows {
					name := formatDiscoverValue(row["transaction"])
					prevByName[name] = row
				}
				for i := range data.Transactions {
					tx := &data.Transactions[i]
					if prev, ok := prevByName[tx.Name]; ok {
						tx.PrevCount = formatDiscoverValue(prev["count"])
						tx.PrevP95 = formatDiscoverValue(prev["p95"])
						tx.PrevAvg = formatDiscoverValue(prev["avg"])
						tx.DeltaP95 = computeDelta(tx.P95, tx.PrevP95)
						tx.DeltaAvg = computeDelta(tx.Avg, tx.PrevAvg)
					}
				}
			}
		}
	}

	// 3) Apdex per transaction via direct SQL.
	// T=300ms. Satisfied: <T, Tolerating: T..4T, Frustrated: >=4T.
	// Apdex = (satisfied + tolerating/2) / total
	if len(data.Transactions) > 0 {
		dur, parseErr := discovershared.ParseDiscoverInterval(timeRange)
		if parseErr == nil {
			since := time.Now().UTC().Add(-dur).Format(time.RFC3339)
			frustrated := apdexThresholdMs * 4
			rows, qErr := h.db.QueryContext(r.Context(),
				`SELECT transaction_name,
					COUNT(*) AS total,
					SUM(CASE WHEN duration_ms < ? THEN 1 ELSE 0 END) AS satisfied,
					SUM(CASE WHEN duration_ms >= ? AND duration_ms < ? THEN 1 ELSE 0 END) AS tolerating
				 FROM transactions
				 WHERE start_timestamp >= ?
				 GROUP BY transaction_name`,
				apdexThresholdMs, apdexThresholdMs, frustrated, since,
			)
			if qErr == nil {
				defer rows.Close()
				apdexByName := make(map[string]float64)
				for rows.Next() {
					var name string
					var total, satisfied, tolerating int64
					if err := rows.Scan(&name, &total, &satisfied, &tolerating); err != nil {
						continue
					}
					if total > 0 {
						apdexByName[name] = (float64(satisfied) + float64(tolerating)/2) / float64(total)
					}
				}
				for i := range data.Transactions {
					tx := &data.Transactions[i]
					if score, ok := apdexByName[tx.Name]; ok {
						tx.Apdex = fmt.Sprintf("%.2f", score)
						tx.ApdexRank = apdexRank(score)
					}
				}
			}
		}
	}

	// 4) Overall throughput and latency percentiles (multi-aggregate, table mode).
	overallQuery, err := buildDiscoverQuery(scope.OrganizationSlug, discoverBuilderState{
		Dataset:       string(discover.DatasetTransactions),
		Aggregate:     "count, p50(duration.ms), p95(duration.ms), max(duration.ms)",
		Visualization: "table",
		TimeRange:     timeRange,
	}, 1)
	if err != nil {
		data.Error = "Failed to build overall stats query: " + err.Error()
		h.render(w, "performance.html", data)
		return
	}
	overallResult, err := h.queries.ExecuteTable(r.Context(), overallQuery)
	if err != nil {
		data.Error = "Failed to query overall stats: " + err.Error()
		h.render(w, "performance.html", data)
		return
	}
	if len(overallResult.Rows) > 0 {
		row := overallResult.Rows[0]
		data.Throughput = formatDiscoverValue(row["count"])
		data.OverallP50 = formatDiscoverValue(row["p50"])
		data.OverallP95 = formatDiscoverValue(row["p95"])
		data.OverallMax = formatDiscoverValue(row["max"])
	}

	// 5) Web Vitals: extract LCP, CLS, INP, TTFB, FCP from measurements_json.
	{
		dur, parseErr := discovershared.ParseDiscoverInterval(timeRange)
		if parseErr == nil {
			since := time.Now().UTC().Add(-dur).Format(time.RFC3339)
			rows, qErr := h.db.QueryContext(r.Context(),
				`SELECT transaction_name,
					COUNT(*) AS cnt,
					AVG(json_extract(measurements_json, '$.lcp.value')) AS avg_lcp,
					AVG(json_extract(measurements_json, '$.cls.value')) AS avg_cls,
					AVG(json_extract(measurements_json, '$.inp.value')) AS avg_inp,
					AVG(json_extract(measurements_json, '$.ttfb.value')) AS avg_ttfb,
					AVG(json_extract(measurements_json, '$.fcp.value')) AS avg_fcp
				 FROM transactions
				 WHERE start_timestamp >= ?
				   AND (json_extract(measurements_json, '$.lcp.value') IS NOT NULL
				     OR json_extract(measurements_json, '$.cls.value') IS NOT NULL
				     OR json_extract(measurements_json, '$.fcp.value') IS NOT NULL)
				 GROUP BY transaction_name
				 ORDER BY cnt DESC
				 LIMIT 20`,
				since,
			)
			if qErr == nil {
				defer rows.Close()
				for rows.Next() {
					var (
						page                                    string
						cnt                                     int
						lcpVal, clsVal, inpVal, ttfbVal, fcpVal sql.NullFloat64
					)
					if err := rows.Scan(&page, &cnt, &lcpVal, &clsVal, &inpVal, &ttfbVal, &fcpVal); err != nil {
						continue
					}
					wv := webVitalSummary{Page: page, Count: cnt}
					if lcpVal.Valid {
						wv.LCP = fmt.Sprintf("%.0f", lcpVal.Float64)
						wv.LCPRating = lcpRating(lcpVal.Float64)
					}
					if clsVal.Valid {
						wv.CLS = fmt.Sprintf("%.3f", clsVal.Float64)
						wv.CLSRating = clsRating(clsVal.Float64)
					}
					if inpVal.Valid {
						wv.INP = fmt.Sprintf("%.0f", inpVal.Float64)
						wv.INPRating = inpRating(inpVal.Float64)
					}
					if ttfbVal.Valid {
						wv.TTFB = fmt.Sprintf("%.0f", ttfbVal.Float64)
						wv.TTFBRating = ttfbRating(ttfbVal.Float64)
					}
					if fcpVal.Valid {
						wv.FCP = fmt.Sprintf("%.0f", fcpVal.Float64)
						wv.FCPRating = fcpRating(fcpVal.Float64)
					}
					data.WebVitals = append(data.WebVitals, wv)
				}
			}
		}
	}

	h.render(w, "performance.html", data)
}

// Web Vital rating functions based on Core Web Vitals thresholds.
// LCP: good <2500ms, needs-improvement <4000ms, poor >=4000ms
func lcpRating(ms float64) string {
	switch {
	case ms < 2500:
		return "good"
	case ms < 4000:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// CLS: good <0.1, needs-improvement <0.25, poor >=0.25
func clsRating(v float64) string {
	switch {
	case v < 0.1:
		return "good"
	case v < 0.25:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// INP: good <200ms, needs-improvement <500ms, poor >=500ms
func inpRating(ms float64) string {
	switch {
	case ms < 200:
		return "good"
	case ms < 500:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// TTFB: good <800ms, needs-improvement <1800ms, poor >=1800ms
func ttfbRating(ms float64) string {
	switch {
	case ms < 800:
		return "good"
	case ms < 1800:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// FCP: good <1800ms, needs-improvement <3000ms, poor >=3000ms
func fcpRating(ms float64) string {
	switch {
	case ms < 1800:
		return "good"
	case ms < 3000:
		return "needs-improvement"
	default:
		return "poor"
	}
}
