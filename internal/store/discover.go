package store

import "time"

// DiscoverIssueSearchOptions captures the supported org issue list params.
type DiscoverIssueSearchOptions struct {
	Filter      string
	Query       string
	Environment string
	ProjectID   string
	Sort        string
	Limit       int
	StatsPeriod string
}

// DiscoverIssue is an org-wide issue row used by discover and cross-project views.
type DiscoverIssue struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	ProjectSlug     string    `json:"projectSlug"`
	ProjectName     string    `json:"projectName,omitempty"`
	ProjectPlatform string    `json:"projectPlatform,omitempty"`
	Release         string    `json:"release,omitempty"`
	Environment     string    `json:"environment,omitempty"`
	Title           string    `json:"title"`
	Culprit         string    `json:"culprit"`
	Level           string    `json:"level"`
	Status          string    `json:"status"`
	FirstSeen       time.Time `json:"firstSeen"`
	LastSeen        time.Time `json:"lastSeen"`
	Count           int64     `json:"count"`
	ShortID         int       `json:"shortId"`
	Priority        int       `json:"priority"`
	Assignee        string    `json:"assignee,omitempty"`
}

// DiscoverLog is a log-centric row for logs exploration and discover views.
type DiscoverLog struct {
	EventID     string            `json:"eventId"`
	ProjectID   string            `json:"projectId"`
	ProjectSlug string            `json:"projectSlug"`
	Title       string            `json:"title"`
	Message     string            `json:"message"`
	Level       string            `json:"level"`
	Platform    string            `json:"platform"`
	Culprit     string            `json:"culprit,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Release     string            `json:"release,omitempty"`
	Logger      string            `json:"logger,omitempty"`
	TraceID     string            `json:"traceId,omitempty"`
	SpanID      string            `json:"spanId,omitempty"`
	Timestamp   time.Time         `json:"timestamp"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// DiscoverTransaction is a transaction-centric row for discover views.
type DiscoverTransaction struct {
	EventID        string    `json:"eventId"`
	ProjectID      string    `json:"projectId"`
	ProjectSlug    string    `json:"projectSlug"`
	Transaction    string    `json:"transaction"`
	Op             string    `json:"op,omitempty"`
	Status         string    `json:"status,omitempty"`
	Platform       string    `json:"platform,omitempty"`
	Environment    string    `json:"environment,omitempty"`
	Release        string    `json:"release,omitempty"`
	TraceID        string    `json:"traceId"`
	SpanID         string    `json:"spanId"`
	StartTimestamp time.Time `json:"startTimestamp,omitempty"`
	EndTimestamp   time.Time `json:"endTimestamp,omitempty"`
	DurationMS     float64   `json:"durationMs"`
	Timestamp      time.Time `json:"timestamp"`
}
