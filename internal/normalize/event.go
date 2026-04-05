// Package normalize transforms raw SDK event payloads into a
// canonical internal representation. This is the core of behavioral
// equivalence — normalized events must be deterministic and stable.
package normalize

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"urgentry/pkg/id"
)

// Event is the normalized internal representation of an error event.
type Event struct {
	Type           string                 `json:"type,omitempty"`
	EventID        string                 `json:"event_id"`
	Timestamp      time.Time              `json:"timestamp"`
	StartTimestamp *time.Time             `json:"start_timestamp,omitempty"`
	Platform       string                 `json:"platform"`
	Level          string                 `json:"level"`
	Logger         string                 `json:"logger,omitempty"`
	Transaction    string                 `json:"transaction,omitempty"`
	ServerName     string                 `json:"server_name,omitempty"`
	Release        string                 `json:"release,omitempty"`
	Dist           string                 `json:"dist,omitempty"`
	Environment    string                 `json:"environment,omitempty"`
	Message        string                 `json:"message,omitempty"`
	Fingerprint    []string               `json:"fingerprint,omitempty"`
	Tags           map[string]string      `json:"tags,omitempty"`
	Extra          map[string]any         `json:"extra,omitempty"`
	Modules        map[string]string      `json:"modules,omitempty"`
	User           *User                  `json:"user,omitempty"`
	Request        *Request               `json:"request,omitempty"`
	Contexts       map[string]any         `json:"contexts,omitempty"`
	SDK            *SDK                   `json:"sdk,omitempty"`
	Exception      *ExceptionList         `json:"exception,omitempty"`
	Breadcrumbs    *BreadcrumbList        `json:"breadcrumbs,omitempty"`
	Spans          []Span                 `json:"spans,omitempty"`
	Measurements   map[string]Measurement `json:"measurements,omitempty"`
}

// rawEvent is used for the first-pass unmarshal to handle flexible types.
type rawEvent struct {
	Type           string                 `json:"type,omitempty"`
	EventID        string                 `json:"event_id"`
	Timestamp      normalizedTimestamp    `json:"timestamp,omitempty"`
	StartTimestamp normalizedTimestamp    `json:"start_timestamp,omitempty"`
	Platform       string                 `json:"platform"`
	Level          string                 `json:"level"`
	Logger         string                 `json:"logger,omitempty"`
	Transaction    string                 `json:"transaction,omitempty"`
	ServerName     string                 `json:"server_name,omitempty"`
	Release        string                 `json:"release,omitempty"`
	Dist           string                 `json:"dist,omitempty"`
	Environment    string                 `json:"environment,omitempty"`
	Message        normalizedMessage      `json:"message,omitempty"`
	Fingerprint    []string               `json:"fingerprint,omitempty"`
	Tags           normalizedTags         `json:"tags,omitempty"`
	Extra          map[string]any         `json:"extra,omitempty"`
	Modules        map[string]string      `json:"modules,omitempty"`
	User           *User                  `json:"user,omitempty"`
	Request        *Request               `json:"request,omitempty"`
	Contexts       map[string]any         `json:"contexts,omitempty"`
	SDK            *SDK                   `json:"sdk,omitempty"`
	Exception      *ExceptionList         `json:"exception,omitempty"`
	Breadcrumbs    *BreadcrumbList        `json:"breadcrumbs,omitempty"`
	Spans          []rawSpan              `json:"spans,omitempty"`
	Measurements   map[string]Measurement `json:"measurements,omitempty"`
}

type ExceptionList struct {
	Values []Exception `json:"values"`
}

type Exception struct {
	Type       string      `json:"type,omitempty"`
	Value      string      `json:"value,omitempty"`
	Module     string      `json:"module,omitempty"`
	ThreadID   int         `json:"thread_id,omitempty"`
	Mechanism  *Mechanism  `json:"mechanism,omitempty"`
	Stacktrace *Stacktrace `json:"stacktrace,omitempty"`
}

type Mechanism struct {
	Type        string `json:"type,omitempty"`
	Handled     *bool  `json:"handled,omitempty"`
	Description string `json:"description,omitempty"`
}

type Stacktrace struct {
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Filename        string   `json:"filename,omitempty"`
	Function        string   `json:"function,omitempty"`
	Module          string   `json:"module,omitempty"`
	Package         string   `json:"package,omitempty"`
	Lineno          int      `json:"lineno,omitempty"`
	Colno           int      `json:"colno,omitempty"`
	AbsPath         string   `json:"abs_path,omitempty"`
	ContextLine     string   `json:"context_line,omitempty"`
	PreContext      []string `json:"pre_context,omitempty"`
	PostContext     []string `json:"post_context,omitempty"`
	InApp           *bool    `json:"in_app,omitempty"`
	InstructionAddr string   `json:"instruction_addr,omitempty"`
	SymbolAddr      string   `json:"symbol_addr,omitempty"`
	AddrMode        string   `json:"addr_mode,omitempty"`
	DebugID         string   `json:"debug_id,omitempty"`
}

type User struct {
	ID        string         `json:"id,omitempty"`
	Email     string         `json:"email,omitempty"`
	Username  string         `json:"username,omitempty"`
	IPAddress string         `json:"ip_address,omitempty"`
	Geo       map[string]any `json:"geo,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Request struct {
	URL         string            `json:"url,omitempty"`
	Method      string            `json:"method,omitempty"`
	QueryString any               `json:"query_string,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Data        any               `json:"data,omitempty"`
	Cookies     any               `json:"cookies,omitempty"`
	Env         map[string]any    `json:"env,omitempty"`
}

type SDK struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type BreadcrumbList struct {
	Values []Breadcrumb `json:"values"`
}

type Breadcrumb struct {
	Timestamp any            `json:"timestamp,omitempty"` // string or float64
	Type      string         `json:"type,omitempty"`
	Category  string         `json:"category,omitempty"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Level     string         `json:"level,omitempty"`
}

type Measurement struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type Span struct {
	TraceID        string            `json:"trace_id,omitempty"`
	SpanID         string            `json:"span_id,omitempty"`
	ParentSpanID   string            `json:"parent_span_id,omitempty"`
	Op             string            `json:"op,omitempty"`
	Description    string            `json:"description,omitempty"`
	Status         string            `json:"status,omitempty"`
	StartTimestamp time.Time         `json:"start_timestamp,omitempty"`
	Timestamp      time.Time         `json:"timestamp,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	Data           map[string]any    `json:"data,omitempty"`
}

type rawSpan struct {
	TraceID        string          `json:"trace_id,omitempty"`
	SpanID         string          `json:"span_id,omitempty"`
	ParentSpanID   string          `json:"parent_span_id,omitempty"`
	Op             string          `json:"op,omitempty"`
	Description    string          `json:"description,omitempty"`
	Status         string          `json:"status,omitempty"`
	StartTimestamp normalizedTimestamp `json:"start_timestamp,omitempty"`
	Timestamp      normalizedTimestamp `json:"timestamp,omitempty"`
	Tags           normalizedTags      `json:"tags,omitempty"`
	Data           map[string]any  `json:"data,omitempty"`
}

type normalizedTimestamp struct {
	set   bool
	value time.Time
}

func (n *normalizedTimestamp) UnmarshalJSON(data []byte) error {
	n.set = true
	n.value = parseNormalizedTimestamp(data)
	return nil
}

type normalizedMessage string

func (m *normalizedMessage) UnmarshalJSON(data []byte) error {
	*m = normalizedMessage(parseNormalizedMessage(data))
	return nil
}

type normalizedTags map[string]string

func (t *normalizedTags) UnmarshalJSON(data []byte) error {
	*t = parseNormalizedTags(data)
	return nil
}

// Normalize takes a raw JSON event payload and produces a normalized Event.
func Normalize(raw []byte) (*Event, error) {
	var r rawEvent
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("invalid event JSON: %w", err)
	}

	evt := &Event{
		Type:         r.Type,
		EventID:      r.EventID,
		Platform:     r.Platform,
		Level:        r.Level,
		Logger:       r.Logger,
		Transaction:  r.Transaction,
		ServerName:   r.ServerName,
		Release:      r.Release,
		Dist:         r.Dist,
		Environment:  r.Environment,
		Fingerprint:  r.Fingerprint,
		Extra:        r.Extra,
		Modules:      r.Modules,
		User:         r.User,
		Request:      r.Request,
		Contexts:     r.Contexts,
		SDK:          r.SDK,
		Exception:    r.Exception,
		Breadcrumbs:  r.Breadcrumbs,
		Measurements: r.Measurements,
	}

	// Normalize event_id
	if evt.EventID == "" {
		evt.EventID = id.New()
	}
	evt.EventID = normalizeEventID(evt.EventID)

	// Normalize timestamp (handles ISO 8601 string or epoch float)
	evt.Timestamp = time.Now().UTC()
	if r.Timestamp.set {
		evt.Timestamp = r.Timestamp.value
	}
	if r.StartTimestamp.set && !r.StartTimestamp.value.IsZero() {
		started := r.StartTimestamp.value
		evt.StartTimestamp = &started
	}

	// Handle tags in array-of-pairs or object format
	if len(r.Tags) > 0 {
		evt.Tags = map[string]string(r.Tags)
	}

	// Normalize message (string or message object)
	if r.Message != "" {
		evt.Message = string(r.Message)
	}

	// Normalize level
	evt.Level = normalizeLevel(evt.Level)

	// Default environment
	if evt.Environment == "" {
		evt.Environment = "production"
	}

	// Default platform
	if evt.Platform == "" {
		evt.Platform = "other"
	}

	if len(r.Spans) > 0 {
		evt.Spans = make([]Span, 0, len(r.Spans))
		for _, span := range r.Spans {
			item := Span{
				TraceID:        span.TraceID,
				SpanID:         span.SpanID,
				ParentSpanID:   span.ParentSpanID,
				Op:             span.Op,
				Description:    span.Description,
				Status:         span.Status,
				StartTimestamp: span.StartTimestamp.value,
				Timestamp:      span.Timestamp.value,
				Data:           span.Data,
			}
			if len(span.Tags) > 0 {
				item.Tags = map[string]string(span.Tags)
			}
			evt.Spans = append(evt.Spans, item)
		}
	}

	return evt, nil
}

func parseNormalizedTimestamp(raw []byte) time.Time {
	if len(raw) == 0 {
		return time.Now().UTC()
	}

	// Dispatch by first byte to avoid wasted unmarshal attempts.
	switch raw[0] {
	case '"':
		if ts, err := strconv.Unquote(string(raw)); err == nil {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				return t.UTC()
			}
			if t, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
				return t.UTC()
			}
			if t, err := time.Parse("2006-01-02T15:04:05.000Z", ts); err == nil {
				return t.UTC()
			}
		}
	default:
		var epoch float64
		if err := json.Unmarshal(raw, &epoch); err == nil && epoch > 0 {
			sec := int64(epoch)
			nsec := int64((epoch - float64(sec)) * 1e9)
			return time.Unix(sec, nsec).UTC()
		}
	}

	return time.Now().UTC()
}

func normalizeEventID(id string) string {
	// Remove dashes, lowercase, ensure 32 hex chars
	id = strings.ReplaceAll(id, "-", "")
	id = strings.ToLower(id)
	if len(id) > 32 {
		id = id[:32]
	}
	return id
}

func normalizeLevel(level string) string {
	switch strings.ToLower(level) {
	case "fatal", "error", "warning", "info", "debug":
		return strings.ToLower(level)
	case "warn":
		return "warning"
	case "":
		return "error"
	default:
		return "error"
	}
}

func parseNormalizedTags(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	// Dispatch by first byte to avoid wasted unmarshal attempts.
	switch raw[0] {
	case '{':
		var objTags map[string]string
		if err := json.Unmarshal(raw, &objTags); err == nil {
			return objTags
		}
	case '[':
		// Try array-of-pairs format: [["key", "value"], ...]
		var arrTags [][2]string
		if err := json.Unmarshal(raw, &arrTags); err == nil {
			result := make(map[string]string, len(arrTags))
			for _, pair := range arrTags {
				result[pair[0]] = pair[1]
			}
			return result
		}
		// Try array-of-objects format: [{"key": "k", "value": "v"}, ...]
		var objectTags []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &objectTags); err == nil {
			result := make(map[string]string, len(objectTags))
			for _, item := range objectTags {
				if item.Key != "" {
					result[item.Key] = item.Value
				}
			}
			return result
		}
	}
	return nil
}

func parseNormalizedMessage(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	// Dispatch by first byte to avoid wasted unmarshal attempts.
	switch raw[0] {
	case '"':
		if s, err := strconv.Unquote(string(raw)); err == nil {
			return s
		}
	case '{':
		var obj struct {
			Formatted string `json:"formatted"`
			Message   string `json:"message"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil {
			if obj.Formatted != "" {
				return obj.Formatted
			}
			return obj.Message
		}
	}
	return ""
}

// Title extracts a human-readable title from the event.
func (e *Event) Title() string {
	if e.EventType() == "transaction" && e.Transaction != "" {
		return e.Transaction
	}
	if e.Exception != nil && len(e.Exception.Values) > 0 {
		last := e.Exception.Values[len(e.Exception.Values)-1]
		if last.Type != "" && last.Value != "" {
			return last.Type + ": " + last.Value
		}
		if last.Type != "" {
			return last.Type
		}
		if last.Value != "" {
			return last.Value
		}
	}
	if e.Message != "" {
		if len(e.Message) > 100 {
			return e.Message[:100]
		}
		return e.Message
	}
	return "<no title>"
}

// Culprit extracts the most relevant code location from the event.
func (e *Event) Culprit() string {
	if e.EventType() == "transaction" {
		if trace := e.TraceContext(); trace.Op != "" {
			return trace.Op
		}
		if e.Transaction != "" {
			return e.Transaction
		}
	}
	if e.Exception != nil {
		for i := len(e.Exception.Values) - 1; i >= 0; i-- {
			exc := e.Exception.Values[i]
			if exc.Stacktrace != nil {
				for j := len(exc.Stacktrace.Frames) - 1; j >= 0; j-- {
					f := exc.Stacktrace.Frames[j]
					if f.InApp != nil && *f.InApp {
						if f.Module != "" && f.Function != "" {
							return f.Module + " in " + f.Function
						}
						if f.Filename != "" && f.Function != "" {
							return f.Filename + " in " + f.Function
						}
						if f.Function != "" {
							return f.Function
						}
					}
				}
			}
		}
	}
	return ""
}

// EventType returns the normalized high-level event kind.
func (e *Event) EventType() string {
	switch strings.ToLower(strings.TrimSpace(e.Type)) {
	case "transaction":
		return "transaction"
	case "error", "":
		return "error"
	default:
		return strings.ToLower(strings.TrimSpace(e.Type))
	}
}

// TraceContext extracts trace identifiers from contexts.trace when present.
func (e *Event) TraceContext() Span {
	if len(e.Contexts) == 0 {
		return Span{}
	}
	traceRaw, ok := e.Contexts["trace"]
	if !ok {
		return Span{}
	}
	data, err := json.Marshal(traceRaw)
	if err != nil {
		return Span{}
	}
	var trace struct {
		TraceID      string `json:"trace_id"`
		SpanID       string `json:"span_id"`
		ParentSpanID string `json:"parent_span_id"`
		Op           string `json:"op"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(data, &trace); err != nil {
		return Span{}
	}
	return Span{
		TraceID:      trace.TraceID,
		SpanID:       trace.SpanID,
		ParentSpanID: trace.ParentSpanID,
		Op:           trace.Op,
		Status:       trace.Status,
	}
}
