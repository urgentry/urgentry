// NOTE: These types are the target unification point. During the migration
// period, normalize.Event and issue.Group are still the active types.
// These will replace them once all packages are updated.
package domain

import (
	"encoding/json"
	"time"
)

// Event is the unified internal representation of an error event.
// Used by normalization, storage, and API layers.
type Event struct {
	ID             string            `json:"id,omitempty"`
	EventID        string            `json:"event_id"`
	ProjectID      string            `json:"projectID,omitempty"`
	GroupID        string            `json:"groupID,omitempty"`
	ReleaseID      string            `json:"releaseId,omitempty"`
	Timestamp      time.Time         `json:"timestamp"`
	Platform       string            `json:"platform"`
	Level          string            `json:"level"`
	Logger         string            `json:"logger,omitempty"`
	Transaction    string            `json:"transaction,omitempty"`
	ServerName     string            `json:"server_name,omitempty"`
	Release        string            `json:"release,omitempty"`
	Dist           string            `json:"dist,omitempty"`
	Environment    string            `json:"environment,omitempty"`
	Message        string            `json:"message,omitempty"`
	Title          string            `json:"title,omitempty"`
	Culprit        string            `json:"culprit,omitempty"`
	EventType      string            `json:"eventType,omitempty"`
	Fingerprint    []string          `json:"fingerprint,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	Extra          map[string]any    `json:"extra,omitempty"`
	Modules        map[string]string `json:"modules,omitempty"`
	User           *User             `json:"user,omitempty"`
	Request        *Request          `json:"request,omitempty"`
	Contexts       map[string]any    `json:"contexts,omitempty"`
	SDK            *SDK              `json:"sdk,omitempty"`
	Exception      *ExceptionList    `json:"exception,omitempty"`
	Breadcrumbs    *BreadcrumbList   `json:"breadcrumbs,omitempty"`
	NormalizedJSON json.RawMessage   `json:"-"`
	PayloadKey     string            `json:"-"` // blob store reference
	OccurredAt     time.Time         `json:"-"`
	IngestedAt     time.Time         `json:"-"`
}

// ExceptionList holds a list of exceptions.
type ExceptionList struct {
	Values []Exception `json:"values"`
}

// Exception represents a single exception in a chain.
type Exception struct {
	Type       string      `json:"type,omitempty"`
	Value      string      `json:"value,omitempty"`
	Module     string      `json:"module,omitempty"`
	ThreadID   int         `json:"thread_id,omitempty"`
	Mechanism  *Mechanism  `json:"mechanism,omitempty"`
	Stacktrace *Stacktrace `json:"stacktrace,omitempty"`
}

// Mechanism describes how the exception was captured.
type Mechanism struct {
	Type        string `json:"type,omitempty"`
	Handled     *bool  `json:"handled,omitempty"`
	Description string `json:"description,omitempty"`
}

// Stacktrace contains a list of frames.
type Stacktrace struct {
	Frames []Frame `json:"frames"`
}

// Frame is a single stack frame.
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

// User represents user information attached to an event.
type User struct {
	ID        string         `json:"id,omitempty"`
	Email     string         `json:"email,omitempty"`
	Username  string         `json:"username,omitempty"`
	IPAddress string         `json:"ip_address,omitempty"`
	Geo       map[string]any `json:"geo,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Request represents HTTP request information.
type Request struct {
	URL         string            `json:"url,omitempty"`
	Method      string            `json:"method,omitempty"`
	QueryString any               `json:"query_string,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Data        any               `json:"data,omitempty"`
	Cookies     any               `json:"cookies,omitempty"`
	Env         map[string]any    `json:"env,omitempty"`
}

// SDK identifies the sending SDK.
type SDK struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// BreadcrumbList holds a list of breadcrumbs.
type BreadcrumbList struct {
	Values []Breadcrumb `json:"values"`
}

// Breadcrumb represents a navigation/action breadcrumb.
type Breadcrumb struct {
	Timestamp any            `json:"timestamp,omitempty"` // string or float64
	Type      string         `json:"type,omitempty"`
	Category  string         `json:"category,omitempty"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Level     string         `json:"level,omitempty"`
}

// EventTitle extracts a human-readable title from the event.
func (e *Event) EventTitle() string {
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

// EventCulprit extracts the most relevant code location from the event.
func (e *Event) EventCulprit() string {
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
