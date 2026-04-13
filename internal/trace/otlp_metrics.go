package trace

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// otlpMetricsRequest is the top-level ExportMetricsServiceRequest.
type otlpMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	Resource     otlpAttributes    `json:"resource"`
	ScopeMetrics []otlpScopeMetric `json:"scopeMetrics"`
}

type otlpScopeMetric struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Unit        string         `json:"unit"`
	Gauge       *otlpGauge     `json:"gauge,omitempty"`
	Sum         *otlpSum       `json:"sum,omitempty"`
}

type otlpGauge struct {
	DataPoints []otlpNumberDataPoint `json:"dataPoints"`
}

type otlpSum struct {
	DataPoints             []otlpNumberDataPoint `json:"dataPoints"`
	AggregationTemporality int                  `json:"aggregationTemporality,omitempty"`
	IsMonotonic            bool                 `json:"isMonotonic,omitempty"`
}

type otlpNumberDataPoint struct {
	Attributes       []otlpKeyValue  `json:"attributes"`
	StartTimeUnixNano string         `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string         `json:"timeUnixNano"`
	AsDouble          *float64       `json:"asDouble,omitempty"`
	AsInt             json.RawMessage `json:"asInt,omitempty"`
}

// MetricBucketEvent is the serialized form of a single metric data point
// as produced by TranslateOTLPMetricsJSON. It matches the fields that
// sqlite.MetricBucket stores (ProjectID is filled in at write time).
type MetricBucketEvent struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"` // "g" for gauge, "c" for sum/counter
	Value     float64           `json:"value"`
	Unit      string            `json:"unit,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// TranslateOTLPMetricsJSON converts an OTLP/HTTP JSON ExportMetricsServiceRequest
// into a slice of serialized MetricBucketEvent payloads. Each element in the
// returned slice corresponds to one numeric data point. Only gauge and sum
// metric types are supported; all others are silently skipped.
func TranslateOTLPMetricsJSON(payload []byte) ([][]byte, error) {
	var req otlpMetricsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("invalid otlp metrics json: %w", err)
	}

	var results [][]byte
	for _, rm := range req.ResourceMetrics {
		resourceAttrs := kvAttrs(rm.Resource.Attributes)
		for _, sm := range rm.ScopeMetrics {
			for _, metric := range sm.Metrics {
				events, err := translateMetric(metric, resourceAttrs)
				if err != nil {
					return nil, err
				}
				results = append(results, events...)
			}
		}
	}
	return results, nil
}

func translateMetric(metric otlpMetric, _ map[string]any) ([][]byte, error) {
	switch {
	case metric.Gauge != nil:
		return translateDataPoints(metric.Name, metric.Unit, "g", metric.Gauge.DataPoints)
	case metric.Sum != nil:
		return translateDataPoints(metric.Name, metric.Unit, "c", metric.Sum.DataPoints)
	default:
		// Unsupported metric type (histogram, exponential histogram, summary) — skip.
		return nil, nil
	}
}

func translateDataPoints(name, unit, metricType string, points []otlpNumberDataPoint) ([][]byte, error) {
	var results [][]byte
	for _, dp := range points {
		value, ok := extractDataPointValue(dp)
		if !ok {
			continue
		}

		ts := parseOTLPMetricTimestamp(dp.TimeUnixNano)
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		tags := dataPointTags(dp.Attributes)

		event := MetricBucketEvent{
			Name:      strings.TrimSpace(name),
			Type:      metricType,
			Value:     value,
			Unit:      strings.TrimSpace(unit),
			Tags:      tags,
			Timestamp: ts,
		}

		raw, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("marshal metric bucket event: %w", err)
		}
		results = append(results, raw)
	}
	return results, nil
}

func extractDataPointValue(dp otlpNumberDataPoint) (float64, bool) {
	if dp.AsDouble != nil {
		return *dp.AsDouble, true
	}
	if len(dp.AsInt) > 0 {
		parsed := parseOTLPIntValue(dp.AsInt)
		switch v := parsed.(type) {
		case int64:
			return float64(v), true
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func dataPointTags(attrs []otlpKeyValue) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	tags := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		v := kv.Value.value()
		if v == nil {
			tags[kv.Key] = ""
			continue
		}
		switch typed := v.(type) {
		case string:
			tags[kv.Key] = typed
		default:
			data, err := json.Marshal(typed)
			if err != nil {
				tags[kv.Key] = fmt.Sprint(typed)
			} else {
				tags[kv.Key] = string(data)
			}
		}
	}
	return tags
}

func parseOTLPMetricTimestamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ns, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}
