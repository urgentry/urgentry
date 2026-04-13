package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type otlpLogRequest struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

type otlpResourceLogs struct {
	Resource                   otlpAttributes `json:"resource"`
	ScopeLogs                  []otlpScopeLog `json:"scopeLogs"`
	InstrumentationLibraryLogs []otlpScopeLog `json:"instrumentationLibraryLogs"`
}

type otlpScopeLog struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpLogRecord struct {
	TimeUnixNano         string         `json:"timeUnixNano"`
	ObservedTimeUnixNano string         `json:"observedTimeUnixNano"`
	SeverityNumber       int            `json:"severityNumber"`
	SeverityText         string         `json:"severityText"`
	Body                 otlpAnyValue   `json:"body"`
	Attributes           []otlpKeyValue `json:"attributes"`
	TraceID              string         `json:"traceId"`
	SpanID               string         `json:"spanId"`
}

// TranslateOTLPLogsJSON converts OTLP/HTTP JSON logs into normalized log events.
func TranslateOTLPLogsJSON(body []byte) ([][]byte, error) {
	var req otlpLogRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid otlp json: %w", err)
	}

	var payloads [][]byte
	for _, resourceLogs := range req.ResourceLogs {
		resourceAttrs := kvAttrs(resourceLogs.Resource.Attributes)
		scopeLogs := append([]otlpScopeLog(nil), resourceLogs.ScopeLogs...)
		scopeLogs = append(scopeLogs, resourceLogs.InstrumentationLibraryLogs...)
		for _, scopeLog := range scopeLogs {
			for _, record := range scopeLog.LogRecords {
				payload, err := buildLogPayload(record, scopeLog.Scope, resourceAttrs)
				if err != nil {
					return nil, err
				}
				payloads = append(payloads, payload)
			}
		}
	}
	return payloads, nil
}

func buildLogPayload(record otlpLogRecord, scope otlpScope, resourceAttrs map[string]any) ([]byte, error) {
	message := anyValueString(record.Body)
	if message == "" {
		message = "otlp log"
	}
	recordedAt := firstNonEmptyString(record.TimeUnixNano, record.ObservedTimeUnixNano)
	timestamp := unixNanoToRFC3339(recordedAt)
	level := otlpLogLevel(record.SeverityText, record.SeverityNumber)
	logAttrs := kvAttrs(record.Attributes)
	extra := map[string]any{}
	for key, value := range logAttrs {
		extra[key] = value
	}
	if strings.TrimSpace(scope.Name) != "" {
		extra["scope.name"] = strings.TrimSpace(scope.Name)
	}
	if strings.TrimSpace(scope.Version) != "" {
		extra["scope.version"] = strings.TrimSpace(scope.Version)
	}

	payload := map[string]any{
		"type":      "log",
		"event_id":  stableOTLPLogEventID(record, scope, message, recordedAt),
		"platform":  "otlp",
		"timestamp": timestamp,
		"level":     level,
		"message":   message,
		"tags":      otlpTags(resourceAttrs),
		"extra":     extra,
	}
	if release := buildRelease(resourceAttrs); release != "" {
		payload["release"] = release
	}
	if environment := stringAttr(resourceAttrs, "deployment.environment"); environment != "" {
		payload["environment"] = environment
	}
	if strings.TrimSpace(scope.Name) != "" {
		payload["logger"] = strings.TrimSpace(scope.Name)
	}
	if strings.TrimSpace(record.TraceID) != "" || strings.TrimSpace(record.SpanID) != "" {
		traceID, err := decodeOTLPID(record.TraceID, 16, true)
		if err != nil {
			return nil, err
		}
		spanID, err := decodeOTLPID(record.SpanID, 8, true)
		if err != nil {
			return nil, err
		}
		payload["contexts"] = map[string]any{
			"trace": map[string]any{
				"trace_id": traceID,
				"span_id":  spanID,
			},
		}
	}
	return json.Marshal(payload)
}

func otlpLogLevel(severityText string, severityNumber int) string {
	switch strings.ToLower(strings.TrimSpace(severityText)) {
	case "fatal", "error", "warning", "warn", "info", "debug":
		if strings.EqualFold(strings.TrimSpace(severityText), "warn") {
			return "warning"
		}
		return strings.ToLower(strings.TrimSpace(severityText))
	}
	switch {
	case severityNumber >= 17:
		return "error"
	case severityNumber >= 13:
		return "warning"
	case severityNumber >= 9:
		return "info"
	default:
		return "debug"
	}
}

func anyValueString(value otlpAnyValue) string {
	item := value.value()
	if item == nil {
		return ""
	}
	switch typed := item.(type) {
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stableOTLPLogEventID(record otlpLogRecord, scope otlpScope, message, recordedAt string) string {
	attrs, _ := json.Marshal(record.Attributes)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(record.TraceID),
		strings.TrimSpace(record.SpanID),
		strings.TrimSpace(recordedAt),
		strings.TrimSpace(scope.Name),
		strings.TrimSpace(scope.Version),
		strings.TrimSpace(message),
		string(attrs),
	}, ":")))
	return hex.EncodeToString(sum[:16])
}
