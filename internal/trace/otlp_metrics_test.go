package trace

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTranslateOTLPMetricsJSONGauge(t *testing.T) {
	body := []byte(`{
		"resourceMetrics":[{
			"resource":{"attributes":[
				{"key":"service.name","value":{"stringValue":"myservice"}}
			]},
			"scopeMetrics":[{
				"metrics":[{
					"name":"system.cpu.usage",
					"unit":"1",
					"gauge":{
						"dataPoints":[{
							"timeUnixNano":"1743076800000000000",
							"asDouble":0.42,
							"attributes":[
								{"key":"cpu","value":{"stringValue":"cpu0"}}
							]
						}]
					}
				}]
			}]
		}]
	}`)

	events, err := TranslateOTLPMetricsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	var evt MetricBucketEvent
	if err := json.Unmarshal(events[0], &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Name != "system.cpu.usage" {
		t.Fatalf("Name = %q, want system.cpu.usage", evt.Name)
	}
	if evt.Type != "g" {
		t.Fatalf("Type = %q, want g (gauge)", evt.Type)
	}
	if evt.Value != 0.42 {
		t.Fatalf("Value = %v, want 0.42", evt.Value)
	}
	if evt.Unit != "1" {
		t.Fatalf("Unit = %q, want 1", evt.Unit)
	}
	if evt.Tags["cpu"] != "cpu0" {
		t.Fatalf("Tags[cpu] = %q, want cpu0", evt.Tags["cpu"])
	}
	if evt.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero")
	}
	want := time.Unix(0, 1743076800000000000).UTC()
	if !evt.Timestamp.Equal(want) {
		t.Fatalf("Timestamp = %v, want %v", evt.Timestamp, want)
	}
}

func TestTranslateOTLPMetricsJSONSum(t *testing.T) {
	body := []byte(`{
		"resourceMetrics":[{
			"scopeMetrics":[{
				"metrics":[{
					"name":"http.server.requests",
					"unit":"requests",
					"sum":{
						"isMonotonic":true,
						"dataPoints":[{
							"timeUnixNano":"1743076800000000000",
							"asDouble":100.0,
							"attributes":[
								{"key":"http.method","value":{"stringValue":"GET"}},
								{"key":"http.status_code","value":{"intValue":"200"}}
							]
						}]
					}
				}]
			}]
		}]
	}`)

	events, err := TranslateOTLPMetricsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	var evt MetricBucketEvent
	if err := json.Unmarshal(events[0], &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Name != "http.server.requests" {
		t.Fatalf("Name = %q, want http.server.requests", evt.Name)
	}
	if evt.Type != "c" {
		t.Fatalf("Type = %q, want c (counter/sum)", evt.Type)
	}
	if evt.Value != 100.0 {
		t.Fatalf("Value = %v, want 100.0", evt.Value)
	}
	if evt.Unit != "requests" {
		t.Fatalf("Unit = %q, want requests", evt.Unit)
	}
	if evt.Tags["http.method"] != "GET" {
		t.Fatalf("Tags[http.method] = %q, want GET", evt.Tags["http.method"])
	}
	if evt.Tags["http.status_code"] != "200" {
		t.Fatalf("Tags[http.status_code] = %q, want 200", evt.Tags["http.status_code"])
	}
}

func TestTranslateOTLPMetricsJSONMultipleMetrics(t *testing.T) {
	body := []byte(`{
		"resourceMetrics":[{
			"scopeMetrics":[{
				"metrics":[
					{
						"name":"metric.a",
						"gauge":{"dataPoints":[
							{"timeUnixNano":"1743076800000000000","asDouble":1.0},
							{"timeUnixNano":"1743076801000000000","asDouble":2.0}
						]}
					},
					{
						"name":"metric.b",
						"sum":{"dataPoints":[
							{"timeUnixNano":"1743076800000000000","asDouble":99.0}
						]}
					}
				]
			}]
		}]
	}`)

	events, err := TranslateOTLPMetricsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
}

func TestTranslateOTLPMetricsJSONSkipsUnsupportedTypes(t *testing.T) {
	// Histogram metrics should be silently skipped.
	body := []byte(`{
		"resourceMetrics":[{
			"scopeMetrics":[{
				"metrics":[{
					"name":"http.request.duration",
					"unit":"ms",
					"histogram":{
						"dataPoints":[{
							"startTimeUnixNano":"1743076800000000000",
							"timeUnixNano":"1743076801000000000",
							"count":"10",
							"sum":1234.5,
							"bucketCounts":["1","3","6"],
							"explicitBounds":[100,500]
						}]
					}
				}]
			}]
		}]
	}`)

	events, err := TranslateOTLPMetricsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0 (histogram skipped)", len(events))
	}
}

func TestTranslateOTLPMetricsJSONRejectsInvalidJSON(t *testing.T) {
	_, err := TranslateOTLPMetricsJSON([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestTranslateOTLPMetricsJSONEmptyPayload(t *testing.T) {
	events, err := TranslateOTLPMetricsJSON([]byte(`{"resourceMetrics":[]}`))
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
}

func TestTranslateOTLPMetricsJSONIntValue(t *testing.T) {
	// Sum data points may carry asInt instead of asDouble.
	body := []byte(`{
		"resourceMetrics":[{
			"scopeMetrics":[{
				"metrics":[{
					"name":"process.open_fds",
					"unit":"fd",
					"sum":{"dataPoints":[{
						"timeUnixNano":"1743076800000000000",
						"asInt":"42"
					}]}
				}]
			}]
		}]
	}`)

	events, err := TranslateOTLPMetricsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPMetricsJSON: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	var evt MetricBucketEvent
	if err := json.Unmarshal(events[0], &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Value != 42.0 {
		t.Fatalf("Value = %v, want 42.0", evt.Value)
	}
}
