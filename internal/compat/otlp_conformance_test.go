//go:build integration

package compat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestOTLPTracesEndpoint verifies that the OTLP traces endpoint accepts
// well-formed OTLP JSON trace data and returns 200.
func TestOTLPTracesEndpoint(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	traceID := "0102030405060708090a0b0c0d0e0f10"
	spanID := "abcdef0123456789"
	now := time.Now().UnixNano()
	body := otlpTracesPayload(traceID, spanID, "test-span", now, now+int64(100*time.Millisecond))

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/traces/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("otlp traces status = %d, want 200", resp.StatusCode)
	}

	waitForTrace(t, srv.db, "default-project", traceID)
}

// TestOTLPLogsEndpoint verifies that the OTLP logs endpoint accepts
// well-formed OTLP JSON log data and returns 200.
func TestOTLPLogsEndpoint(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := otlpLogsPayload("test-service", "test.logger", "conformance log message", "INFO")

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/logs/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("otlp logs status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := srv.db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = 'default-project' AND event_type = 'log'`).Scan(&count); err != nil {
			t.Fatalf("count log events: %v", err)
		}
		if count >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("log event was not persisted")
}

// TestOTLPAuthRequired verifies that requests without valid authentication
// are rejected with 401.
func TestOTLPAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	traceID := "0102030405060708090a0b0c0d0e0f10"
	spanID := "abcdef0123456789"
	now := time.Now().UnixNano()
	traceBody := otlpTracesPayload(traceID, spanID, "no-auth-span", now, now+int64(50*time.Millisecond))
	logBody := otlpLogsPayload("test-service", "auth.logger", "no-auth log", "ERROR")

	tests := []struct {
		name    string
		url     string
		body    []byte
		headers map[string]string
	}{
		{
			name: "traces_missing_auth",
			url:  srv.server.URL + "/api/default-project/otlp/v1/traces/",
			body: traceBody,
			headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
		{
			name: "traces_invalid_key",
			url:  srv.server.URL + "/api/default-project/otlp/v1/traces/",
			body: traceBody,
			headers: map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": "Sentry sentry_key=bad-key-value,sentry_version=7",
			},
		},
		{
			name: "logs_missing_auth",
			url:  srv.server.URL + "/api/default-project/otlp/v1/logs/",
			body: logBody,
			headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
		{
			name: "logs_invalid_key",
			url:  srv.server.URL + "/api/default-project/otlp/v1/logs/",
			body: logBody,
			headers: map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": "Sentry sentry_key=bad-key-value,sentry_version=7",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, tc.url, bytes.NewReader(tc.body), tc.headers)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestOTLPProtobufContentType verifies that the OTLP endpoint rejects
// application/x-protobuf with 415 Unsupported Media Type (protobuf is not
// currently supported).
func TestOTLPProtobufContentType(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Protobuf requests are expected to receive 415 since only JSON is supported.
	fakeProto := []byte{0x0a, 0x00}

	tests := []struct {
		name string
		url  string
	}{
		{name: "traces", url: srv.server.URL + "/api/default-project/otlp/v1/traces/"},
		{name: "logs", url: srv.server.URL + "/api/default-project/otlp/v1/logs/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, tc.url, bytes.NewReader(fakeProto), map[string]string{
				"Content-Type":  "application/x-protobuf",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415", resp.StatusCode)
			}

			// Verify the response content type matches the request content type
			// per the OTLP spec.
			ct := resp.Header.Get("Content-Type")
			if ct != "application/x-protobuf" {
				t.Fatalf("response Content-Type = %q, want application/x-protobuf", ct)
			}
		})
	}
}

// TestOTLPJSONContentType verifies that the OTLP endpoint accepts
// application/json and returns application/json in the response.
func TestOTLPJSONContentType(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	traceID := "aabbccddeeff00112233445566778899"
	spanID := "1122334455667788"
	now := time.Now().UnixNano()
	traceBody := otlpTracesPayload(traceID, spanID, "json-ct-span", now, now+int64(50*time.Millisecond))
	logBody := otlpLogsPayload("ct-service", "ct.logger", "json ct log", "DEBUG")

	tests := []struct {
		name string
		url  string
		body []byte
	}{
		{name: "traces", url: srv.server.URL + "/api/default-project/otlp/v1/traces/", body: traceBody},
		{name: "logs", url: srv.server.URL + "/api/default-project/otlp/v1/logs/", body: logBody},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, tc.url, bytes.NewReader(tc.body), map[string]string{
				"Content-Type":  "application/json",
				"X-Sentry-Auth": srv.sentryAuthHeader(),
			})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			ct := resp.Header.Get("Content-Type")
			if ct != "application/json" {
				t.Fatalf("response Content-Type = %q, want application/json", ct)
			}
		})
	}
}

// TestOTLPTraceSpanStructure verifies that OTLP trace spans are converted
// into Sentry-compatible transaction format with the expected fields.
func TestOTLPTraceSpanStructure(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	traceID := "fedcba9876543210fedcba9876543210"
	rootSpanID := "aaaaaaaaaaaaaaaa"
	childSpanID := "bbbbbbbbbbbbbbbb"
	startNano := int64(1743076800000000000)
	endNano := int64(1743076801000000000)
	childStartNano := int64(1743076800100000000)
	childEndNano := int64(1743076800200000000)

	body := otlpTracesPayloadWithChild(
		traceID, rootSpanID, childSpanID,
		"GET /api/orders", "SELECT orders",
		startNano, endNano,
		childStartNano, childEndNano,
	)

	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/otlp/v1/traces/", bytes.NewReader(body), map[string]string{
		"Content-Type":  "application/json",
		"X-Sentry-Auth": srv.sentryAuthHeader(),
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("otlp status = %d, want 200", resp.StatusCode)
	}

	waitForTrace(t, srv.db, "default-project", traceID)

	// Query the persisted transaction and verify the Sentry-compatible structure.
	var rawPayload []byte
	err := srv.db.QueryRow(
		`SELECT payload_json FROM transactions WHERE project_id = 'default-project' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&rawPayload)
	if err != nil {
		t.Fatalf("query transaction payload: %v", err)
	}

	var event map[string]any
	if err := json.Unmarshal(rawPayload, &event); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Verify type is transaction.
	if got, _ := event["type"].(string); got != "transaction" {
		t.Fatalf("type = %q, want transaction", got)
	}

	// Verify platform is otlp.
	if got, _ := event["platform"].(string); got != "otlp" {
		t.Fatalf("platform = %q, want otlp", got)
	}

	// Verify transaction name.
	if got, _ := event["transaction"].(string); got != "GET /api/orders" {
		t.Fatalf("transaction = %q, want %q", got, "GET /api/orders")
	}

	// Verify trace context contains expected trace_id and span_id.
	contexts, _ := event["contexts"].(map[string]any)
	traceCtx, _ := contexts["trace"].(map[string]any)
	if got, _ := traceCtx["trace_id"].(string); got != traceID {
		t.Fatalf("trace_id = %q, want %q", got, traceID)
	}
	if got, _ := traceCtx["span_id"].(string); got != rootSpanID {
		t.Fatalf("span_id = %q, want %q", got, rootSpanID)
	}

	// Verify operation is derived from attributes (http.request.method -> http.server).
	if got, _ := traceCtx["op"].(string); got != "http.server" {
		t.Fatalf("op = %q, want http.server", got)
	}

	// Verify status is translated from OTLP status code 1 -> "ok".
	if got, _ := traceCtx["status"].(string); got != "ok" {
		t.Fatalf("status = %q, want ok", got)
	}

	// Verify timestamps are present and non-empty.
	if got, _ := event["start_timestamp"].(string); got == "" {
		t.Fatal("start_timestamp is empty")
	}
	if got, _ := event["timestamp"].(string); got == "" {
		t.Fatal("timestamp is empty")
	}

	// Verify child spans exist.
	spans, _ := event["spans"].([]any)
	if len(spans) == 0 {
		t.Fatal("expected at least one child span")
	}
	childSpan, _ := spans[0].(map[string]any)
	if got, _ := childSpan["span_id"].(string); got != childSpanID {
		t.Fatalf("child span_id = %q, want %q", got, childSpanID)
	}
	if got, _ := childSpan["parent_span_id"].(string); got != rootSpanID {
		t.Fatalf("child parent_span_id = %q, want %q", got, rootSpanID)
	}
	if got, _ := childSpan["op"].(string); got != "db" {
		t.Fatalf("child op = %q, want db", got)
	}
	if got, _ := childSpan["description"].(string); got != "SELECT orders" {
		t.Fatalf("child description = %q, want %q", got, "SELECT orders")
	}

	// Verify release is built from service.name + service.version.
	if got, _ := event["release"].(string); got != "order-api@1.2.3" {
		t.Fatalf("release = %q, want order-api@1.2.3", got)
	}

	// Verify tags include service.name.
	tags, _ := event["tags"].([]any)
	foundTag := false
	for _, raw := range tags {
		item, _ := raw.(map[string]any)
		if gotKey, _ := item["key"].(string); gotKey == "service.name" {
			if gotValue, _ := item["value"].(string); gotValue != "order-api" {
				t.Fatalf("service.name tag value = %q, want order-api", gotValue)
			}
			foundTag = true
			break
		}
	}
	if !foundTag {
		t.Fatalf("tags = %#v, want service.name tag", tags)
	}
}

// --- helpers ---

func otlpTracesPayload(traceID, spanID, name string, startNano, endNano int64) []byte {
	payload := map[string]any{
		"resourceSpans": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"stringValue": "test"}},
					},
				},
				"scopeSpans": []map[string]any{
					{
						"spans": []map[string]any{
							{
								"traceId":           traceID,
								"spanId":            spanID,
								"name":              name,
								"kind":              2,
								"startTimeUnixNano": formatNano(startNano),
								"endTimeUnixNano":   formatNano(endNano),
								"status":            map[string]any{"code": 1},
							},
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

func otlpTracesPayloadWithChild(
	traceID, rootSpanID, childSpanID string,
	rootName, childName string,
	rootStart, rootEnd int64,
	childStart, childEnd int64,
) []byte {
	payload := map[string]any{
		"resourceSpans": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"stringValue": "order-api"}},
						{"key": "service.version", "value": map[string]any{"stringValue": "1.2.3"}},
					},
				},
				"scopeSpans": []map[string]any{
					{
						"spans": []map[string]any{
							{
								"traceId":           traceID,
								"spanId":            rootSpanID,
								"name":              rootName,
								"kind":              2,
								"startTimeUnixNano": formatNano(rootStart),
								"endTimeUnixNano":   formatNano(rootEnd),
								"attributes": []map[string]any{
									{"key": "http.request.method", "value": map[string]any{"stringValue": "GET"}},
								},
								"status": map[string]any{"code": 1},
							},
							{
								"traceId":           traceID,
								"spanId":            childSpanID,
								"parentSpanId":      rootSpanID,
								"name":              childName,
								"kind":              3,
								"startTimeUnixNano": formatNano(childStart),
								"endTimeUnixNano":   formatNano(childEnd),
								"attributes": []map[string]any{
									{"key": "db.system", "value": map[string]any{"stringValue": "postgresql"}},
								},
								"status": map[string]any{"code": 1},
							},
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

func otlpLogsPayload(serviceName, loggerName, message, severity string) []byte {
	now := time.Now().UnixNano()
	payload := map[string]any{
		"resourceLogs": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"stringValue": serviceName}},
					},
				},
				"scopeLogs": []map[string]any{
					{
						"scope": map[string]any{
							"name": loggerName,
						},
						"logRecords": []map[string]any{
							{
								"timeUnixNano": formatNano(now),
								"severityText": severity,
								"body":         map[string]any{"stringValue": message},
								"attributes":   []map[string]any{},
							},
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

func formatNano(n int64) string {
	return strconv.FormatInt(n, 10)
}
