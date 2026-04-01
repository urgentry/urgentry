package securityreport

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/pkg/id"
)

type reportingEnvelope struct {
	CSPReport map[string]any   `json:"csp-report"`
	Reports   []reportingEntry `json:"reports"`
	Type      string           `json:"type"`
	URL       string           `json:"url"`
	UserAgent string           `json:"user_agent"`
	Body      map[string]any   `json:"body"`
}

type reportingEntry struct {
	Type      string         `json:"type"`
	URL       string         `json:"url"`
	UserAgent string         `json:"user_agent"`
	Body      map[string]any `json:"body"`
}

// TranslateJSON converts browser security reports into regular event payloads
// that can flow through the normal event pipeline.
func TranslateJSON(body []byte) ([][]byte, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty security report body")
	}

	if strings.HasPrefix(trimmed, "[") {
		var entries []reportingEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, fmt.Errorf("invalid reporting api payload: %w", err)
		}
		return translateEntries(entries)
	}

	var env reportingEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("invalid security report payload: %w", err)
	}
	switch {
	case len(env.CSPReport) > 0:
		payload, err := buildCSPEvent(env.CSPReport, "csp")
		if err != nil {
			return nil, err
		}
		return [][]byte{payload}, nil
	case len(env.Reports) > 0:
		return translateEntries(env.Reports)
	case strings.TrimSpace(env.Type) != "":
		payload, err := buildReportingEvent(reportingEntry{
			Type:      env.Type,
			URL:       env.URL,
			UserAgent: env.UserAgent,
			Body:      env.Body,
		})
		if err != nil {
			return nil, err
		}
		return [][]byte{payload}, nil
	default:
		return nil, fmt.Errorf("unsupported security report payload")
	}
}

func translateEntries(entries []reportingEntry) ([][]byte, error) {
	payloads := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		payload, err := buildReportingEvent(entry)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

func buildReportingEvent(entry reportingEntry) ([]byte, error) {
	reportType := strings.TrimSpace(entry.Type)
	switch reportType {
	case "", "csp-violation":
		return buildCSPEvent(entry.Body, firstNonEmpty(reportType, "csp-violation"))
	default:
		url := firstNonEmpty(strings.TrimSpace(entry.URL), stringField(entry.Body, "documentURL", "document-uri", "url"))
		message := fmt.Sprintf("%s report", humanizeReportType(reportType))
		if url != "" {
			message += " for " + url
		}
		tags := map[string]string{"report_type": reportType}
		addTag(tags, "phase", stringField(entry.Body, "phase"))
		addTag(tags, "disposition", stringField(entry.Body, "disposition"))
		addTag(tags, "status_code", stringField(entry.Body, "status_code", "statusCode"))
		addTag(tags, "user_agent", entry.UserAgent)
		return buildEvent(eventSpec{
			Logger:      "security." + sanitizeName(reportType),
			Level:       "warning",
			Message:     message,
			URL:         url,
			Tags:        tags,
			Extra:       map[string]any{"report_type": reportType, "url": entry.URL, "user_agent": entry.UserAgent, "body": entry.Body},
			Fingerprint: []string{"security", reportType, firstNonEmpty(url, stringField(entry.Body, "phase"))},
		})
	}
}

func buildCSPEvent(report map[string]any, reportType string) ([]byte, error) {
	if len(report) == 0 {
		return nil, fmt.Errorf("empty csp report body")
	}
	directive := stringField(report, "effective-directive", "effectiveDirective", "violated-directive", "violatedDirective")
	blocked := stringField(report, "blocked-uri", "blockedURL", "blockedUrl")
	documentURL := stringField(report, "document-uri", "documentURL", "documentUrl", "url")
	disposition := stringField(report, "disposition")
	message := "CSP violation"
	if directive != "" {
		message = "CSP " + directive
	}
	if blocked != "" {
		message += " blocked " + blocked
	}
	if disposition != "" {
		message += " (" + disposition + ")"
	}

	tags := map[string]string{"report_type": firstNonEmpty(reportType, "csp")}
	addTag(tags, "directive", directive)
	addTag(tags, "blocked_uri", blocked)
	addTag(tags, "document_uri", documentURL)
	addTag(tags, "disposition", disposition)
	addTag(tags, "status_code", stringField(report, "status-code", "statusCode"))

	level := "warning"
	if disposition == "enforce" {
		level = "error"
	}
	return buildEvent(eventSpec{
		Logger:      "security.csp",
		Level:       level,
		Message:     message,
		URL:         documentURL,
		Tags:        tags,
		Extra:       map[string]any{"report_type": firstNonEmpty(reportType, "csp"), "body": report},
		Fingerprint: []string{"security", "csp", firstNonEmpty(directive, "unknown"), firstNonEmpty(blocked, "self")},
	})
}

type eventSpec struct {
	Logger      string
	Level       string
	Message     string
	URL         string
	Tags        map[string]string
	Extra       map[string]any
	Fingerprint []string
}

func buildEvent(spec eventSpec) ([]byte, error) {
	event := map[string]any{
		"event_id":    id.New(),
		"platform":    "javascript",
		"level":       firstNonEmpty(spec.Level, "warning"),
		"logger":      spec.Logger,
		"message":     spec.Message,
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"fingerprint": compactStrings(spec.Fingerprint),
		"extra": map[string]any{
			"security_report": spec.Extra,
		},
	}
	if len(spec.Tags) > 0 {
		event["tags"] = spec.Tags
	}
	if strings.TrimSpace(spec.URL) != "" {
		event["request"] = map[string]any{"url": strings.TrimSpace(spec.URL)}
	}
	return json.Marshal(event)
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func humanizeReportType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "Security"
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
	}
	return strings.Join(parts, " ")
}

func sanitizeName(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "report"
	}
	replacer := strings.NewReplacer("-", ".", "_", ".", " ", ".")
	raw = replacer.Replace(raw)
	for strings.Contains(raw, "..") {
		raw = strings.ReplaceAll(raw, "..", ".")
	}
	return strings.Trim(raw, ".")
}

func stringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func addTag(tags map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	tags[key] = value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
