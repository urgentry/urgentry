package payloads

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Surface string

const (
	SurfaceStore          Surface = "store"
	SurfaceEnvelope       Surface = "envelope"
	SurfaceOTLPTraces     Surface = "otlp_traces"
	SurfaceOTLPLogs       Surface = "otlp_logs"
	SurfaceSecurityReport Surface = "security_report"
	SurfaceNegative       Surface = "negative"
)

type Compression string

const (
	CompressionNone    Compression = "none"
	CompressionGzip    Compression = "gzip"
	CompressionDeflate Compression = "deflate"
)

type Manifest struct {
	Name              string      `json:"name"`
	Surface           Surface     `json:"surface"`
	Method            string      `json:"method"`
	PathKind          string      `json:"path_kind"`
	ContentType       string      `json:"content_type"`
	Compression       Compression `json:"compression,omitempty"`
	EventID           string      `json:"event_id,omitempty"`
	Release           string      `json:"release,omitempty"`
	Environment       string      `json:"environment,omitempty"`
	MonitorSlug       string      `json:"monitor_slug,omitempty"`
	ExpectStatusCode  int         `json:"expect_status_code"`
	ExpectSideEffects []string    `json:"expect_side_effects,omitempty"`
	Description       string      `json:"description,omitempty"`
}

type Payload struct {
	Manifest Manifest `json:"manifest"`
	Body     []byte   `json:"-"`
}

func All() ([]Payload, error) {
	packs := [][]Payload{
		storeCorpus(),
		envelopeCorpus(),
		otlpTraceCorpus(),
		otlpLogCorpus(),
		securityReportCorpus(),
	}

	fixturePacks, err := fixtureCorpora()
	if err != nil {
		return nil, err
	}
	packs = append(packs, fixturePacks...)

	var out []Payload
	for _, pack := range packs {
		out = append(out, pack...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, nil
}

func BySurface(surface Surface) ([]Payload, error) {
	items, err := All()
	if err != nil {
		return nil, err
	}
	filtered := make([]Payload, 0, len(items))
	for _, item := range items {
		if item.Manifest.Surface == surface {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func MustAll() []Payload {
	items, err := All()
	if err != nil {
		panic(err)
	}
	return items
}

var (
	defaultEventTime = time.Date(2026, time.April, 6, 2, 30, 0, 0, time.UTC)
	traceStart       = time.Date(2026, time.April, 6, 2, 31, 0, 0, time.UTC)
	traceEnd         = traceStart.Add(250 * time.Millisecond)
)

const (
	storeEventID            = "01010101010101010101010101010101"
	envelopeEventID         = "02020202020202020202020202020202"
	sessionEnvelopeEventID  = "03030303030303030303030303030303"
	sessionAggregateEventID = "04040404040404040404040404040404"
	clientReportEventID     = "05050505050505050505050505050505"
	checkInEnvelopeEventID  = "06060606060606060606060606060606"
	sessionID               = "11111111-2222-4333-8444-555555555555"
	checkInID               = "66666666-7777-4888-8999-aaaaaaaaaaaa"
)

func storeCorpus() []Payload {
	body := mustJSON(map[string]any{
		"event_id":    storeEventID,
		"timestamp":   defaultEventTime.Format(time.RFC3339),
		"platform":    "javascript",
		"level":       "error",
		"environment": "synthetic",
		"release":     "synthetic@1.0.0",
		"message": map[string]any{
			"formatted": "Synthetic store payload",
		},
		"tags": map[string]string{
			"synthetic": "true",
			"surface":   "store",
		},
	})
	gzipBody := mustGzip(body)
	deflateBody := mustDeflate(body)
	return []Payload{
		{
			Manifest: Manifest{
				Name:              "store-basic-error",
				Surface:           SurfaceStore,
				Method:            "POST",
				PathKind:          "store",
				ContentType:       "application/json",
				EventID:           storeEventID,
				Release:           "synthetic@1.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event", "group"},
				Description:       "Canonical store payload for core error ingest.",
			},
			Body: body,
		},
		{
			Manifest: Manifest{
				Name:              "store-basic-error-gzip",
				Surface:           SurfaceStore,
				Method:            "POST",
				PathKind:          "store",
				ContentType:       "application/json",
				Compression:       CompressionGzip,
				EventID:           storeEventID,
				Release:           "synthetic@1.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event", "group"},
				Description:       "Gzip-compressed store payload.",
			},
			Body: gzipBody,
		},
		{
			Manifest: Manifest{
				Name:              "store-basic-error-deflate",
				Surface:           SurfaceStore,
				Method:            "POST",
				PathKind:          "store",
				ContentType:       "application/json",
				Compression:       CompressionDeflate,
				EventID:           storeEventID,
				Release:           "synthetic@1.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event", "group"},
				Description:       "Deflate-compressed store payload.",
			},
			Body: deflateBody,
		},
	}
}

func envelopeCorpus() []Payload {
	eventPayload := mustJSON(map[string]any{
		"event_id":    envelopeEventID,
		"timestamp":   defaultEventTime.Format(time.RFC3339),
		"platform":    "javascript",
		"level":       "error",
		"environment": "synthetic",
		"release":     "synthetic@1.0.0",
		"message":     "Synthetic envelope event",
	})
	sessionPayload := mustJSON(map[string]any{
		"sid":     sessionID,
		"did":     "synthetic-user-001",
		"status":  "ok",
		"errors":  0,
		"started": defaultEventTime.Format(time.RFC3339),
		"attrs": map[string]any{
			"release":     "synthetic@1.0.0",
			"environment": "synthetic",
		},
	})
	sessionAggregatePayload := mustJSON(map[string]any{
		"aggregates": []map[string]any{
			{
				"started":  defaultEventTime.Format(time.RFC3339),
				"exited":   4,
				"errored":  1,
				"abnormal": 0,
				"crashed":  1,
			},
		},
		"attrs": map[string]any{
			"release":     "synthetic@2.0.0",
			"environment": "synthetic",
		},
	})
	feedbackPayload := mustJSON(map[string]any{
		"event_id": envelopeEventID,
		"name":     "Synthetic Reporter",
		"email":    "synthetic@example.com",
		"comments": "Synthetic feedback item",
	})
	clientReportPayload := mustJSON(map[string]any{
		"timestamp": defaultEventTime.Format(time.RFC3339),
		"discarded_events": []map[string]any{
			{"reason": "queue_overflow", "category": "error", "quantity": 2},
		},
	})
	checkInPayload := mustJSON(map[string]any{
		"check_in_id":  checkInID,
		"monitor_slug": "synthetic-cron",
		"status":       "ok",
		"duration":     4.2,
		"environment":  "synthetic",
		"monitor_config": map[string]any{
			"schedule": map[string]any{
				"type":  "interval",
				"value": 5,
				"unit":  "minute",
			},
		},
	})

	return []Payload{
		{
			Manifest: Manifest{
				Name:              "envelope-event",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				EventID:           envelopeEventID,
				Release:           "synthetic@1.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event", "group"},
			},
			Body: buildEnvelope(map[string]any{"event_id": envelopeEventID}, envelopeItem{typ: "event", payload: eventPayload}),
		},
		{
			Manifest: Manifest{
				Name:              "envelope-session",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				Release:           "synthetic@1.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"release_session"},
			},
			Body: buildEnvelope(map[string]any{"event_id": sessionEnvelopeEventID}, envelopeItem{typ: "session", payload: sessionPayload}),
		},
		{
			Manifest: Manifest{
				Name:              "envelope-session-aggregate",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				Release:           "synthetic@2.0.0",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"release_session_aggregate"},
			},
			Body: buildEnvelope(map[string]any{"event_id": sessionAggregateEventID}, envelopeItem{typ: "sessions", payload: sessionAggregatePayload}),
		},
		{
			Manifest: Manifest{
				Name:              "envelope-user-feedback",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				EventID:           envelopeEventID,
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"user_feedback"},
			},
			Body: buildEnvelope(map[string]any{"event_id": envelopeEventID}, envelopeItem{typ: "user_report", payload: feedbackPayload}),
		},
		{
			Manifest: Manifest{
				Name:              "envelope-client-report",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"outcome"},
			},
			Body: buildEnvelope(map[string]any{"event_id": clientReportEventID}, envelopeItem{typ: "client_report", payload: clientReportPayload}),
		},
		{
			Manifest: Manifest{
				Name:              "envelope-check-in",
				Surface:           SurfaceEnvelope,
				Method:            "POST",
				PathKind:          "envelope",
				ContentType:       "application/x-sentry-envelope",
				MonitorSlug:       "synthetic-cron",
				Environment:       "synthetic",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"monitor", "monitor_checkin"},
			},
			Body: buildEnvelope(map[string]any{"event_id": checkInEnvelopeEventID}, envelopeItem{typ: "check_in", payload: checkInPayload}),
		},
	}
}

func otlpTraceCorpus() []Payload {
	traceID := "0123456789abcdef0123456789abcdef"
	rootSpanID := "0123456789abcdef"
	childSpanID := "abcdef0123456789"
	return []Payload{
		{
			Manifest: Manifest{
				Name:              "otlp-traces-root",
				Surface:           SurfaceOTLPTraces,
				Method:            "POST",
				PathKind:          "otlp_traces",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"transaction"},
				Description:       "Single-span OTLP trace payload.",
			},
			Body: otlpTracesPayload(traceID, rootSpanID, "synthetic-root-span", traceStart.UnixNano(), traceEnd.UnixNano()),
		},
		{
			Manifest: Manifest{
				Name:              "otlp-traces-with-child",
				Surface:           SurfaceOTLPTraces,
				Method:            "POST",
				PathKind:          "otlp_traces",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"transaction", "span"},
				Description:       "Parent/child OTLP trace payload.",
			},
			Body: otlpTracesPayloadWithChild(
				traceID,
				rootSpanID,
				childSpanID,
				"synthetic-root-span",
				"synthetic-child-span",
				traceStart.UnixNano(),
				traceEnd.UnixNano(),
				traceStart.Add(10*time.Millisecond).UnixNano(),
				traceStart.Add(120*time.Millisecond).UnixNano(),
			),
		},
	}
}

func otlpLogCorpus() []Payload {
	return []Payload{
		{
			Manifest: Manifest{
				Name:              "otlp-logs-basic",
				Surface:           SurfaceOTLPLogs,
				Method:            "POST",
				PathKind:          "otlp_logs",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: otlpLogsPayload("synthetic-service", "synthetic.logger", "Synthetic OTLP log", "INFO"),
		},
	}
}

func securityReportCorpus() []Payload {
	return []Payload{
		{
			Manifest: Manifest{
				Name:              "security-csp-json",
				Surface:           SurfaceSecurityReport,
				Method:            "POST",
				PathKind:          "security",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: []byte(`{"csp-report":{"document-uri":"https://example.com","violated-directive":"script-src","blocked-uri":"https://evil.test/app.js","original-policy":"default-src 'self'"}}`),
		},
		{
			Manifest: Manifest{
				Name:              "security-csp-report",
				Surface:           SurfaceSecurityReport,
				Method:            "POST",
				PathKind:          "security",
				ContentType:       "application/csp-report",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: []byte(`{"csp-report":{"document-uri":"https://example.com/page","violated-directive":"style-src","blocked-uri":"https://evil.test/style.css","original-policy":"style-src 'self'"}}`),
		},
		{
			Manifest: Manifest{
				Name:              "security-report-to",
				Surface:           SurfaceSecurityReport,
				Method:            "POST",
				PathKind:          "security",
				ContentType:       "application/reports+json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: []byte(`[{"type":"csp-violation","url":"https://example.com/app","body":{"documentURL":"https://example.com/app","effectiveDirective":"img-src","blockedURL":"https://tracker.example.com/pixel.gif","disposition":"enforce"}}]`),
		},
		{
			Manifest: Manifest{
				Name:              "security-expect-ct",
				Surface:           SurfaceSecurityReport,
				Method:            "POST",
				PathKind:          "security",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: []byte(`{"type":"expect-ct","url":"https://example.com","body":{"hostname":"example.com","port":443,"effective-expiration-date":"2026-12-31T00:00:00Z","served-certificate-chain":["..."],"validated-certificate-chain":["..."],"scts":[]}}`),
		},
		{
			Manifest: Manifest{
				Name:              "security-hpkp",
				Surface:           SurfaceSecurityReport,
				Method:            "POST",
				PathKind:          "security",
				ContentType:       "application/json",
				ExpectStatusCode:  httpStatusOK,
				ExpectSideEffects: []string{"event"},
			},
			Body: []byte(`{"type":"hpkp","url":"https://example.com","body":{"hostname":"example.com","port":443,"noted-hostname":"example.com","include-subdomains":false,"effective-expiration-date":"2026-12-31T00:00:00Z","served-certificate-chain":["..."],"validated-certificate-chain":["..."],"known-pins":["pin-sha256=\"...\""],"date-time":"2026-01-01T00:00:00Z"}}`),
		},
	}
}

func fixtureCorpora() ([][]Payload, error) {
	return [][]Payload{
		loadFixturePayloads(SurfaceStore, "store", "application/json", "store"),
		loadFixturePayloads(SurfaceEnvelope, "envelopes", "application/x-sentry-envelope", "envelope"),
		loadFixturePayloads(SurfaceNegative, "negative", "application/octet-stream", "negative"),
	}, nil
}

func loadFixturePayloads(surface Surface, family, defaultContentType, pathKind string) []Payload {
	entries, err := fixtureEntries(family)
	if err != nil {
		panic(err)
	}
	payloads := make([]Payload, 0, len(entries))
	for _, entry := range entries {
		contentType := defaultContentType
		if surface == SurfaceNegative && strings.HasSuffix(entry.Name, ".json") {
			contentType = "application/json"
		}
		payloads = append(payloads, Payload{
			Manifest: Manifest{
				Name:             fixtureName(family, entry.Name),
				Surface:          surface,
				Method:           "POST",
				PathKind:         pathKind,
				ContentType:      contentType,
				ExpectStatusCode: expectedStatus(surface, entry.Name),
				Description:      "Fixture-backed corpus item from eval/fixtures/" + family,
			},
			Body: entry.Data,
		})
	}
	return payloads
}

func fixtureName(family, filename string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	name = strings.ReplaceAll(name, "_", "-")
	return "fixture-" + family + "-" + name
}

func expectedStatus(surface Surface, filename string) int {
	if surface == SurfaceNegative {
		return httpStatusBadRequest
	}
	return httpStatusOK
}

func buildEnvelope(header map[string]any, items ...envelopeItem) []byte {
	var buf bytes.Buffer
	headerJSON := mustJSON(header)
	buf.Write(headerJSON)
	buf.WriteByte('\n')
	for _, item := range items {
		payloadBytes := item.payload
		itemHeader := map[string]any{
			"type":   item.typ,
			"length": len(payloadBytes),
		}
		if item.filename != "" {
			itemHeader["filename"] = item.filename
		}
		if item.contentType != "" {
			itemHeader["content_type"] = item.contentType
		}
		buf.Write(mustJSON(itemHeader))
		buf.WriteByte('\n')
		buf.Write(payloadBytes)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

type envelopeItem struct {
	typ         string
	payload     []byte
	filename    string
	contentType string
}

func otlpTracesPayload(traceID, spanID, name string, startNano, endNano int64) []byte {
	return mustJSON(map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{
					{"key": "service.name", "value": map[string]any{"stringValue": "synthetic"}},
				},
			},
			"scopeSpans": []map[string]any{{
				"spans": []map[string]any{{
					"traceId":           traceID,
					"spanId":            spanID,
					"name":              name,
					"kind":              2,
					"startTimeUnixNano": fmt.Sprintf("%d", startNano),
					"endTimeUnixNano":   fmt.Sprintf("%d", endNano),
					"status":            map[string]any{"code": 1},
				}},
			}},
		}},
	})
}

func otlpTracesPayloadWithChild(traceID, rootSpanID, childSpanID, rootName, childName string, rootStart, rootEnd, childStart, childEnd int64) []byte {
	return mustJSON(map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{
					{"key": "service.name", "value": map[string]any{"stringValue": "synthetic"}},
					{"key": "service.version", "value": map[string]any{"stringValue": "1.0.0"}},
				},
			},
			"scopeSpans": []map[string]any{{
				"spans": []map[string]any{
					{
						"traceId":           traceID,
						"spanId":            rootSpanID,
						"name":              rootName,
						"kind":              2,
						"startTimeUnixNano": fmt.Sprintf("%d", rootStart),
						"endTimeUnixNano":   fmt.Sprintf("%d", rootEnd),
						"status":            map[string]any{"code": 1},
					},
					{
						"traceId":           traceID,
						"spanId":            childSpanID,
						"parentSpanId":      rootSpanID,
						"name":              childName,
						"kind":              3,
						"startTimeUnixNano": fmt.Sprintf("%d", childStart),
						"endTimeUnixNano":   fmt.Sprintf("%d", childEnd),
						"status":            map[string]any{"code": 1},
					},
				},
			}},
		}},
	})
}

func otlpLogsPayload(serviceName, loggerName, message, severity string) []byte {
	now := defaultEventTime.UnixNano()
	return mustJSON(map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{
					{"key": "service.name", "value": map[string]any{"stringValue": serviceName}},
				},
			},
			"scopeLogs": []map[string]any{{
				"scope": map[string]any{"name": loggerName},
				"logRecords": []map[string]any{{
					"timeUnixNano": fmt.Sprintf("%d", now),
					"severityText": severity,
					"body":         map[string]any{"stringValue": message},
					"attributes":   []map[string]any{},
				}},
			}},
		}},
	})
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func mustGzip(data []byte) []byte {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func mustDeflate(data []byte) []byte {
	var buf bytes.Buffer
	writer, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		panic(err)
	}
	if _, err := writer.Write(data); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

const (
	httpStatusOK         = 200
	httpStatusBadRequest = 400
)

func fixtureEntries(family string) ([]fixtureEntry, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "eval", "fixtures", family)
	items, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]fixtureEntry, 0, len(items))
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		data, err := osReadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, fixtureEntry{Name: item.Name(), Data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type fixtureEntry struct {
	Name string
	Data []byte
}

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..")), nil
}
