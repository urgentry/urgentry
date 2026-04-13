package trace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource                    otlpAttributes  `json:"resource"`
	ScopeSpans                  []otlpScopeSpan `json:"scopeSpans"`
	InstrumentationLibrarySpans []otlpScopeSpan `json:"instrumentationLibrarySpans"`
}

type otlpScopeSpan struct {
	Spans []otlpSpan `json:"spans"`
}

type otlpAttributes struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes"`
	Status            otlpStatus     `json:"status"`
}

type otlpStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type otlpKeyValue struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue *string         `json:"stringValue,omitempty"`
	IntValue    json.RawMessage `json:"intValue,omitempty"`
	DoubleValue *float64        `json:"doubleValue,omitempty"`
	BoolValue   *bool           `json:"boolValue,omitempty"`
	ArrayValue  *otlpArrayValue `json:"arrayValue,omitempty"`
	KVListValue *otlpKVList     `json:"kvlistValue,omitempty"`
	BytesValue  *string         `json:"bytesValue,omitempty"`
}

type otlpArrayValue struct {
	Values []otlpAnyValue `json:"values"`
}

type otlpKVList struct {
	Values []otlpKeyValue `json:"values"`
}

// TranslateOTLPJSON converts OTLP/HTTP JSON traces into transaction payloads
// that match the normal Sentry transaction ingest path.
func TranslateOTLPJSON(body []byte) ([][]byte, error) {
	var req otlpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid otlp json: %w", err)
	}

	var payloads [][]byte
	for _, resourceSpans := range req.ResourceSpans {
		resourceAttrs := kvAttrs(resourceSpans.Resource.Attributes)
		allSpans := append([]otlpSpan(nil), flattenScopeSpans(resourceSpans.ScopeSpans)...)
		allSpans = append(allSpans, flattenScopeSpans(resourceSpans.InstrumentationLibrarySpans)...)
		if len(allSpans) == 0 {
			continue
		}

		spanByID := make(map[string]otlpSpan, len(allSpans))
		children := make(map[string][]otlpSpan)
		var roots []otlpSpan
		for _, span := range allSpans {
			spanByID[span.SpanID] = span
		}
		for _, span := range allSpans {
			parent := strings.TrimSpace(span.ParentSpanID)
			if parent == "" || spanByID[parent].SpanID == "" {
				roots = append(roots, span)
				continue
			}
			children[parent] = append(children[parent], span)
		}
		if len(roots) == 0 {
			roots = append(roots, allSpans[0])
		}

		for _, root := range roots {
			payload, err := buildTransactionPayload(root, children, resourceAttrs)
			if err != nil {
				return nil, err
			}
			payloads = append(payloads, payload)
		}
	}
	return payloads, nil
}

func buildTransactionPayload(root otlpSpan, children map[string][]otlpSpan, resourceAttrs map[string]any) ([]byte, error) {
	traceID, err := decodeOTLPID(root.TraceID, 16, false)
	if err != nil {
		return nil, err
	}
	spanID, err := decodeOTLPID(root.SpanID, 8, false)
	if err != nil {
		return nil, err
	}
	parentSpanID, err := decodeOTLPID(root.ParentSpanID, 8, true)
	if err != nil {
		return nil, err
	}
	rootAttrs := kvAttrs(root.Attributes)

	transaction := strings.TrimSpace(root.Name)
	if transaction == "" {
		transaction = "otlp transaction"
	}
	release := buildRelease(resourceAttrs)
	environment := stringAttr(resourceAttrs, "deployment.environment")
	op := spanOperation(rootAttrs, root.Kind)
	status := otlpStatusString(root.Status)

	payload := map[string]any{
		"type":            "transaction",
		"event_id":        stableOTLPEventID(traceID, spanID, root.StartTimeUnixNano),
		"platform":        "otlp",
		"transaction":     transaction,
		"start_timestamp": unixNanoToRFC3339(root.StartTimeUnixNano),
		"timestamp":       unixNanoToRFC3339(root.EndTimeUnixNano),
		"tags":            otlpTags(resourceAttrs),
		"contexts": map[string]any{
			"trace": map[string]any{
				"trace_id":       traceID,
				"span_id":        spanID,
				"parent_span_id": parentSpanID,
				"op":             op,
				"status":         status,
			},
		},
	}
	if release != "" {
		payload["release"] = release
	}
	if environment != "" {
		payload["environment"] = environment
	}

	var spans []map[string]any
	for _, child := range collectDescendants(root.SpanID, children) {
		childTraceID, err := decodeOTLPID(child.TraceID, 16, false)
		if err != nil {
			return nil, err
		}
		childSpanID, err := decodeOTLPID(child.SpanID, 8, false)
		if err != nil {
			return nil, err
		}
		childParentSpanID, err := decodeOTLPID(child.ParentSpanID, 8, true)
		if err != nil {
			return nil, err
		}
		childAttrs := kvAttrs(child.Attributes)
		spans = append(spans, map[string]any{
			"trace_id":        childTraceID,
			"span_id":         childSpanID,
			"parent_span_id":  childParentSpanID,
			"op":              spanOperation(childAttrs, child.Kind),
			"description":     strings.TrimSpace(child.Name),
			"status":          otlpStatusString(child.Status),
			"start_timestamp": unixNanoToRFC3339(child.StartTimeUnixNano),
			"timestamp":       unixNanoToRFC3339(child.EndTimeUnixNano),
			"data":            childAttrs,
		})
	}
	if len(spans) > 0 {
		payload["spans"] = spans
	}

	return json.Marshal(payload)
}

func flattenScopeSpans(scopes []otlpScopeSpan) []otlpSpan {
	var spans []otlpSpan
	for _, scope := range scopes {
		spans = append(spans, scope.Spans...)
	}
	return spans
}

func collectDescendants(rootID string, children map[string][]otlpSpan) []otlpSpan {
	var result []otlpSpan
	var walk func(parent string)
	walk = func(parent string) {
		for _, child := range children[parent] {
			result = append(result, child)
			walk(child.SpanID)
		}
	}
	walk(rootID)
	return result
}

func kvAttrs(items []otlpKeyValue) map[string]any {
	out := make(map[string]any, len(items))
	for _, item := range items {
		value := item.Value.value()
		if value == nil {
			out[item.Key] = ""
			continue
		}
		out[item.Key] = value
	}
	return out
}

func (v otlpAnyValue) value() any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case len(v.IntValue) > 0:
		return parseOTLPIntValue(v.IntValue)
	case v.DoubleValue != nil:
		return *v.DoubleValue
	case v.BoolValue != nil:
		return *v.BoolValue
	case v.ArrayValue != nil:
		values := make([]any, 0, len(v.ArrayValue.Values))
		for _, item := range v.ArrayValue.Values {
			values = append(values, item.value())
		}
		return values
	case v.KVListValue != nil:
		return kvAttrs(v.KVListValue.Values)
	case v.BytesValue != nil:
		return *v.BytesValue
	default:
		return nil
	}
}

func parseOTLPIntValue(raw json.RawMessage) any {
	value := strings.TrimSpace(string(bytes.TrimSpace(raw)))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			value = strings.TrimSpace(text)
		}
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	return value
}

func otlpTags(attrs map[string]any) map[string]string {
	tags := make(map[string]string)
	for _, key := range []string{"service.name", "deployment.environment", "service.namespace"} {
		if value := stringAttr(attrs, key); value != "" {
			tags[key] = value
		}
	}
	return tags
}

func stringAttr(attrs map[string]any, key string) string {
	value, ok := attrs[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func buildRelease(attrs map[string]any) string {
	name := stringAttr(attrs, "service.name")
	version := stringAttr(attrs, "service.version")
	switch {
	case name != "" && version != "":
		return name + "@" + version
	case version != "":
		return version
	default:
		return ""
	}
}

func spanOperation(attrs map[string]any, kind int) string {
	for _, key := range []string{"sentry.op", "span.op"} {
		if value := stringAttr(attrs, key); value != "" {
			return value
		}
	}
	switch {
	case stringAttr(attrs, "db.system") != "":
		return "db"
	case stringAttr(attrs, "rpc.system") != "":
		return "rpc"
	case stringAttr(attrs, "http.request.method") != "":
		return "http.server"
	case kind == 3:
		return "http.client"
	default:
		return "otel"
	}
}

func otlpStatusString(status otlpStatus) string {
	switch status.Code {
	case 2:
		return "error"
	case 1:
		return "ok"
	default:
		return ""
	}
}

func decodeOTLPID(raw string, byteLen int, allowEmpty bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("missing otlp id")
	}
	if len(raw) != byteLen*2 {
		return "", fmt.Errorf("invalid otlp id length")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", fmt.Errorf("invalid otlp id")
	}
	return strings.ToLower(raw), nil
}

func stableOTLPEventID(traceID, spanID, startedAt string) string {
	sum := sha256.Sum256([]byte(traceID + ":" + spanID + ":" + strings.TrimSpace(startedAt)))
	return hex.EncodeToString(sum[:16])
}

func unixNanoToRFC3339(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(0, value).UTC().Format(time.RFC3339Nano)
}
