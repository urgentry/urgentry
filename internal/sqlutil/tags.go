// Package sqlutil provides shared helpers for database value handling.
package sqlutil

import "encoding/json"

// ParseTags unmarshals a JSON string into a map of tags.
// Returns an empty (non-nil) map if raw is empty or invalid.
func ParseTags(raw string) map[string]string {
	tags := make(map[string]string)
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &tags)
	}
	return tags
}
