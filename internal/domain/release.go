// NOTE: These types are the target unification point. During the migration
// period, normalize.Event and issue.Group are still the active types.
// These will replace them once all packages are updated.
package domain

import "time"

// Release represents a release version.
type Release struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId,omitempty"`
	OrgSlug        string    `json:"-"`
	Version        string    `json:"version"`
	ShortVersion   string    `json:"shortVersion"`
	Ref            string    `json:"ref,omitempty"`
	URL            string    `json:"url,omitempty"`
	DateReleased   time.Time `json:"dateReleased,omitempty"`
	DateCreated    time.Time `json:"dateCreated"`
	NewGroups      int       `json:"newGroups"`
}

// Deploy represents a deployment of a release.
type Deploy struct {
	ID           string    `json:"id"`
	ReleaseID    string    `json:"releaseId"`
	Environment  string    `json:"environment"`
	Name         string    `json:"name,omitempty"`
	DateStarted  time.Time `json:"dateStarted,omitempty"`
	DateFinished time.Time `json:"dateFinished,omitempty"`
	DateCreated  time.Time `json:"dateCreated"`
}
