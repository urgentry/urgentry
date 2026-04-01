// Package grouping implements deterministic event grouping.
//
// Events are assigned to groups (issues) based on their fingerprint,
// exception type+stacktrace, or message content. The algorithm is
// versioned so it can evolve safely.
package grouping

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"urgentry/internal/normalize"
)

const Version = "urgentry-v1"

// Result holds the grouping output for an event.
type Result struct {
	Version     string   `json:"version"`
	GroupingKey string   `json:"grouping_key"`
	Components  []string `json:"components"` // what contributed to the hash
}

// ComputeGrouping determines the grouping key for a normalized event.
// Priority chain (per Sentry public docs):
// 1. Explicit fingerprint
// 2. Stack trace (in-app frames)
// 3. Exception type + value
// 4. Message
func ComputeGrouping(evt *normalize.Event) Result {
	// 1. Explicit fingerprint
	if len(evt.Fingerprint) > 0 && !isDefaultFingerprint(evt.Fingerprint) {
		return groupByFingerprint(evt)
	}

	// 2. Stack trace based (if exception has frames)
	if evt.Exception != nil && len(evt.Exception.Values) > 0 {
		// Use the last (outermost) exception for grouping
		for i := len(evt.Exception.Values) - 1; i >= 0; i-- {
			exc := evt.Exception.Values[i]
			if exc.Stacktrace != nil && len(exc.Stacktrace.Frames) > 0 {
				inAppFrames := filterInApp(exc.Stacktrace.Frames)
				if len(inAppFrames) > 0 {
					return groupByStackTrace(exc.Type, inAppFrames)
				}
				// Fall through to all frames if none are in_app
				return groupByStackTrace(exc.Type, exc.Stacktrace.Frames)
			}
		}

		// 3. Exception type + value (no stacktrace)
		last := evt.Exception.Values[len(evt.Exception.Values)-1]
		return groupByException(last.Type, last.Value)
	}

	// 4. Message
	if evt.Message != "" {
		return groupByMessage(evt.Message)
	}

	// Fallback: event-level grouping (each event is its own group)
	return Result{
		Version:     Version,
		GroupingKey: hashComponents([]string{"single-event", evt.EventID}),
		Components:  []string{"event_id:" + evt.EventID},
	}
}

func isDefaultFingerprint(fp []string) bool {
	return len(fp) == 1 && fp[0] == "{{ default }}"
}

func groupByFingerprint(evt *normalize.Event) Result {
	components := make([]string, 0, len(evt.Fingerprint))
	for _, part := range evt.Fingerprint {
		if part == "{{ default }}" {
			// Mix in the default grouping hash
			defaultResult := computeDefault(evt)
			components = append(components, "default:"+defaultResult.GroupingKey)
		} else {
			components = append(components, "fingerprint:"+part)
		}
	}
	return Result{
		Version:     Version,
		GroupingKey: hashComponents(components),
		Components:  components,
	}
}

func computeDefault(evt *normalize.Event) Result {
	if evt.Exception != nil && len(evt.Exception.Values) > 0 {
		for i := len(evt.Exception.Values) - 1; i >= 0; i-- {
			exc := evt.Exception.Values[i]
			if exc.Stacktrace != nil && len(exc.Stacktrace.Frames) > 0 {
				inAppFrames := filterInApp(exc.Stacktrace.Frames)
				if len(inAppFrames) > 0 {
					return groupByStackTrace(exc.Type, inAppFrames)
				}
				return groupByStackTrace(exc.Type, exc.Stacktrace.Frames)
			}
		}
		last := evt.Exception.Values[len(evt.Exception.Values)-1]
		return groupByException(last.Type, last.Value)
	}
	if evt.Message != "" {
		return groupByMessage(evt.Message)
	}
	return Result{Version: Version, GroupingKey: "empty"}
}

func groupByStackTrace(excType string, frames []normalize.Frame) Result {
	var components []string
	if excType != "" {
		components = append(components, "type:"+excType)
	}
	for _, f := range frames {
		component := normalizeFrameForGrouping(f)
		components = append(components, component)
	}
	return Result{
		Version:     Version,
		GroupingKey: hashComponents(components),
		Components:  components,
	}
}

func groupByException(typ, value string) Result {
	components := []string{"type:" + typ, "value:" + value}
	return Result{
		Version:     Version,
		GroupingKey: hashComponents(components),
		Components:  components,
	}
}

func groupByMessage(msg string) Result {
	normalized := normalizeMessage(msg)
	components := []string{"message:" + normalized}
	return Result{
		Version:     Version,
		GroupingKey: hashComponents(components),
		Components:  components,
	}
}

func filterInApp(frames []normalize.Frame) []normalize.Frame {
	var result []normalize.Frame
	for _, f := range frames {
		if f.InApp != nil && *f.InApp {
			result = append(result, f)
		}
	}
	return result
}

// normalizeFrameForGrouping produces a stable string from a frame.
// Removes noisy values like hex addresses and generated names.
func normalizeFrameForGrouping(f normalize.Frame) string {
	var parts []string

	filename := f.Filename
	if f.Module != "" {
		parts = append(parts, "module:"+f.Module)
	} else if filename != "" {
		parts = append(parts, "file:"+normalizeFilename(filename))
	}

	fn := f.Function
	if fn != "" {
		fn = normalizeFunction(fn)
		parts = append(parts, "func:"+fn)
	}

	if len(parts) == 0 {
		return "frame:<unknown>"
	}
	return "frame:" + strings.Join(parts, "|")
}

var hexPattern = regexp.MustCompile(`0x[0-9a-fA-F]+`)
var generatedSuffix = regexp.MustCompile(`\$\d+$`)

func normalizeFunction(fn string) string {
	// Remove hex addresses (e.g., get_0x7f1234567890 → get_<hex>)
	fn = hexPattern.ReplaceAllString(fn, "<hex>")
	// Remove generated suffixes
	fn = generatedSuffix.ReplaceAllString(fn, "")
	return fn
}

func normalizeFilename(filename string) string {
	// Strip query strings and fragments from URLs
	if idx := strings.IndexByte(filename, '?'); idx >= 0 {
		filename = filename[:idx]
	}
	if idx := strings.IndexByte(filename, '#'); idx >= 0 {
		filename = filename[:idx]
	}
	return filename
}

// normalizeMessage strips dynamic values to group similar messages together.
var numberPattern = regexp.MustCompile(`\b\d+\b`)

func normalizeMessage(msg string) string {
	// Replace numbers with <int> to group "user 12345" and "user 67890"
	return numberPattern.ReplaceAllString(msg, "<int>")
}

func hashComponents(components []string) string {
	h := sha256.New()
	h.Write([]byte(Version))
	for _, c := range components {
		h.Write([]byte(c))
		h.Write([]byte{0}) // separator
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
