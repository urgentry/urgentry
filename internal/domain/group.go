// NOTE: These types are the target unification point. During the migration
// period, normalize.Event and issue.Group are still the active types.
// These will replace them once all packages are updated.
package domain

import "time"

// Group is the core issue entity. Events are grouped into issues by
// their grouping key, which is computed from the event's fingerprint,
// stack trace, exception type, or message.
type Group struct {
	ID              string
	ProjectID       string
	GroupingVersion string
	GroupingKey     string
	Title           string
	Culprit         string
	Level           string
	Status          string // "unresolved", "resolved", "ignored"
	FirstSeen       time.Time
	LastSeen        time.Time
	TimesSeen       int64
	LastEventID     string
}

// GroupListOpts controls filtering, pagination, and sorting for group queries.
type GroupListOpts struct {
	Limit       int
	Cursor      string
	Sort        string // "last_seen_desc" (default), "last_seen_asc", "first_seen_desc", "first_seen_asc", "times_seen_desc"
	Status      string
	Release     string
	Environment string
}
