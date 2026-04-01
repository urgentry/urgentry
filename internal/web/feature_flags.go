package web

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// Feature Flag Context
// ---------------------------------------------------------------------------

// featureFlag represents a single flag parsed from the event contexts.
type featureFlag struct {
	Name   string
	Result string // "true"/"false" or variant string
}

// parseFeatureFlags extracts feature flags from the event payload's
// contexts.flags section. Returns nil if no flags context is present.
func parseFeatureFlags(payloadJSON string) []featureFlag {
	if payloadJSON == "" {
		return nil
	}

	var raw struct {
		Contexts map[string]json.RawMessage `json:"contexts"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &raw); err != nil || len(raw.Contexts) == 0 {
		return nil
	}

	flagsRaw, ok := raw.Contexts["flags"]
	if !ok {
		return nil
	}

	// Try parsing as {"values": [{"flag": "name", "result": true/false/string}]}
	// which is the Sentry feature flag context protocol.
	var structured struct {
		Values []struct {
			Flag   string `json:"flag"`
			Result any    `json:"result"`
		} `json:"values"`
	}
	if err := json.Unmarshal(flagsRaw, &structured); err == nil && len(structured.Values) > 0 {
		flags := make([]featureFlag, 0, len(structured.Values))
		for _, v := range structured.Values {
			flags = append(flags, featureFlag{
				Name:   v.Flag,
				Result: formatFlagResult(v.Result),
			})
		}
		return flags
	}

	// Fallback: try as a flat map {"flag_name": true/false/string}
	var flat map[string]any
	if err := json.Unmarshal(flagsRaw, &flat); err != nil {
		return nil
	}
	// Remove the "type" key that Sentry adds.
	delete(flat, "type")
	if len(flat) == 0 {
		return nil
	}

	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	flags := make([]featureFlag, 0, len(keys))
	for _, k := range keys {
		flags = append(flags, featureFlag{
			Name:   k,
			Result: formatFlagResult(flat[k]),
		})
	}
	return flags
}

func formatFlagResult(v any) string {
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
