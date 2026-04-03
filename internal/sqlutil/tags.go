// Package sqlutil provides shared helpers for database value handling.
package sqlutil

import "encoding/json"

// ParseTags unmarshals a JSON string into a map of tags.
// Returns nil if raw is empty or contains only "{}".
func ParseTags(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return nil
	}
	var tags map[string]string
	if json.Unmarshal([]byte(raw), &tags) != nil || len(tags) == 0 {
		return nil
	}
	return tags
}
