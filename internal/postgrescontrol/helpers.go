package postgrescontrol

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"urgentry/pkg/id"
)

func generateID() string {
	return id.New()
}

// nullIfEmpty returns nil for empty/whitespace-only strings, otherwise the trimmed value.
func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// nullStr extracts a plain string from a sql.NullString (empty string if not valid).
func nullStr(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

// nullString extracts a trimmed string from a sql.NullString (empty string if not valid).
func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}

// nullTime extracts a UTC time from a sql.NullTime (zero time if not valid).
func nullTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

// nullTimePtr extracts a *time.Time from a sql.NullTime (nil if not valid).
func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}

// optionalNullTime extracts a *time.Time from a sql.NullTime (nil if not valid).
// Alias retained for call-sites that read better with this name.
func optionalNullTime(value sql.NullTime) *time.Time {
	return nullTimePtr(value)
}

// optionalTime converts a *time.Time to a nullable DB value.
func optionalTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

// hashToken returns the hex-encoded SHA-256 hash of a raw token string.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// tokenPrefix extracts the prefix portion of a raw token for safe logging.
func tokenPrefix(raw string) string {
	if idx := strings.IndexByte(raw, '_'); idx >= 0 {
		rest := raw[idx+1:]
		if next := strings.IndexByte(rest, '_'); next >= 0 {
			return raw[:idx+1+next]
		}
	}
	if len(raw) <= 18 {
		return raw
	}
	return raw[:18]
}

// rawToken generates a new raw token string with the given prefix.
func rawToken(prefix string) string {
	return prefix + "_" + generateID()[:12] + "_" + generateID() + generateID()
}

// marshalJSON is a thin wrapper around json.Marshal for consistency.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// mustJSON marshals a value to JSON, panicking on error.
func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// itoa converts an int to a string (convenience wrapper for query building).
func itoa(v int) string {
	return strconv.Itoa(v)
}

// firstNonEmpty returns the first non-empty/non-whitespace string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
