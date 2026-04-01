package sqlite

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func firstJSONString(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return strings.TrimSpace(value.String())
	case float64:
		return strconv.FormatInt(int64(value), 10)
	default:
		return ""
	}
}

func firstJSONObject(raw any) map[string]any {
	obj, _ := raw.(map[string]any)
	return obj
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseBoolAny(raw any) bool {
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(strings.ToLower(value))
		return value == "true" || value == "1" || value == "yes"
	case float64:
		return value != 0
	default:
		return false
	}
}

func parseInt64Any(raw any) int64 {
	switch value := raw.(type) {
	case nil:
		return 0
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		i, _ := value.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return i
	default:
		return 0
	}
}

func intFromAny(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		i, err := value.Int64()
		if err == nil {
			return int(i), true
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func canonicalizeJSONObject(raw []byte) ([]byte, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
