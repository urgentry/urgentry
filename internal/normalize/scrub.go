// Package normalize — scrub.go provides PII data scrubbing applied during
// event normalization before storage. Scrubbing rules match credit card
// numbers, password-like field names, IP addresses, and email addresses,
// replacing matched values with masked placeholders. Projects can supply
// additional custom field names via ScrubConfig.
package normalize

import (
	"encoding/json"
	"regexp"
	"strings"
)

const maskedValue = "[Filtered]"

// ScrubConfig controls per-project scrubbing behavior.
type ScrubConfig struct {
	// Enabled turns scrubbing on/off. Default true.
	Enabled bool `json:"enabled"`

	// ExtraFields are additional dot-path or field-name patterns to scrub
	// (e.g. "ssn", "tax_id"). These are matched case-insensitively against
	// JSON object keys at any nesting depth.
	ExtraFields []string `json:"extra_fields,omitempty"`
}

// DefaultScrubConfig returns the default scrubbing configuration.
func DefaultScrubConfig() ScrubConfig {
	return ScrubConfig{Enabled: true}
}

// Compiled regexes — built once at init time.
var (
	// Credit card: 13–19 digit sequences that may contain dashes or spaces.
	creditCardRe = regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`)

	// IPv4 address.
	ipv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

	// IPv6 address (simplified: 4+ hex groups separated by colons).
	ipv6Re = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b`)

	// Email address.
	emailRe = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
)

// defaultSensitiveKeys are field name substrings that indicate a sensitive
// value, matched case-insensitively.
var defaultSensitiveKeys = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"apikey",
	"access_key",
	"auth",
	"credentials",
	"private_key",
	"credit_card",
	"creditcard",
	"card_number",
	"cardnumber",
	"cvv",
	"cvc",
	"ssn",
}

// ScrubEvent applies PII scrubbing rules to a normalized Event in place.
// If cfg is nil, default rules are used.
func ScrubEvent(evt *Event, cfg *ScrubConfig) {
	if evt == nil {
		return
	}
	c := DefaultScrubConfig()
	if cfg != nil {
		c = *cfg
	}
	if !c.Enabled {
		return
	}

	sensitiveKeys := buildSensitiveKeys(c.ExtraFields)

	// Scrub tags.
	if evt.Tags != nil {
		scrubMap(evt.Tags, sensitiveKeys)
	}

	// Scrub extra.
	if evt.Extra != nil {
		scrubAnyMap(evt.Extra, sensitiveKeys)
	}

	// Scrub user data.
	if evt.User != nil {
		evt.User.Email = scrubString(evt.User.Email)
		evt.User.IPAddress = maskIP(evt.User.IPAddress)
		if evt.User.Data != nil {
			scrubAnyMap(evt.User.Data, sensitiveKeys)
		}
	}

	// Scrub request data.
	if evt.Request != nil {
		scrubRequestHeaders(evt.Request.Headers, sensitiveKeys)
		if evt.Request.Cookies != nil {
			evt.Request.Cookies = maskedValue
		}
		if evt.Request.Data != nil {
			evt.Request.Data = scrubAny(evt.Request.Data, sensitiveKeys)
		}
		if evt.Request.Env != nil {
			scrubAnyMap(evt.Request.Env, sensitiveKeys)
		}
	}

	// Scrub breadcrumb data maps.
	if evt.Breadcrumbs != nil {
		for i := range evt.Breadcrumbs.Values {
			if evt.Breadcrumbs.Values[i].Data != nil {
				scrubAnyMap(evt.Breadcrumbs.Values[i].Data, sensitiveKeys)
			}
		}
	}

	// Scrub contexts.
	if evt.Contexts != nil {
		scrubAnyMap(evt.Contexts, sensitiveKeys)
	}

	// Scrub message body.
	evt.Message = scrubString(evt.Message)
}

// buildSensitiveKeys produces the full list of sensitive key substrings.
func buildSensitiveKeys(extra []string) []string {
	keys := make([]string, len(defaultSensitiveKeys), len(defaultSensitiveKeys)+len(extra))
	copy(keys, defaultSensitiveKeys)
	for _, k := range extra {
		k = strings.TrimSpace(strings.ToLower(k))
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// isSensitiveKey reports whether a JSON field name looks sensitive.
func isSensitiveKey(key string, sensitiveKeys []string) bool {
	lower := strings.ToLower(key)
	for _, sk := range sensitiveKeys {
		if strings.Contains(lower, sk) {
			return true
		}
	}
	return false
}

// scrubString masks credit card numbers, emails, and IP addresses in a string.
func scrubString(s string) string {
	if s == "" {
		return s
	}
	s = creditCardRe.ReplaceAllString(s, maskedValue)
	s = emailRe.ReplaceAllString(s, maskedValue)
	s = ipv4Re.ReplaceAllString(s, maskedValue)
	s = ipv6Re.ReplaceAllString(s, maskedValue)
	return s
}

// maskIP replaces a full IP address with a masked version.
func maskIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ip
	}
	// IPv4: keep first two octets, mask the rest.
	if parts := strings.Split(ip, "."); len(parts) == 4 {
		return parts[0] + "." + parts[1] + ".0.0"
	}
	// IPv6: mask entirely.
	if strings.Contains(ip, ":") {
		return maskedValue
	}
	return ip
}

// scrubMap scrubs string-valued maps in place.
func scrubMap(m map[string]string, sensitiveKeys []string) {
	for k, v := range m {
		if isSensitiveKey(k, sensitiveKeys) {
			m[k] = maskedValue
		} else {
			m[k] = scrubString(v)
		}
	}
}

// scrubAnyMap scrubs map[string]any in place.
func scrubAnyMap(m map[string]any, sensitiveKeys []string) {
	for k, v := range m {
		if isSensitiveKey(k, sensitiveKeys) {
			m[k] = maskedValue
		} else {
			m[k] = scrubAny(v, sensitiveKeys)
		}
	}
}

// scrubAny recursively scrubs values of any type.
func scrubAny(v any, sensitiveKeys []string) any {
	switch val := v.(type) {
	case string:
		return scrubString(val)
	case map[string]any:
		scrubAnyMap(val, sensitiveKeys)
		return val
	case []any:
		for i, elem := range val {
			val[i] = scrubAny(elem, sensitiveKeys)
		}
		return val
	case json.RawMessage:
		return json.RawMessage(scrubString(string(val)))
	default:
		return v
	}
}

// scrubRequestHeaders scrubs HTTP request headers in place.
func scrubRequestHeaders(headers map[string]string, sensitiveKeys []string) {
	if headers == nil {
		return
	}
	for k, v := range headers {
		lower := strings.ToLower(k)
		// Always scrub authorization and cookie headers.
		if lower == "authorization" || lower == "cookie" || lower == "set-cookie" {
			headers[k] = maskedValue
			continue
		}
		if isSensitiveKey(k, sensitiveKeys) {
			headers[k] = maskedValue
		} else {
			headers[k] = scrubString(v)
		}
	}
}
