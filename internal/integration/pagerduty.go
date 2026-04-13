package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PagerDutyIntegration sends PagerDuty Events API v2 requests when
// alerts trigger or resolve. The integration key (routing key) is
// stored per-project in the integration config.
type PagerDutyIntegration struct{}

var _ Integration = (*PagerDutyIntegration)(nil)

const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

func (p *PagerDutyIntegration) ID() string          { return "pagerduty" }
func (p *PagerDutyIntegration) Name() string         { return "PagerDuty" }
func (p *PagerDutyIntegration) Description() string  { return "Create and resolve PagerDuty incidents from Urgentry alerts." }

func (p *PagerDutyIntegration) ConfigSchema() []ConfigField {
	return []ConfigField{
		{
			Key:         "integration_key",
			Label:       "Integration Key",
			Type:        "secret",
			Required:    true,
			Placeholder: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			HelpText:    "PagerDuty Events API v2 integration key (routing key) from your PagerDuty service.",
		},
	}
}

// OnEvent is a no-op — PagerDuty is alert-driven, not event-driven.
func (p *PagerDutyIntegration) OnEvent(_ context.Context, _ map[string]string, _ EventPayload) error {
	return nil
}

// OnAlert creates or resolves a PagerDuty incident depending on the
// alert payload action.
func (p *PagerDutyIntegration) OnAlert(ctx context.Context, config map[string]string, alert AlertPayload) error {
	integrationKey := config["integration_key"]
	if integrationKey == "" {
		return fmt.Errorf("pagerduty: missing integration_key in config")
	}

	action := alert.Action
	if action == "" {
		action = "trigger"
	}

	event := pdEvent{
		RoutingKey:  integrationKey,
		EventAction: action,
		DedupKey:    "urgentry-alert-" + alert.RuleID,
	}

	if action == "trigger" {
		event.Payload = &pdPayload{
			Summary:   alert.Message,
			Source:    fmt.Sprintf("urgentry/%s", alert.ProjectSlug),
			Severity:  pdSeverity(alert),
			Component: alert.ProjectSlug,
			Group:     alert.OrganizationID,
			CustomDetails: map[string]string{
				"rule_id":   alert.RuleID,
				"rule_name": alert.RuleName,
				"project":   alert.ProjectSlug,
			},
		}
	}

	return p.send(ctx, event)
}

// OnWebhook is a no-op — PagerDuty integration is outbound-only.
func (p *PagerDutyIntegration) OnWebhook(_ context.Context, _ map[string]string, _ []byte) ([]byte, error) {
	return []byte(`{"ok":true}`), nil
}

// pdEvent is the Events API v2 request body.
type pdEvent struct {
	RoutingKey  string     `json:"routing_key"`
	EventAction string     `json:"event_action"` // "trigger", "acknowledge", "resolve"
	DedupKey    string     `json:"dedup_key"`
	Payload     *pdPayload `json:"payload,omitempty"`
}

// pdPayload is the payload section of a trigger event.
type pdPayload struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"` // "critical", "error", "warning", "info"
	Component     string            `json:"component,omitempty"`
	Group         string            `json:"group,omitempty"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

func (p *PagerDutyIntegration) send(ctx context.Context, event pdEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("pagerduty: marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pagerDutyEventsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pagerduty: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pagerduty: remote returned %d", resp.StatusCode)
	}
	return nil
}

// pdSeverity maps alert metadata to a PagerDuty severity level.
func pdSeverity(alert AlertPayload) string {
	if level, ok := alert.Extra["level"]; ok {
		switch level {
		case "fatal", "critical":
			return "critical"
		case "error":
			return "error"
		case "warning":
			return "warning"
		}
	}
	return "error"
}
