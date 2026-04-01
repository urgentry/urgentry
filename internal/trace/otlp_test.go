package trace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateOTLPJSON(t *testing.T) {
	traceID := "0102030405060708090a0b0c0d0e0f10"
	rootID := "1111111111111111"
	childID := "2222222222222222"

	body := []byte(`{
		"resourceSpans":[{
			"resource":{"attributes":[
				{"key":"service.name","value":{"stringValue":"checkout"}},
				{"key":"service.version","value":{"stringValue":"1.2.3"}},
				{"key":"deployment.environment","value":{"stringValue":"production"}}
			]},
			"scopeSpans":[{
				"spans":[
					{"traceId":"` + traceID + `","spanId":"` + rootID + `","name":"GET /checkout","kind":2,"startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}}],"status":{"code":1}},
					{"traceId":"` + traceID + `","spanId":"` + childID + `","parentSpanId":"` + rootID + `","name":"SELECT orders","kind":3,"startTimeUnixNano":"1743076800100000000","endTimeUnixNano":"1743076800200000000","attributes":[{"key":"db.system","value":{"stringValue":"sqlite"}}],"status":{"code":1}}
				]
			}]
		}]
	}`)

	payloads, err := TranslateOTLPJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPJSON: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(payloads))
	}

	var payload map[string]any
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["type"] != "transaction" {
		t.Fatalf("type = %v, want transaction", payload["type"])
	}
	if payload["transaction"] != "GET /checkout" {
		t.Fatalf("transaction = %v, want GET /checkout", payload["transaction"])
	}
	if payload["event_id"] == "" {
		t.Fatal("event_id is empty")
	}
	contexts := payload["contexts"].(map[string]any)
	trace := contexts["trace"].(map[string]any)
	if trace["trace_id"] != traceID {
		t.Fatalf("trace_id = %v, want %s", trace["trace_id"], traceID)
	}
	spans := payload["spans"].([]any)
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
}

func TestTranslateOTLPJSONRejectsInvalidHexIDs(t *testing.T) {
	body := []byte(`{
		"resourceSpans":[{
			"scopeSpans":[{
				"spans":[
					{"traceId":"AQIDBAUGBwgJCgsMDQ4PEA==","spanId":"1111111111111111","name":"GET /checkout","startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000"}
				]
			}]
		}]
	}`)

	_, err := TranslateOTLPJSON(body)
	if err == nil {
		t.Fatal("TranslateOTLPJSON error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid otlp id") {
		t.Fatalf("TranslateOTLPJSON error = %v, want invalid otlp id", err)
	}
}

func TestTranslateOTLPJSONPreservesZeroFalseAndStructuredAttributes(t *testing.T) {
	body := []byte(`{
		"resourceSpans":[{
			"scopeSpans":[{
				"spans":[
					{
						"traceId":"0102030405060708090a0b0c0d0e0f10",
						"spanId":"1111111111111111",
						"name":"GET /checkout",
						"startTimeUnixNano":"1743076800000000000",
						"endTimeUnixNano":"1743076801000000000",
						"attributes":[
							{"key":"cache.hit","value":{"boolValue":false}},
							{"key":"db.rows","value":{"intValue":"0"}},
							{"key":"queue.delay","value":{"doubleValue":0}},
							{"key":"tags","value":{"arrayValue":{"values":[{"stringValue":"a"},{"stringValue":"b"}]}}},
							{"key":"resource","value":{"kvlistValue":{"values":[{"key":"env","value":{"stringValue":"prod"}}]}}},
							{"key":"raw","value":{"bytesValue":"AQID"}}
						]
					}
				]
			}]
		}]
	}`)

	payloads, err := TranslateOTLPJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPJSON: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	data := payload["spans"]
	if data != nil {
		t.Fatalf("unexpected child spans in payload: %+v", data)
	}
	contexts := payload["contexts"].(map[string]any)
	if contexts["trace"] == nil {
		t.Fatalf("missing trace context: %+v", contexts)
	}
}

func TestTranslateOTLPJSONPreservesStructuredChildAttributes(t *testing.T) {
	body := []byte(`{
		"resourceSpans":[{
			"scopeSpans":[{
				"spans":[
					{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"1111111111111111","name":"GET /checkout","startTimeUnixNano":"1743076800000000000","endTimeUnixNano":"1743076801000000000"},
					{
						"traceId":"0102030405060708090a0b0c0d0e0f10",
						"spanId":"2222222222222222",
						"parentSpanId":"1111111111111111",
						"name":"SELECT orders",
						"startTimeUnixNano":"1743076800100000000",
						"endTimeUnixNano":"1743076800200000000",
						"attributes":[
							{"key":"cache.hit","value":{"boolValue":false}},
							{"key":"db.rows","value":{"intValue":"0"}},
							{"key":"queue.delay","value":{"doubleValue":0}},
							{"key":"tags","value":{"arrayValue":{"values":[{"stringValue":"a"},{"stringValue":"b"}]}}},
							{"key":"resource","value":{"kvlistValue":{"values":[{"key":"env","value":{"stringValue":"prod"}}]}}},
							{"key":"raw","value":{"bytesValue":"AQID"}}
						]
					}
				]
			}]
		}]
	}`)

	payloads, err := TranslateOTLPJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPJSON: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	spans := payload["spans"].([]any)
	data := spans[0].(map[string]any)["data"].(map[string]any)
	if got := data["cache.hit"]; got != false {
		t.Fatalf("cache.hit = %#v, want false", got)
	}
	if got := data["db.rows"]; got != float64(0) {
		t.Fatalf("db.rows = %#v, want 0", got)
	}
	if got := data["queue.delay"]; got != float64(0) {
		t.Fatalf("queue.delay = %#v, want 0", got)
	}
	if got := data["raw"]; got != "AQID" {
		t.Fatalf("raw = %#v, want AQID", got)
	}
	tags := data["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
	resource := data["resource"].(map[string]any)
	if resource["env"] != "prod" {
		t.Fatalf("unexpected resource: %#v", resource)
	}
}

func TestTranslateOTLPLogsJSON(t *testing.T) {
	body := []byte(`{
		"resourceLogs":[{
			"resource":{"attributes":[
				{"key":"service.name","value":{"stringValue":"checkout"}},
				{"key":"service.version","value":{"stringValue":"1.2.3"}},
				{"key":"deployment.environment","value":{"stringValue":"production"}}
			]},
			"scopeLogs":[{
				"scope":{"name":"checkout.logger","version":"1.0.0"},
				"logRecords":[
					{
						"timeUnixNano":"1743076800000000000",
						"severityText":"WARN",
						"traceId":"0102030405060708090a0b0c0d0e0f10",
						"spanId":"1111111111111111",
						"body":{"stringValue":"cache miss"},
						"attributes":[{"key":"cache.hit","value":{"boolValue":false}}]
					}
				]
			}]
		}]
	}`)

	payloads, err := TranslateOTLPLogsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPLogsJSON: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(payloads))
	}

	var payload map[string]any
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["type"] != "log" {
		t.Fatalf("type = %v, want log", payload["type"])
	}
	if payload["message"] != "cache miss" {
		t.Fatalf("message = %v, want cache miss", payload["message"])
	}
	if payload["level"] != "warning" {
		t.Fatalf("level = %v, want warning", payload["level"])
	}
	if payload["logger"] != "checkout.logger" {
		t.Fatalf("logger = %v, want checkout.logger", payload["logger"])
	}
}

func TestTranslateOTLPLogsJSONDoesNotCollapseDistinctRecords(t *testing.T) {
	body := []byte(`{
		"resourceLogs":[{
			"scopeLogs":[{
				"scope":{"name":"checkout.logger"},
				"logRecords":[
					{"timeUnixNano":"1743076800000000000","severityText":"INFO","body":{"stringValue":"cache miss"},"attributes":[{"key":"worker","value":{"stringValue":"a"}}]},
					{"timeUnixNano":"1743076800000000000","severityText":"INFO","body":{"stringValue":"cache miss"},"attributes":[{"key":"worker","value":{"stringValue":"b"}}]}
				]
			}]
		}]
	}`)

	payloads, err := TranslateOTLPLogsJSON(body)
	if err != nil {
		t.Fatalf("TranslateOTLPLogsJSON: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("len(payloads) = %d, want 2", len(payloads))
	}

	var first map[string]any
	var second map[string]any
	if err := json.Unmarshal(payloads[0], &first); err != nil {
		t.Fatalf("unmarshal first payload: %v", err)
	}
	if err := json.Unmarshal(payloads[1], &second); err != nil {
		t.Fatalf("unmarshal second payload: %v", err)
	}
	if first["event_id"] == second["event_id"] {
		t.Fatalf("event ids collapsed: %v", first["event_id"])
	}
}
