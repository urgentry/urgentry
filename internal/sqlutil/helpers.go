package sqlutil

import (
	"database/sql"
	"strings"
	"time"
)

// EscapeLike escapes SQL LIKE wildcards (% and _) so user input is treated
// literally. The queries that use this must include ESCAPE '\'.
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// NullStr extracts a string from a sql.NullString, returning "" if null.
func NullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// ParseDBTime parses a time string from the database.
// It tries RFC3339 first, then the "2006-01-02 15:04:05" format.
func ParseDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if len(s) > 10 {
		switch s[10] {
		case 'T':
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t
			}
			return time.Time{}
		case ' ':
			if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
				return t
			}
			return time.Time{}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// SortToOrderBy converts a sort field name to a SQL ORDER BY clause.
// The tablePrefix (e.g. "g.") is prepended to column names.
func SortToOrderBy(sort, tablePrefix string) string {
	p := tablePrefix
	switch sort {
	case "first_seen":
		return p + "first_seen DESC"
	case "events":
		return p + "times_seen DESC"
	case "priority":
		return p + "priority ASC, " + p + "last_seen DESC"
	default: // "last_seen"
		return p + "last_seen DESC"
	}
}
