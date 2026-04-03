package api

import "testing"

func TestMetricFromAggregate(t *testing.T) {
	tests := []struct {
		aggregate string
		want      string
	}{
		{"count()", "error_count"},
		{"count_unique(user)", "error_count"},
		{"p95(transaction.duration)", "p95_latency"},
		{"p50(transaction.duration)", "p95_latency"},
		{"p75(transaction.duration)", "p95_latency"},
		{"p99(transaction.duration)", "p95_latency"},
		{"percentile(transaction.duration, 0.95)", "p95_latency"},
		{"failure_rate()", "failure_rate"},
		{"apdex(300)", "apdex"},
		{"count(transaction.id)", "transaction_count"},
		{"avg(transaction.duration)", "custom_metric"},
		{"sum(transaction.duration)", "custom_metric"},
		{"max(transaction.duration)", "custom_metric"},
		{"min(transaction.duration)", "custom_metric"},
		{"", "error_count"},
		{"unknown_aggregate", "error_count"},
		{"P95(transaction.duration)", "p95_latency"},
		{"  Failure_Rate()  ", "failure_rate"},
	}
	for _, tt := range tests {
		got := metricFromAggregate(tt.aggregate)
		if got != tt.want {
			t.Errorf("metricFromAggregate(%q) = %q, want %q", tt.aggregate, got, tt.want)
		}
	}
}
