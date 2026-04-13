package alert

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Metric type constants used in MetricAlertRule.Metric.
const (
	MetricErrorCount       = "error_count"
	MetricTransactionCount = "transaction_count"
	MetricP95Latency       = "p95_latency"
	MetricFailureRate      = "failure_rate"
	MetricCustom           = "custom_metric"
)

// MetricQueryEngine abstracts the aggregate queries needed by the metric alert
// evaluator. Implementations may run against SQLite, Postgres, or any other
// telemetry backend.
type MetricQueryEngine interface {
	// CountEvents returns the number of error events for a project in the given
	// time window, optionally filtered by environment.
	CountEvents(ctx context.Context, projectID string, since time.Time, environment string) (int64, error)

	// CountTransactions returns the number of transactions for a project in the
	// given time window, optionally filtered by environment.
	CountTransactions(ctx context.Context, projectID string, since time.Time, environment string) (int64, error)

	// P95Latency returns the 95th-percentile duration_ms for transactions in
	// the given time window, optionally filtered by environment. Returns 0 if
	// there are no transactions.
	P95Latency(ctx context.Context, projectID string, since time.Time, environment string) (float64, error)

	// FailureRate returns the ratio of failed transactions (status not in
	// ("ok","")) to total transactions in the given time window. Returns 0 if
	// there are no transactions.
	FailureRate(ctx context.Context, projectID string, since time.Time, environment string) (float64, error)

	// CustomMetricValue returns the average value for a named custom metric
	// bucket in the given time window. Returns 0 if there are no matching
	// buckets.
	CustomMetricValue(ctx context.Context, projectID string, metricName string, since time.Time, environment string) (float64, error)
}

// MetricAlertEvaluator evaluates all active metric alert rules against
// current telemetry data on a periodic basis.
type MetricAlertEvaluator struct {
	Store  MetricAlertRuleStore
	Engine MetricQueryEngine
}

// EvaluateAll lists every active metric alert rule, queries the relevant
// metric for each, and returns state transitions (ok->triggered or
// triggered->resolved). The caller is responsible for persisting state
// changes and dispatching notifications.
func (e *MetricAlertEvaluator) EvaluateAll(ctx context.Context) ([]MetricAlertTransition, error) {
	if e.Store == nil || e.Engine == nil {
		return nil, nil
	}

	rules, err := e.Store.ListAllActiveMetricAlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active metric alert rules: %w", err)
	}

	var transitions []MetricAlertTransition
	now := time.Now().UTC()

	for _, rule := range rules {
		value, qErr := e.queryMetric(ctx, rule, now)
		if qErr != nil {
			log.Warn().
				Err(qErr).
				Str("rule_id", rule.ID).
				Str("metric", rule.Metric).
				Msg("metric alert query failed")
			continue
		}

		transition := evaluateMetricRule(rule, value, now)
		if transition != nil {
			transitions = append(transitions, *transition)
		}
	}

	return transitions, nil
}

func (e *MetricAlertEvaluator) queryMetric(ctx context.Context, rule *MetricAlertRule, now time.Time) (float64, error) {
	since := now.Add(-time.Duration(rule.TimeWindowSecs) * time.Second)
	env := strings.TrimSpace(rule.Environment)

	switch rule.Metric {
	case MetricErrorCount:
		count, err := e.Engine.CountEvents(ctx, rule.ProjectID, since, env)
		return float64(count), err
	case MetricTransactionCount:
		count, err := e.Engine.CountTransactions(ctx, rule.ProjectID, since, env)
		return float64(count), err
	case MetricP95Latency:
		return e.Engine.P95Latency(ctx, rule.ProjectID, since, env)
	case MetricFailureRate:
		return e.Engine.FailureRate(ctx, rule.ProjectID, since, env)
	case MetricCustom:
		metricName := strings.TrimSpace(rule.CustomMetricName)
		if metricName == "" {
			return 0, fmt.Errorf("custom_metric rule %q missing customMetricName", rule.ID)
		}
		return e.Engine.CustomMetricValue(ctx, rule.ProjectID, metricName, since, env)
	default:
		return 0, fmt.Errorf("unsupported metric %q", rule.Metric)
	}
}

// MetricAlertTransition records a state change for a metric alert rule.
type MetricAlertTransition struct {
	Rule      *MetricAlertRule
	FromState string
	ToState   string
	Value     float64
	Timestamp time.Time
}

// evaluateMetricRule compares the current metric value against the rule's
// thresholds and returns a transition if the state should change.
func evaluateMetricRule(rule *MetricAlertRule, value float64, now time.Time) *MetricAlertTransition {
	breached := thresholdBreached(value, rule.Threshold, rule.ThresholdType)

	switch rule.State {
	case "ok", "resolved":
		if breached {
			return &MetricAlertTransition{
				Rule:      rule,
				FromState: rule.State,
				ToState:   "triggered",
				Value:     value,
				Timestamp: now,
			}
		}
	case "triggered":
		if shouldResolve(rule, value, breached) {
			return &MetricAlertTransition{
				Rule:      rule,
				FromState: rule.State,
				ToState:   "resolved",
				Value:     value,
				Timestamp: now,
			}
		}
	}

	return nil
}

// thresholdBreached returns true if value breaches the threshold in the
// direction specified by thresholdType.
func thresholdBreached(value, threshold float64, thresholdType string) bool {
	switch strings.ToLower(strings.TrimSpace(thresholdType)) {
	case "below":
		return value < threshold
	default: // "above" or unset
		return value > threshold
	}
}

// shouldResolve checks whether a triggered rule should auto-resolve.
func shouldResolve(rule *MetricAlertRule, value float64, stillBreached bool) bool {
	// If there's an explicit resolve threshold, use it.
	if rule.ResolveThreshold > 0 {
		switch strings.ToLower(strings.TrimSpace(rule.ThresholdType)) {
		case "below":
			// For "below" alerts, resolve when value rises above the resolve threshold.
			return value >= rule.ResolveThreshold
		default:
			// For "above" alerts, resolve when value drops below the resolve threshold.
			return value <= rule.ResolveThreshold
		}
	}
	// No explicit resolve threshold: resolve when the trigger threshold is no
	// longer breached.
	return !stillBreached
}
