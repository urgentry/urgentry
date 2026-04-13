package search

import "strings"

// Parse converts a raw search query string into a Filter struct.
// It lexes the input and then maps each token to the appropriate filter field.
func Parse(raw string) Filter {
	tokens := Lex(raw)
	return ParseTokens(tokens)
}

// ParseTokens converts a slice of tokens into a Filter.
func ParseTokens(tokens []Token) Filter {
	var f Filter

	for _, tok := range tokens {
		switch tok.Type {
		case IS:
			status := normalizeStatus(tok.Value)
			if status != "" {
				f.Status = status
			}
		case NOT_IS:
			status := normalizeStatus(tok.Value)
			if status != "" {
				f.NegatedStatuses = append(f.NegatedStatuses, status)
			}
		case HAS:
			f.HasFields = append(f.HasFields, strings.ToLower(tok.Key))
		case NOT_HAS:
			f.NotHasFields = append(f.NotHasFields, strings.ToLower(tok.Key))
		case KEY_VALUE:
			assignKeyValue(&f, tok.Key, tok.Value, false)
		case NEGATED_KEY_VALUE:
			assignKeyValue(&f, tok.Key, tok.Value, true)
		case TEXT:
			f.Terms = append(f.Terms, tok.Value)
		}
	}

	return f
}

func assignKeyValue(f *Filter, key, value string, negated bool) {
	lower := strings.ToLower(key)
	switch lower {
	case "level":
		if negated {
			f.NegLevel = strings.ToLower(value)
		} else {
			f.Level = strings.ToLower(value)
		}
	case "release":
		if negated {
			f.NegRelease = value
		} else {
			f.Release = value
		}
	case "environment", "env":
		if negated {
			f.NegEnv = value
		} else {
			f.Environment = value
		}
	case "event.type", "type":
		if negated {
			f.NegEventType = strings.ToLower(value)
		} else {
			f.EventType = strings.ToLower(value)
		}
	case "assigned":
		if negated {
			f.NegAssigned = value
		} else {
			f.Assigned = value
		}
	case "platform":
		if negated {
			f.NegPlatform = strings.ToLower(value)
		} else {
			f.Platform = strings.ToLower(value)
		}
	case "firstseen", "first_seen":
		f.FirstSeen = value
	case "lastseen", "last_seen":
		f.LastSeen = value
	case "times_seen", "timesseen":
		f.TimesSeen = value
	case "bookmarks":
		f.Bookmarked = value
	default:
		// Unknown key — treat as a tag filter.
		tf := TagFilter{Key: key, Value: value}
		if negated {
			f.NegTags = append(f.NegTags, tf)
		} else {
			f.Tags = append(f.Tags, tf)
		}
	}
}

func normalizeStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "unresolved", "open":
		return "unresolved"
	case "resolved", "closed":
		return "resolved"
	case "ignored":
		return "ignored"
	default:
		return ""
	}
}
