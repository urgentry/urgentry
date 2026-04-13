package web

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/sqlite"
)

type metricsPageData struct {
	Title          string
	Nav            string
	Environment    string
	Environments   []string
	Error          string
	Metrics        []sqlite.MetricSummary
	MetricNames    []string
	SelectedMetric string
	Aggregate      string
	ChartResult    *sqlite.MetricBucketResult
}

func (h *Handler) metricsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	engine := sqlite.NewMetricBucketQueryEngine(h.db)

	// Default: list all metrics from the last hour.
	since := time.Now().UTC().Add(-1 * time.Hour)
	summaries, err := engine.Summarise(ctx, "", since)
	if err != nil {
		h.render(w, "metrics.html", metricsPageData{
			Title: "Metrics",
			Nav:   "metrics",
			Error: "Failed to load metrics: " + err.Error(),
		})
		return
	}

	names, _ := engine.ListMetricNames(ctx, "")

	// If a metric is selected via query param, run an aggregation query.
	selectedMetric := strings.TrimSpace(r.URL.Query().Get("metric"))
	aggregate := strings.TrimSpace(r.URL.Query().Get("aggregate"))
	if aggregate == "" {
		aggregate = "avg"
	}

	var chartResult *sqlite.MetricBucketResult
	if selectedMetric != "" {
		q := sqlite.MetricBucketQuery{
			MetricName: selectedMetric,
			Aggregate:  sqlite.AggregateFunc(aggregate),
			Start:      since,
			End:        time.Now().UTC(),
		}
		chartResult, _ = engine.Query(ctx, q)
	}

	h.render(w, "metrics.html", metricsPageData{
		Title:          "Metrics",
		Nav:            "metrics",
		Environment:    readSelectedEnvironment(r),
		Environments:   h.loadEnvironments(ctx),
		Metrics:        summaries,
		MetricNames:    names,
		SelectedMetric: selectedMetric,
		Aggregate:      aggregate,
		ChartResult:    chartResult,
	})
}
