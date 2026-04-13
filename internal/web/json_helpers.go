package web

import "encoding/json"

func prettyJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	formatted, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return raw
	}
	return string(formatted)
}
