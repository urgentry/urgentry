// Package integration defines the plugin framework for third-party
// integrations (e.g. GitHub, PagerDuty, generic webhooks). Each
// integration implements the Integration interface and is registered
// with a global Registry. Integration configs are persisted per-org
// (or per-project) in the integration_configs table.
package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Integration is the contract every connector must satisfy.
type Integration interface {
	// ID returns a stable machine identifier ("github", "pagerduty", etc.).
	ID() string
	// Name returns a human-readable display name.
	Name() string
	// Description returns a short summary for the UI.
	Description() string
	// ConfigSchema returns the set of fields the user must fill in to
	// install this integration.
	ConfigSchema() []ConfigField
	// OnEvent is called when an issue event occurs.
	OnEvent(ctx context.Context, config map[string]string, event EventPayload) error
	// OnAlert is called when an alert fires.
	OnAlert(ctx context.Context, config map[string]string, alert AlertPayload) error
	// OnWebhook is called for inbound webhook deliveries addressed to
	// this integration. Implementations may return a response body.
	OnWebhook(ctx context.Context, config map[string]string, payload []byte) ([]byte, error)
}

// ConfigField describes a single configuration field exposed to the UI.
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "string", "url", "secret", "boolean"
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	HelpText    string `json:"helpText,omitempty"`
}

// IntegrationConfig is the persisted per-install configuration row.
type IntegrationConfig struct {
	ID             string            `json:"id"`
	OrganizationID string            `json:"organizationId"`
	IntegrationID  string            `json:"integrationId"` // "github", "webhook", ...
	ProjectID      string            `json:"projectId,omitempty"`
	Config         map[string]string `json:"config"`
	Status         string            `json:"status"` // "active", "disabled"
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// EventPayload carries the data pushed to integrations on issue events.
type EventPayload struct {
	Action         string            `json:"action"` // "created", "resolved", "assigned", ...
	IssueID        string            `json:"issueId"`
	IssueTitle     string            `json:"issueTitle"`
	ProjectSlug    string            `json:"projectSlug"`
	OrganizationID string            `json:"organizationId"`
	EventID        string            `json:"eventId,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
}

// AlertPayload carries the data pushed to integrations on alert fires.
type AlertPayload struct {
	Action         string            `json:"action"` // "trigger" or "resolve"
	RuleID         string            `json:"ruleId"`
	RuleName       string            `json:"ruleName"`
	ProjectSlug    string            `json:"projectSlug"`
	OrganizationID string            `json:"organizationId"`
	Message        string            `json:"message"`
	Extra          map[string]string `json:"extra,omitempty"`
}

// Store abstracts persistence of IntegrationConfig rows.
type Store interface {
	Create(ctx context.Context, cfg *IntegrationConfig) error
	Get(ctx context.Context, id string) (*IntegrationConfig, error)
	ListByOrganization(ctx context.Context, orgID string) ([]*IntegrationConfig, error)
	Delete(ctx context.Context, id string) error
}

// InboundWebhookIntegration marks integrations that intentionally accept
// unauthenticated inbound webhook deliveries.
type InboundWebhookIntegration interface {
	HandlesInboundWebhook() bool
}

// WebhookVerifier validates inbound webhook requests before OnWebhook runs.
type WebhookVerifier interface {
	VerifyWebhook(config map[string]string, headers http.Header, payload []byte) error
}

// WebhookError lets integrations return a specific HTTP status code.
type WebhookError struct {
	StatusCode int
	Message    string
}

func (e *WebhookError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// AsWebhookError unwraps webhook-specific errors returned by verifiers or handlers.
func AsWebhookError(err error) (*WebhookError, bool) {
	var target *WebhookError
	if !errors.As(err, &target) || target == nil {
		return nil, false
	}
	return target, true
}

// Registry holds all known integration implementations. It is
// goroutine-safe after initial registration.
type Registry struct {
	mu           sync.RWMutex
	integrations map[string]Integration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{integrations: make(map[string]Integration)}
}

// Register adds an integration to the registry. Duplicate IDs panic.
func (r *Registry) Register(i Integration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.integrations[i.ID()]; exists {
		panic(fmt.Sprintf("integration %q already registered", i.ID()))
	}
	r.integrations[i.ID()] = i
}

// Get returns the integration with the given ID or nil.
func (r *Registry) Get(id string) Integration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.integrations[id]
}

// All returns every registered integration in no guaranteed order.
func (r *Registry) All() []Integration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Integration, 0, len(r.integrations))
	for _, i := range r.integrations {
		out = append(out, i)
	}
	return out
}

// NewBuiltinRegistry returns a registry pre-loaded with all built-in
// integrations (webhook, pagerduty, etc.).
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()
	r.Register(&WebhookIntegration{})
	r.Register(&PagerDutyIntegration{})
	r.Register(&JiraIntegration{})
	return r
}
