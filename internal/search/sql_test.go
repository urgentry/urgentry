package search

import (
	"strings"
	"testing"
)

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func TestToSQL(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantClause string // substring that must appear in joined clauses
		wantArgs   int    // expected number of args
	}{
		{
			name:       "is:unresolved",
			input:      "is:unresolved",
			wantClause: "g.status = ?",
			wantArgs:   1,
		},
		{
			name:       "!is:resolved",
			input:      "!is:resolved",
			wantClause: "g.status != ?",
			wantArgs:   1,
		},
		{
			name:       "level:error",
			input:      "level:error",
			wantClause: "LOWER(COALESCE(g.level, '')) = ?",
			wantArgs:   1,
		},
		{
			name:       "!level:error",
			input:      "!level:error",
			wantClause: "LOWER(COALESCE(g.level, '')) != ?",
			wantArgs:   1,
		},
		{
			name:       "environment:production",
			input:      "environment:production",
			wantClause: "e.environment = ?",
			wantArgs:   1,
		},
		{
			name:       "!environment:staging",
			input:      "!environment:staging",
			wantClause: "NOT EXISTS",
			wantArgs:   1,
		},
		{
			name:       "release:1.2.3",
			input:      "release:1.2.3",
			wantClause: "e.release = ?",
			wantArgs:   1,
		},
		{
			name:       "event.type:error",
			input:      "event.type:error",
			wantClause: "LOWER(COALESCE(e.event_type, 'error')) = ?",
			wantArgs:   1,
		},
		{
			name:       "assigned:user@example.com",
			input:      "assigned:user@example.com",
			wantClause: "LOWER(COALESCE(g.assignee, '')) = ?",
			wantArgs:   1,
		},
		{
			name:       "has:assignee",
			input:      "has:assignee",
			wantClause: "COALESCE(g.assignee, '') != ''",
			wantArgs:   0,
		},
		{
			name:       "!has:assignee",
			input:      "!has:assignee",
			wantClause: "g.assignee IS NULL OR g.assignee = ''",
			wantArgs:   0,
		},
		{
			name:       "tag filter browser.name:Chrome",
			input:      "browser.name:Chrome",
			wantClause: "jt.key = ? AND jt.value = ?",
			wantArgs:   2,
		},
		{
			name:       "negated tag filter",
			input:      "!browser.name:Chrome",
			wantClause: "NOT EXISTS",
			wantArgs:   2,
		},
		{
			name:       "bare text",
			input:      "connection",
			wantClause: "g.title LIKE ?",
			wantArgs:   5,
		},
		{
			name:       "quoted text",
			input:      `"connection refused"`,
			wantClause: "g.title LIKE ?",
			wantArgs:   5,
		},
		{
			name:       "empty query produces no clauses",
			input:      "",
			wantClause: "",
			wantArgs:   0,
		},
		{
			name:       "has:release",
			input:      "has:release",
			wantClause: "COALESCE(g.resolved_in_release, '') != ''",
			wantArgs:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Parse(tt.input)
			sc := ToSQL(f, SQLite, "g", escapeLike)
			joined := strings.Join(sc.Clauses, " AND ")
			if tt.wantClause != "" && !strings.Contains(joined, tt.wantClause) {
				t.Errorf("clause does not contain %q\ngot: %s", tt.wantClause, joined)
			}
			if tt.wantClause == "" && len(sc.Clauses) != 0 {
				t.Errorf("expected no clauses, got: %s", joined)
			}
			if len(sc.Args) != tt.wantArgs {
				t.Errorf("got %d args, want %d\nargs: %v", len(sc.Args), tt.wantArgs, sc.Args)
			}
		})
	}
}

func TestParseComparisonValue(t *testing.T) {
	tests := []struct {
		input   string
		wantOp  string
		wantVal string
	}{
		{">10", ">", "10"},
		{"<100", "<", "100"},
		{">=2024-01-01", ">=", "2024-01-01"},
		{"<=2024-06-01T00:00:00Z", "<=", "2024-06-01T00:00:00Z"},
		{"!=abc", "!=", "abc"},
		{"=exact", "=", "exact"},
		{"bare_value", "=", "bare_value"},
		{"42", "=", "42"},
	}
	for _, tt := range tests {
		gotOp, gotVal := parseComparisonValue(tt.input)
		if gotOp != tt.wantOp || gotVal != tt.wantVal {
			t.Errorf("parseComparisonValue(%q) = (%q, %q), want (%q, %q)",
				tt.input, gotOp, gotVal, tt.wantOp, tt.wantVal)
		}
	}
}

func TestToSQL_AllQualifiers(t *testing.T) {
	f := Filter{
		Status:          "unresolved",
		NegatedStatuses: []string{"resolved"},
		Level:           "error",
		NegLevel:        "debug",
		Environment:     "production",
		NegEnv:          "staging",
		Release:         "1.0.0",
		NegRelease:      "0.9.0",
		EventType:       "error",
		NegEventType:    "transaction",
		Assigned:        "alice@example.com",
		NegAssigned:     "bob@example.com",
		Platform:        "python",
		NegPlatform:     "javascript",
		FirstSeen:       ">2024-01-01",
		LastSeen:        "<2024-06-01",
		TimesSeen:       ">10",
		Bookmarked:      "me",
		HasFields:       []string{"assignee"},
		NotHasFields:    []string{"release"},
		Tags:            []TagFilter{{Key: "browser", Value: "Chrome"}},
		NegTags:         []TagFilter{{Key: "os", Value: "Windows"}},
		Terms:           []string{"crash"},
	}

	sc := ToSQL(f, SQLite, "g", escapeLike)

	// Verify all qualifiers produce clauses.
	expectedFragments := []string{
		"g.status = ?",                           // is:
		"g.status != ?",                          // !is:
		"LOWER(COALESCE(g.level, '')) = ?",       // level:
		"LOWER(COALESCE(g.level, '')) != ?",      // !level:
		"e.environment = ?",                      // environment:
		"NOT EXISTS",                             // !environment: or !platform: or !tag
		"e.release = ?",                          // release:
		"LOWER(COALESCE(e.event_type, 'error'))", // event.type:
		"LOWER(COALESCE(g.assignee, '')) = ?",    // assigned:
		"LOWER(COALESCE(e.platform, '')) = ?",    // platform:
		"g.first_seen > ?",                       // firstSeen:
		"g.last_seen < ?",                        // lastSeen:
		"g.times_seen > ?",                       // times_seen:
		"COALESCE(g.assignee, '') != ''",         // has:assignee
		"g.resolved_in_release IS NULL",          // !has:release
		"jt.key = ?",                             // tag filter
		"g.title LIKE ?",                         // free text
	}

	joined := strings.Join(sc.Clauses, " AND ")
	for _, frag := range expectedFragments {
		if !strings.Contains(joined, frag) {
			t.Errorf("missing clause fragment %q in:\n%s", frag, joined)
		}
	}

	// Sanity check arg count — each qualifier produces at least 1 arg.
	if len(sc.Args) < 15 {
		t.Errorf("expected at least 15 args, got %d: %v", len(sc.Args), sc.Args)
	}
}

func TestToSQL_MultipleFilters(t *testing.T) {
	f := Parse("is:unresolved level:error has:assignee browser.name:Chrome connection")
	sc := ToSQL(f, SQLite, "g", escapeLike)

	if len(sc.Clauses) != 5 {
		t.Fatalf("expected 5 clauses, got %d: %v", len(sc.Clauses), sc.Clauses)
	}

	joined := strings.Join(sc.Clauses, " AND ")

	expectations := []string{
		"g.status = ?",
		"LOWER(COALESCE(g.level, '')) = ?",
		"COALESCE(g.assignee, '') != ''",
		"jt.key = ?",
		"g.title LIKE ?",
	}
	for _, exp := range expectations {
		if !strings.Contains(joined, exp) {
			t.Errorf("missing expected clause fragment %q in:\n%s", exp, joined)
		}
	}
}
