package normalize

import (
	"encoding/json"
	"fmt"
	"time"
)

// ParsedFrame is a stack frame extracted from normalized event JSON.
type ParsedFrame struct {
	File       string
	Function   string
	LineNo     int
	ColNo      int
	InApp      bool
	MappedFrom string // e.g. "mapped from app.min.js" (empty if not source-mapped)
	CodeLines  []ParsedCodeLine
}

// ParsedCodeLine is a line of source code context.
type ParsedCodeLine struct {
	Number    int
	Content   string
	Highlight bool
}

// KVPair is a generic key-value pair used for tags, user info, and request data.
type KVPair struct {
	Key   string
	Value string
}

// ParsedBreadcrumb is a breadcrumb extracted from normalized event JSON.
type ParsedBreadcrumb struct {
	Level    string
	Time     string
	Category string
	Message  string
}

// parsedEvent is used to extract exception frames and user data from the stored normalized JSON.
type parsedEvent struct {
	Exception *struct {
		Values []struct {
			Stacktrace *struct {
				Frames []struct {
					Filename    string   `json:"filename"`
					Function    string   `json:"function"`
					Lineno      int      `json:"lineno"`
					Colno       int      `json:"colno"`
					AbsPath     string   `json:"abs_path"`
					InApp       *bool    `json:"in_app"`
					ContextLine string   `json:"context_line"`
					PreContext  []string `json:"pre_context"`
					PostContext []string `json:"post_context"`
				} `json:"frames"`
			} `json:"stacktrace"`
		} `json:"values"`
	} `json:"exception"`
	User *struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Username  string `json:"username"`
		IPAddress string `json:"ip_address"`
	} `json:"user"`
	Request *struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"request"`
	Breadcrumbs *struct {
		Values []struct {
			Timestamp interface{} `json:"timestamp"`
			Category  string      `json:"category"`
			Message   string      `json:"message"`
			Level     string      `json:"level"`
			Type      string      `json:"type"`
		} `json:"values"`
	} `json:"breadcrumbs"`
}

// ParseFrames extracts real stack frames from the normalized event JSON.
// When AbsPath is set (source-mapped event), it shows the resolved file:line and
// includes a "mapped from" annotation showing the original minified location.
func ParseFrames(rawJSON string) []ParsedFrame {
	var evt parsedEvent
	if err := json.Unmarshal([]byte(rawJSON), &evt); err != nil || evt.Exception == nil {
		return nil
	}

	var frames []ParsedFrame
	for _, exc := range evt.Exception.Values {
		if exc.Stacktrace == nil {
			continue
		}
		// Iterate frames in reverse (most recent call first, matching Sentry convention).
		for i := len(exc.Stacktrace.Frames) - 1; i >= 0; i-- {
			f := exc.Stacktrace.Frames[i]
			if f.Filename == "" && f.Function == "" {
				continue
			}

			sf := ParsedFrame{
				File:     f.Filename,
				Function: f.Function,
				LineNo:   f.Lineno,
				ColNo:    f.Colno,
				InApp:    f.InApp != nil && *f.InApp,
			}

			// If AbsPath is populated, the frame was source-mapped.
			// Show the minified filename only (we don't have original line:col anymore).
			if f.AbsPath != "" && f.AbsPath != f.Filename {
				sf.MappedFrom = fmt.Sprintf("mapped from %s", f.AbsPath)
			}

			// Build code context lines from the normalized event data.
			if f.ContextLine != "" || len(f.PreContext) > 0 || len(f.PostContext) > 0 {
				startLine := f.Lineno - len(f.PreContext)
				for pi, line := range f.PreContext {
					sf.CodeLines = append(sf.CodeLines, ParsedCodeLine{
						Number:    startLine + pi,
						Content:   line,
						Highlight: false,
					})
				}
				if f.ContextLine != "" {
					sf.CodeLines = append(sf.CodeLines, ParsedCodeLine{
						Number:    f.Lineno,
						Content:   f.ContextLine,
						Highlight: true,
					})
				}
				for pi, line := range f.PostContext {
					sf.CodeLines = append(sf.CodeLines, ParsedCodeLine{
						Number:    f.Lineno + 1 + pi,
						Content:   line,
						Highlight: false,
					})
				}
			}

			frames = append(frames, sf)
		}
	}
	return frames
}

// CountNativeFrames returns resolved and unresolved native frame counts from a normalized event payload.
func CountNativeFrames(rawJSON string) (int, int) {
	if rawJSON == "" {
		return 0, 0
	}
	var evt Event
	if err := json.Unmarshal([]byte(rawJSON), &evt); err != nil || evt.Exception == nil {
		return 0, 0
	}
	resolved, unresolved := 0, 0
	for _, exc := range evt.Exception.Values {
		if exc.Stacktrace == nil {
			continue
		}
		for _, frame := range exc.Stacktrace.Frames {
			if frame.InstructionAddr == "" && frame.DebugID == "" && frame.Package == "" {
				continue
			}
			if frame.Function != "" || frame.Filename != "" {
				resolved++
			} else {
				unresolved++
			}
		}
	}
	return resolved, unresolved
}

// ParseUser extracts user data from the normalized event JSON.
func ParseUser(rawJSON string) []KVPair {
	if rawJSON == "" {
		return nil
	}
	var evt parsedEvent
	if err := json.Unmarshal([]byte(rawJSON), &evt); err != nil || evt.User == nil {
		return nil
	}
	var pairs []KVPair
	if evt.User.ID != "" {
		pairs = append(pairs, KVPair{Key: "ID", Value: evt.User.ID})
	}
	if evt.User.Email != "" {
		pairs = append(pairs, KVPair{Key: "Email", Value: evt.User.Email})
	}
	if evt.User.Username != "" {
		pairs = append(pairs, KVPair{Key: "Username", Value: evt.User.Username})
	}
	if evt.User.IPAddress != "" {
		pairs = append(pairs, KVPair{Key: "IP Address", Value: evt.User.IPAddress})
	}
	return pairs
}

// ParseRequest extracts request data from the normalized event JSON.
func ParseRequest(rawJSON string) []KVPair {
	if rawJSON == "" {
		return nil
	}
	var evt parsedEvent
	if err := json.Unmarshal([]byte(rawJSON), &evt); err != nil || evt.Request == nil {
		return nil
	}
	var pairs []KVPair
	if evt.Request.Method != "" {
		pairs = append(pairs, KVPair{Key: "Method", Value: evt.Request.Method})
	}
	if evt.Request.URL != "" {
		pairs = append(pairs, KVPair{Key: "URL", Value: evt.Request.URL})
	}
	if host, ok := evt.Request.Headers["Host"]; ok {
		pairs = append(pairs, KVPair{Key: "Host", Value: host})
	}
	return pairs
}

// ParseNormalizedTags extracts tags from the normalized event JSON.
// Tags can be a map or an array of [key, value] pairs.
func ParseNormalizedTags(rawJSON string) []KVPair {
	if rawJSON == "" {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return nil
	}
	tagsRaw, ok := raw["tags"]
	if !ok {
		return nil
	}

	// Tags can be a map or an array of [key, value] pairs.
	var tagsMap map[string]string
	if err := json.Unmarshal(tagsRaw, &tagsMap); err == nil {
		var pairs []KVPair
		for k, v := range tagsMap {
			pairs = append(pairs, KVPair{Key: k, Value: v})
		}
		return pairs
	}

	var tagsArr [][]string
	if err := json.Unmarshal(tagsRaw, &tagsArr); err == nil {
		var pairs []KVPair
		for _, kv := range tagsArr {
			if len(kv) == 2 {
				pairs = append(pairs, KVPair{Key: kv[0], Value: kv[1]})
			}
		}
		return pairs
	}

	return nil
}

// ParseBreadcrumbs extracts breadcrumbs from the normalized event JSON.
func ParseBreadcrumbs(rawJSON string) []ParsedBreadcrumb {
	if rawJSON == "" {
		return nil
	}
	var evt parsedEvent
	if err := json.Unmarshal([]byte(rawJSON), &evt); err != nil || evt.Breadcrumbs == nil {
		return nil
	}
	var bcs []ParsedBreadcrumb
	for _, v := range evt.Breadcrumbs.Values {
		bc := ParsedBreadcrumb{
			Category: v.Category,
			Message:  v.Message,
			Level:    v.Level,
		}
		if bc.Level == "" {
			bc.Level = "info"
		}
		// Parse timestamp — can be string or float.
		switch ts := v.Timestamp.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				bc.Time = t.Format("15:04:05")
			} else {
				bc.Time = ts
			}
		case float64:
			bc.Time = time.Unix(int64(ts), 0).UTC().Format("15:04:05")
		}
		bcs = append(bcs, bc)
	}
	return bcs
}
