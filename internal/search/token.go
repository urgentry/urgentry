// Package search implements a structured search query parser for Sentry-like
// issue search syntax. It tokenizes, parses, and converts search queries
// into SQL WHERE clauses.
package search

// TokenType classifies a lexed search token.
type TokenType int

const (
	// TEXT is bare text for full-text search.
	TEXT TokenType = iota
	// KEY_VALUE is key:value (e.g. level:error).
	KEY_VALUE
	// NEGATED_KEY_VALUE is !key:value (e.g. !level:error).
	NEGATED_KEY_VALUE
	// HAS checks for non-null/non-empty (e.g. has:assignee).
	HAS
	// NOT_HAS checks for null/empty (e.g. !has:assignee).
	NOT_HAS
	// IS checks a status/state predicate (e.g. is:unresolved).
	IS
	// NOT_IS negates a status/state predicate (e.g. !is:resolved).
	NOT_IS
)

// Token is a single lexed element from a search query string.
type Token struct {
	Type  TokenType
	Key   string // For KEY_VALUE/HAS/IS tokens
	Value string // The value or bare text
}

// Filter is a parsed search filter ready for SQL generation.
type Filter struct {
	// Status filters (is:unresolved, is:resolved, is:ignored).
	Status   string
	NegatedStatuses []string // !is:resolved etc.

	// Field filters from key:value tokens.
	Level       string
	NegLevel    string // !level:error
	Release     string
	NegRelease  string
	Environment string
	NegEnv      string
	EventType   string
	NegEventType string
	Assigned    string   // assigned:email@example.com
	NegAssigned string
	Platform    string   // platform:python
	NegPlatform string
	FirstSeen   string   // firstSeen:>2024-01-01 (comparison op + value)
	LastSeen    string   // lastSeen:<2024-06-01
	TimesSeen   string   // times_seen:>10
	Bookmarked  string   // bookmarks:me

	// Tag filters (arbitrary key:value pairs like browser.name:Chrome).
	Tags    []TagFilter
	NegTags []TagFilter

	// Has/NotHas presence checks.
	HasFields    []string // has:assignee
	NotHasFields []string // !has:assignee

	// Free-text search terms.
	Terms []string
}

// TagFilter represents a tag key-value filter on event tags_json.
type TagFilter struct {
	Key   string
	Value string
}
