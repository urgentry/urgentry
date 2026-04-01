package web

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// contextPanel is a structured section for a Sentry context (device, OS, browser, etc.).
type contextPanel struct {
	Title string
	Items []kvPair
}

// knownContextKeys defines the display title and which sub-keys to extract
// for well-known context types. Order matters for display.
var knownContextKeys = []struct {
	Key   string
	Title string
	Pick  []string // sub-keys to extract in order; empty = all
}{
	{"device", "Device", []string{"name", "family", "model", "model_id", "arch", "brand", "manufacturer", "memory_size", "storage_size", "simulator", "battery_level", "charging", "online", "orientation"}},
	{"os", "Operating System", []string{"name", "version", "build", "kernel_version", "rooted"}},
	{"browser", "Browser", []string{"name", "version"}},
	{"runtime", "Runtime", []string{"name", "version", "raw_description"}},
	{"app", "App", []string{"app_name", "app_version", "app_build", "app_identifier", "build_type", "app_start_time", "device_app_hash"}},
	{"gpu", "GPU", []string{"name", "version", "vendor_name", "vendor_id", "memory_size", "api_type", "multi_threaded_rendering", "npot_support"}},
}

// parseContextPanels extracts context sections from the normalized event JSON.
// It returns panels for known context types (device, os, browser, runtime, app, gpu)
// and also includes any unknown context types that contain key-value data.
// The "trace" context is excluded because it is already rendered elsewhere.
func parseContextPanels(payloadJSON string) []contextPanel {
	if payloadJSON == "" {
		return nil
	}

	var raw struct {
		Contexts map[string]json.RawMessage `json:"contexts"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &raw); err != nil || len(raw.Contexts) == 0 {
		return nil
	}

	var panels []contextPanel

	// Process known context types in defined order.
	seen := make(map[string]bool)
	for _, kc := range knownContextKeys {
		ctxRaw, ok := raw.Contexts[kc.Key]
		if !ok {
			continue
		}
		seen[kc.Key] = true

		items := extractContextItems(ctxRaw, kc.Pick)
		if len(items) == 0 {
			continue
		}
		panels = append(panels, contextPanel{Title: kc.Title, Items: items})
	}

	// Collect unknown context types (skip "trace" — rendered separately).
	var unknownKeys []string
	for key := range raw.Contexts {
		if seen[key] || key == "trace" {
			continue
		}
		unknownKeys = append(unknownKeys, key)
	}
	sort.Strings(unknownKeys)

	for _, key := range unknownKeys {
		items := extractContextItems(raw.Contexts[key], nil)
		if len(items) == 0 {
			continue
		}
		panels = append(panels, contextPanel{
			Title: contextTitle(key),
			Items: items,
		})
	}

	return panels
}

// extractContextItems unmarshals a context object and returns key-value pairs.
// If pick is non-empty, only those keys are extracted (in order). Otherwise all keys
// are returned sorted alphabetically.
func extractContextItems(raw json.RawMessage, pick []string) []kvPair {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	// Remove the "type" meta-key that Sentry adds to all contexts.
	delete(obj, "type")

	if len(pick) > 0 {
		var items []kvPair
		for _, key := range pick {
			val, ok := obj[key]
			if !ok {
				continue
			}
			s := formatContextValue(val)
			if s == "" {
				continue
			}
			items = append(items, kvPair{Key: contextLabel(key), Value: s})
		}
		return items
	}

	// All keys, sorted.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var items []kvPair
	for _, k := range keys {
		s := formatContextValue(obj[k])
		if s == "" {
			continue
		}
		items = append(items, kvPair{Key: contextLabel(k), Value: s})
	}
	return items
}

// formatContextValue converts an arbitrary JSON value to a display string.
func formatContextValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "yes"
		}
		return "no"
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	case nil:
		return ""
	default:
		// For arrays/maps, marshal back to compact JSON.
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}

// contextLabel converts a snake_case key to a human-readable label.
func contextLabel(key string) string {
	key = strings.ReplaceAll(key, "_", " ")
	if len(key) == 0 {
		return key
	}
	// Title-case first letter of each word.
	words := strings.Fields(key)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// contextTitle converts a context key to a panel title.
func contextTitle(key string) string {
	return contextLabel(key)
}
