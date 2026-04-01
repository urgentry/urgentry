// NOTE: These types are the target unification point. During the migration
// period, normalize.Event and issue.Group are still the active types.
// These will replace them once all packages are updated.
package domain

import "time"

// Organization represents a Sentry organization.
type Organization struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	DateCreated time.Time `json:"dateCreated"`
}

// Project represents a Sentry project.
type Project struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	OrgSlug     string    `json:"organization,omitempty"`
	Platform    string    `json:"platform"`
	DateCreated time.Time `json:"dateCreated"`
	TeamSlug    string    `json:"team,omitempty"`
}

// Team represents a team within an organization.
type Team struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	OrgID       string    `json:"orgId,omitempty"`
	DateCreated time.Time `json:"dateCreated"`
}

// ProjectKey represents a DSN key for a project.
type ProjectKey struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Label       string    `json:"label"`
	Public      string    `json:"public"`
	Secret      string    `json:"secret"`
	IsActive    bool      `json:"isActive"`
	DSN         DSNURLs   `json:"dsn"`
	DateCreated time.Time `json:"dateCreated"`
}

// DSNURLs holds the public and secret DSN strings.
type DSNURLs struct {
	Public string `json:"public"`
	Secret string `json:"secret"`
}
