// Package migration defines import/export payloads for Tiny-mode cutover.
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/alert"
)

// ImportRequest defines what to import.
type ImportRequest struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	SourceKind     string    `json:"sourceKind"` // "sentry-export", "json"
	Status         string    `json:"status"`     // "pending", "processing", "completed", "failed"
	CreatedAt      time.Time `json:"dateCreated"`
	UpdatedAt      time.Time `json:"dateModified"`
}

// ProjectImport represents imported project metadata.
type ProjectImport struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Slug     string   `json:"slug"`
	Platform string   `json:"platform,omitempty"`
	TeamSlug string   `json:"teamSlug,omitempty"`
	Teams    []string `json:"teams,omitempty"`
}

// ReleaseImport represents imported release metadata.
type ReleaseImport struct {
	ID           string `json:"id,omitempty"`
	Version      string `json:"version"`
	Ref          string `json:"ref,omitempty"`
	DateReleased string `json:"dateReleased,omitempty"`
}

// IssueImport represents a grouped issue record.
type IssueImport struct {
	ID              string `json:"id,omitempty"`
	ProjectSlug     string `json:"projectSlug"`
	Title           string `json:"title"`
	Culprit         string `json:"culprit,omitempty"`
	Level           string `json:"level,omitempty"`
	Status          string `json:"status,omitempty"`
	GroupingVersion string `json:"groupingVersion,omitempty"`
	GroupingKey     string `json:"groupingKey,omitempty"`
	FirstSeen       string `json:"firstSeen,omitempty"`
	LastSeen        string `json:"lastSeen,omitempty"`
	TimesSeen       int    `json:"timesSeen,omitempty"`
	ShortID         int    `json:"shortId,omitempty"`
	Assignee        string `json:"assignee,omitempty"`
	Priority        int    `json:"priority,omitempty"`
}

// EventImport represents a single error event.
type EventImport struct {
	ID             string            `json:"id,omitempty"`
	ProjectSlug    string            `json:"projectSlug"`
	EventID        string            `json:"eventId"`
	GroupID        string            `json:"groupId"`
	EventType      string            `json:"eventType,omitempty"`
	Release        string            `json:"release,omitempty"`
	Environment    string            `json:"environment,omitempty"`
	Platform       string            `json:"platform,omitempty"`
	Level          string            `json:"level,omitempty"`
	Title          string            `json:"title,omitempty"`
	Message        string            `json:"message,omitempty"`
	Culprit        string            `json:"culprit,omitempty"`
	OccurredAt     string            `json:"occurredAt,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	PayloadJSON    string            `json:"payloadJson,omitempty"`
	PayloadKey     string            `json:"payloadKey,omitempty"`
	UserIdentifier string            `json:"userIdentifier,omitempty"`
}

// ProjectKeyImport represents a project key.
type ProjectKeyImport struct {
	ID          string `json:"id,omitempty"`
	ProjectSlug string `json:"projectSlug"`
	PublicKey   string `json:"publicKey"`
	SecretKey   string `json:"secretKey,omitempty"`
	Status      string `json:"status,omitempty"`
	Label       string `json:"label,omitempty"`
	RateLimit   int    `json:"rateLimit,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// AlertRuleImport represents an alert rule.
type AlertRuleImport struct {
	ID          string            `json:"id,omitempty"`
	ProjectSlug string            `json:"projectSlug"`
	Name        string            `json:"name"`
	Status      string            `json:"status,omitempty"`
	RuleType    string            `json:"actionMatch,omitempty"`
	Conditions  []alert.Condition `json:"conditions,omitempty"`
	Actions     []alert.Action    `json:"actions,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
}

// MemberImport represents an organization member.
type MemberImport struct {
	ID          string `json:"id,omitempty"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
	Role        string `json:"role,omitempty"`
	IsActive    bool   `json:"isActive,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// ArtifactImport represents artifact metadata for attachments and release artifacts.
type ArtifactImport struct {
	Kind           string `json:"kind"` // attachment, source_map, proguard, apple, native, macho, elf, pe, ...
	ID             string `json:"id,omitempty"`
	ProjectSlug    string `json:"projectSlug"`
	ReleaseVersion string `json:"releaseVersion,omitempty"`
	EventID        string `json:"eventId,omitempty"`
	Name           string `json:"name"`
	ContentType    string `json:"contentType,omitempty"`
	UUID           string `json:"uuid,omitempty"`
	CodeID         string `json:"codeId,omitempty"`
	BuildID        string `json:"buildId,omitempty"`
	ModuleName     string `json:"moduleName,omitempty"`
	Architecture   string `json:"architecture,omitempty"`
	Platform       string `json:"platform,omitempty"`
	ObjectKey      string `json:"objectKey,omitempty"`
	Checksum       string `json:"checksum,omitempty"`
	Size           int64  `json:"size,omitempty"`
	BodyBase64     string `json:"bodyBase64,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
}

// ImportPayload is the top-level import JSON structure.
type ImportPayload struct {
	Projects    []ProjectImport    `json:"projects,omitempty"`
	Releases    []ReleaseImport    `json:"releases,omitempty"`
	Issues      []IssueImport      `json:"issues,omitempty"`
	Events      []EventImport      `json:"events,omitempty"`
	ProjectKeys []ProjectKeyImport `json:"projectKeys,omitempty"`
	AlertRules  []AlertRuleImport  `json:"alertRules,omitempty"`
	Members     []MemberImport     `json:"members,omitempty"`
	Artifacts   []ArtifactImport   `json:"artifacts,omitempty"`
}

// Importer processes import payloads.
type Importer struct {
	// Callbacks for creating resources
	CreateProject    func(ctx context.Context, orgID string, p ProjectImport) error
	CreateRelease    func(ctx context.Context, orgID string, r ReleaseImport) error
	CreateIssue      func(ctx context.Context, orgID string, i IssueImport) error
	CreateEvent      func(ctx context.Context, orgID string, e EventImport) error
	CreateProjectKey func(ctx context.Context, orgID string, k ProjectKeyImport) error
	CreateAlertRule  func(ctx context.Context, orgID string, a AlertRuleImport) error
	CreateMember     func(ctx context.Context, orgID string, m MemberImport) error
	CreateArtifact   func(ctx context.Context, orgID string, a ArtifactImport) error
}

// Import processes an import payload.
func (imp *Importer) Import(ctx context.Context, orgID string, data []byte) (*ImportResult, error) {
	var payload ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse import payload: %w", err)
	}

	result := &ImportResult{}

	for _, p := range payload.Projects {
		if imp.CreateProject != nil {
			if err := imp.CreateProject(ctx, orgID, p); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("project %s: %v", p.Slug, err))
				continue
			}
		}
		result.ProjectsImported++
	}

	for _, r := range payload.Releases {
		if imp.CreateRelease != nil {
			if err := imp.CreateRelease(ctx, orgID, r); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("release %s: %v", r.Version, err))
				continue
			}
		}
		result.ReleasesImported++
	}

	for _, i := range payload.Issues {
		if imp.CreateIssue != nil {
			if err := imp.CreateIssue(ctx, orgID, i); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("issue %s: %v", i.ID, err))
				continue
			}
		}
		result.IssuesImported++
	}

	for _, e := range payload.Events {
		if imp.CreateEvent != nil {
			if err := imp.CreateEvent(ctx, orgID, e); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("event %s: %v", e.EventID, err))
				continue
			}
		}
		result.EventsImported++
	}

	for _, k := range payload.ProjectKeys {
		if imp.CreateProjectKey != nil {
			if err := imp.CreateProjectKey(ctx, orgID, k); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("project key %s: %v", k.PublicKey, err))
				continue
			}
		}
		result.ProjectKeysImported++
	}

	for _, a := range payload.AlertRules {
		if imp.CreateAlertRule != nil {
			if err := imp.CreateAlertRule(ctx, orgID, a); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("alert rule %s: %v", a.Name, err))
				continue
			}
		}
		result.AlertRulesImported++
	}

	for _, m := range payload.Members {
		if imp.CreateMember != nil {
			if err := imp.CreateMember(ctx, orgID, m); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("member %s: %v", m.Email, err))
				continue
			}
		}
		result.MembersImported++
	}

	for _, a := range payload.Artifacts {
		if imp.CreateArtifact != nil {
			if err := imp.CreateArtifact(ctx, orgID, a); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("artifact %s: %v", a.Name, err))
				continue
			}
		}
		result.ArtifactsImported++
	}

	return result, nil
}

// ImportResult summarizes what was imported.
type ImportResult struct {
	ProjectsImported    int      `json:"projectsImported"`
	ReleasesImported    int      `json:"releasesImported"`
	IssuesImported      int      `json:"issuesImported"`
	EventsImported      int      `json:"eventsImported"`
	ProjectKeysImported int      `json:"projectKeysImported"`
	AlertRulesImported  int      `json:"alertRulesImported"`
	MembersImported     int      `json:"membersImported"`
	ArtifactsImported   int      `json:"artifactsImported"`
	ArtifactsVerified   int      `json:"artifactsVerified,omitempty"`
	DryRun              bool     `json:"dryRun,omitempty"`
	Errors              []string `json:"errors,omitempty"`
}
