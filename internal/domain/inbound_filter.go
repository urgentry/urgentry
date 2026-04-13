package domain

import (
	"context"
	"time"
)

// FilterType enumerates the supported inbound filter categories.
type FilterType string

const (
	FilterLegacyBrowser FilterType = "legacy_browsers"
	FilterLocalhost     FilterType = "localhost"
	FilterCrawler       FilterType = "web_crawlers"
	FilterIPRange       FilterType = "ip_range"
)

// InboundFilter is a single filter rule attached to a project.
type InboundFilter struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectId"`
	Type      FilterType `json:"type"`
	Active    bool       `json:"active"`
	Pattern   string     `json:"pattern,omitempty"` // UA substring or CIDR
	CreatedAt time.Time  `json:"dateCreated"`
	UpdatedAt time.Time  `json:"dateModified"`
}

// InboundFilterStore persists and queries inbound filter rules.
type InboundFilterStore interface {
	CreateFilter(ctx context.Context, f *InboundFilter) error
	GetFilter(ctx context.Context, id string) (*InboundFilter, error)
	ListFilters(ctx context.Context, projectID string) ([]*InboundFilter, error)
	UpdateFilter(ctx context.Context, f *InboundFilter) error
	DeleteFilter(ctx context.Context, id string) error
}
