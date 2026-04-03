package search

import "strings"

// SQLDialect controls placeholder style and string functions.
type SQLDialect int

const (
	// SQLite uses ? placeholders and LIKE for case-insensitive matching.
	SQLite SQLDialect = iota
	// Postgres uses $N placeholders and ILIKE.
	Postgres
)

// SQLClauses holds generated WHERE clause fragments and their bind args.
type SQLClauses struct {
	Clauses []string
	Args    []any
}

// ToSQL converts a Filter into SQL WHERE clause fragments.
// The caller joins them with AND and appends to their query.
// The groupAlias is the table alias for the groups table (e.g. "g").
// The escaper is a function that escapes LIKE wildcards.
func ToSQL(f Filter, _ SQLDialect, groupAlias string, escapeLike func(string) string) SQLClauses {
	var sc SQLClauses
	dot := groupAlias + "."
	if groupAlias == "" {
		dot = ""
	}

	// is: status filter
	if f.Status != "" {
		sc.add(dot+"status = ?", f.Status)
	}

	// !is: negated status
	for _, s := range f.NegatedStatuses {
		sc.add(dot+"status != ?", s)
	}

	// level:
	if f.Level != "" {
		sc.add("LOWER(COALESCE("+dot+"level, '')) = ?", f.Level)
	}
	if f.NegLevel != "" {
		sc.add("LOWER(COALESCE("+dot+"level, '')) != ?", f.NegLevel)
	}

	// environment: — uses an EXISTS subquery on events.
	if f.Environment != "" {
		sc.add(`EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND e.environment = ?
		)`, f.Environment)
	}
	if f.NegEnv != "" {
		sc.add(`NOT EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND e.environment = ?
		)`, f.NegEnv)
	}

	// release:
	if f.Release != "" {
		sc.add(`EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND e.release = ?
		)`, f.Release)
	}
	if f.NegRelease != "" {
		sc.add(`NOT EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND e.release = ?
		)`, f.NegRelease)
	}

	// event.type / type:
	if f.EventType != "" {
		sc.add(`EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND LOWER(COALESCE(e.event_type, 'error')) = ?
		)`, f.EventType)
	}
	if f.NegEventType != "" {
		sc.add(`NOT EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND LOWER(COALESCE(e.event_type, 'error')) = ?
		)`, f.NegEventType)
	}

	// platform:
	if f.Platform != "" {
		sc.add(`EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND LOWER(COALESCE(e.platform, '')) = ?
		)`, f.Platform)
	}
	if f.NegPlatform != "" {
		sc.add(`NOT EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id AND LOWER(COALESCE(e.platform, '')) = ?
		)`, f.NegPlatform)
	}

	// assigned:
	if f.Assigned != "" {
		sc.add("LOWER(COALESCE("+dot+"assignee, '')) = ?", strings.ToLower(f.Assigned))
	}
	if f.NegAssigned != "" {
		sc.add("LOWER(COALESCE("+dot+"assignee, '')) != ?", strings.ToLower(f.NegAssigned))
	}

	// firstSeen: / lastSeen: / times_seen: (comparison operators embedded in value)
	if f.FirstSeen != "" {
		if op, val := parseComparisonValue(f.FirstSeen); op != "" {
			sc.add(dot+"first_seen "+op+" ?", val)
		}
	}
	if f.LastSeen != "" {
		if op, val := parseComparisonValue(f.LastSeen); op != "" {
			sc.add(dot+"last_seen "+op+" ?", val)
		}
	}
	if f.TimesSeen != "" {
		if op, val := parseComparisonValue(f.TimesSeen); op != "" {
			sc.add(dot+"times_seen "+op+" ?", val)
		}
	}

	// bookmarks: accepted at parse level, requires outer context for SQL.
	// No SQL clause generated here — the caller handles bookmark filtering.

	// has: presence checks
	for _, field := range f.HasFields {
		col := hasFieldToColumn(field, dot)
		if col != "" {
			sc.Clauses = append(sc.Clauses, "COALESCE("+col+", '') != ''")
		}
	}

	// !has: absence checks
	for _, field := range f.NotHasFields {
		col := hasFieldToColumn(field, dot)
		if col != "" {
			sc.Clauses = append(sc.Clauses, "("+col+" IS NULL OR "+col+" = '')")
		}
	}

	// Tag filters (key:value on event tags_json).
	for _, tf := range f.Tags {
		sc.add(`EXISTS (
			SELECT 1 FROM events e, json_each(e.tags_json) jt
			WHERE e.group_id = `+dot+`id
			  AND e.tags_json IS NOT NULL AND e.tags_json != ''
			  AND jt.key = ? AND jt.value = ?
		)`, tf.Key, tf.Value)
	}
	for _, tf := range f.NegTags {
		sc.add(`NOT EXISTS (
			SELECT 1 FROM events e, json_each(e.tags_json) jt
			WHERE e.group_id = `+dot+`id
			  AND e.tags_json IS NOT NULL AND e.tags_json != ''
			  AND jt.key = ? AND jt.value = ?
		)`, tf.Key, tf.Value)
	}

	// Free-text search terms.
	for _, term := range f.Terms {
		like := "%" + escapeLike(term) + "%"
		sc.Clauses = append(sc.Clauses, `(`+dot+`title LIKE ? ESCAPE '\' OR `+dot+`culprit LIKE ? ESCAPE '\' OR EXISTS (
			SELECT 1 FROM events e
			WHERE e.group_id = `+dot+`id
			  AND (e.title LIKE ? ESCAPE '\' OR e.message LIKE ? ESCAPE '\' OR e.culprit LIKE ? ESCAPE '\')
		))`)
		sc.Args = append(sc.Args, like, like, like, like, like)
	}

	return sc
}

func (sc *SQLClauses) add(clause string, args ...any) {
	sc.Clauses = append(sc.Clauses, clause)
	sc.Args = append(sc.Args, args...)
}

// parseComparisonValue extracts an operator and value from strings like ">10" or "<=2024-01-01".
func parseComparisonValue(raw string) (string, string) {
	for _, prefix := range []string{">=", "<=", "!=", ">", "<", "="} {
		if strings.HasPrefix(raw, prefix) {
			val := strings.TrimSpace(raw[len(prefix):])
			if val != "" {
				return prefix, val
			}
		}
	}
	return "=", raw
}

// hasFieldToColumn maps a has: field name to a database column.
func hasFieldToColumn(field, dot string) string {
	switch field {
	case "assignee", "assigned":
		return dot + "assignee"
	case "release":
		return dot + "resolved_in_release"
	default:
		return ""
	}
}
