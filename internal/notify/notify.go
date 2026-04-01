// Package notify delivers alert notifications via email, webhook, and future
// delivery kinds such as Slack.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/alert"
	"github.com/rs/zerolog/log"
)

const (
	DeliveryKindEmail   = "email"
	DeliveryKindWebhook = "webhook"
	DeliveryKindSlack   = "slack"

	DeliveryStatusQueued    = "queued"
	DeliveryStatusDelivered = "delivered"
	DeliveryStatusFailed    = "failed"
)

// Destination defines where to send notifications.
type Destination struct {
	ID     string `json:"id"`
	OrgID  string `json:"organizationId"`
	Kind   string `json:"kind"` // "email", "webhook", "slack"
	Name   string `json:"name"`
	Config any    `json:"config"`
}

// EmailConfig holds email notification settings.
type EmailConfig struct {
	To       []string `json:"to"`
	SMTPHost string   `json:"smtp_host"`
	SMTPPort string   `json:"smtp_port"`
	From     string   `json:"from"`
}

// WebhookConfig holds webhook notification settings.
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// EmailNotification captures a notification that should be delivered or
// exposed in Tiny mode.
type EmailNotification struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectId"`
	RuleID    string     `json:"ruleId"`
	GroupID   string     `json:"groupId"`
	EventID   string     `json:"eventId"`
	Recipient string     `json:"recipient"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	Transport string     `json:"transport"`
	Status    string     `json:"status"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
}

// EmailOutbox stores Tiny-mode outbound email notifications.
type EmailOutbox interface {
	RecordEmail(ctx context.Context, notification *EmailNotification) error
}

// DeliveryRecord captures the outcome of a webhook or email delivery attempt.
type DeliveryRecord struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"projectId"`
	RuleID         string     `json:"ruleId,omitempty"`
	GroupID        string     `json:"groupId,omitempty"`
	EventID        string     `json:"eventId,omitempty"`
	Kind           string     `json:"kind"`
	Target         string     `json:"target"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	ResponseStatus *int       `json:"responseStatus,omitempty"`
	Error          string     `json:"error,omitempty"`
	PayloadJSON    string     `json:"payloadJson,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastAttemptAt  *time.Time `json:"lastAttemptAt,omitempty"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
}

// DeliveryRecorder stores recent delivery outcomes for UI and admin surfaces.
type DeliveryRecorder interface {
	RecordDelivery(ctx context.Context, delivery *DeliveryRecord) error
}

// Notifier dispatches alert trigger events to their destinations.
type Notifier struct {
	HTTPClient       *http.Client
	EmailOutbox      EmailOutbox
	DeliveryRecorder DeliveryRecorder
	RetryDelays      []time.Duration
}

func NewNotifier(outbox EmailOutbox, deliveries DeliveryRecorder) *Notifier {
	return &Notifier{
		EmailOutbox:      outbox,
		DeliveryRecorder: deliveries,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				MaxConnsPerHost:     10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		RetryDelays: []time.Duration{0, 250 * time.Millisecond, 750 * time.Millisecond},
	}
}

// NotifyWebhook sends a webhook notification.
func (n *Notifier) NotifyWebhook(ctx context.Context, projectID, url string, trigger alert.TriggerEvent) error {
	body := map[string]any{
		"action":      "triggered",
		"rule_id":     trigger.RuleID,
		"group_id":    trigger.GroupID,
		"event_id":    trigger.EventID,
		"event_type":  trigger.EventType,
		"trace_id":    trigger.TraceID,
		"transaction": trigger.Transaction,
		"duration_ms": trigger.DurationMS,
		"timestamp":   trigger.Timestamp.Format(time.RFC3339),
	}
	if trigger.Profile != nil {
		body["profile"] = trigger.Profile
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	return n.postJSON(ctx, &DeliveryRecord{
		ProjectID:   projectID,
		RuleID:      trigger.RuleID,
		GroupID:     trigger.GroupID,
		EventID:     trigger.EventID,
		Kind:        DeliveryKindWebhook,
		Target:      strings.TrimSpace(url),
		Status:      DeliveryStatusQueued,
		PayloadJSON: string(payload),
		CreatedAt:   time.Now().UTC(),
	}, payload)
}

// NotifySlack sends a Slack incoming-webhook notification.
func (n *Notifier) NotifySlack(ctx context.Context, projectID, url string, trigger alert.TriggerEvent) error {
	payload, err := json.Marshal(map[string]any{
		"text": slackText(trigger),
	})
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}
	return n.postJSON(ctx, &DeliveryRecord{
		ProjectID:   projectID,
		RuleID:      trigger.RuleID,
		GroupID:     trigger.GroupID,
		EventID:     trigger.EventID,
		Kind:        DeliveryKindSlack,
		Target:      strings.TrimSpace(url),
		Status:      DeliveryStatusQueued,
		PayloadJSON: string(payload),
		CreatedAt:   time.Now().UTC(),
	}, payload)
}

// SendTestWebhook issues a synthetic probe and records the result.
func (n *Notifier) SendTestWebhook(ctx context.Context, projectID, url string) error {
	payload, err := json.Marshal(map[string]any{
		"action":    "test",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal test webhook payload: %w", err)
	}
	return n.postJSON(ctx, &DeliveryRecord{
		ProjectID:   projectID,
		Kind:        DeliveryKindWebhook,
		Target:      strings.TrimSpace(url),
		Status:      DeliveryStatusQueued,
		PayloadJSON: string(payload),
		CreatedAt:   time.Now().UTC(),
	}, payload)
}

func (n *Notifier) postJSON(ctx context.Context, delivery *DeliveryRecord, payload []byte) error {
	target := strings.TrimSpace(delivery.Target)
	if target == "" {
		return fmt.Errorf("empty webhook target")
	}

	var lastErr error
	for attempt, delay := range n.RetryDelays {
		if attempt > 0 && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
		if err != nil {
			lastErr = fmt.Errorf("create webhook request: %w", err)
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "urgentry-alert/1.0")

		now := time.Now().UTC()
		resp, err := n.HTTPClient.Do(req)
		delivery.Attempts = attempt + 1
		delivery.LastAttemptAt = &now
		if err != nil {
			lastErr = fmt.Errorf("webhook request: %w", err)
			delivery.Status = DeliveryStatusFailed
			delivery.Error = lastErr.Error()
			continue
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			log.Warn().
				Err(err).
				Str("target", target).
				Str("kind", delivery.Kind).
				Msg("failed to drain notification response body")
		}
		resp.Body.Close()

		statusCode := resp.StatusCode
		delivery.ResponseStatus = &statusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			delivery.Status = DeliveryStatusDelivered
			delivery.Error = ""
			deliveredAt := now
			delivery.DeliveredAt = &deliveredAt
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
		delivery.Status = DeliveryStatusFailed
		delivery.Error = lastErr.Error()
	}
	if lastErr != nil && delivery.DeliveredAt == nil {
		delivery.Status = DeliveryStatusFailed
		delivery.Error = lastErr.Error()
	}
	if n.DeliveryRecorder != nil {
		if err := n.DeliveryRecorder.RecordDelivery(ctx, delivery); err != nil {
			log.Warn().
				Err(err).
				Str("project_id", delivery.ProjectID).
				Str("kind", delivery.Kind).
				Str("target", delivery.Target).
				Msg("failed to record notification delivery")
		}
	}
	return lastErr
}

// NotifyEmail records a Tiny-mode email notification. If SMTP is configured
// in the future, this can fan out to a real transport after persistence.
func (n *Notifier) NotifyEmail(ctx context.Context, projectID, recipient string, trigger alert.TriggerEvent) error {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return fmt.Errorf("empty email recipient")
	}

	subject, body := emailContent(trigger)

	if n.EmailOutbox == nil {
		return fmt.Errorf("email outbox not configured")
	}

	if err := n.EmailOutbox.RecordEmail(ctx, &EmailNotification{
		ID:        "",
		ProjectID: projectID,
		RuleID:    trigger.RuleID,
		GroupID:   trigger.GroupID,
		EventID:   trigger.EventID,
		Recipient: recipient,
		Subject:   subject,
		Body:      body,
		Transport: "tiny-outbox",
		Status:    DeliveryStatusQueued,
		CreatedAt: trigger.Timestamp,
	}); err != nil {
		return err
	}
	if n.DeliveryRecorder != nil {
		if err := n.DeliveryRecorder.RecordDelivery(ctx, &DeliveryRecord{
			ProjectID:   projectID,
			RuleID:      trigger.RuleID,
			GroupID:     trigger.GroupID,
			EventID:     trigger.EventID,
			Kind:        DeliveryKindEmail,
			Target:      recipient,
			Status:      DeliveryStatusQueued,
			Attempts:    1,
			PayloadJSON: body,
			CreatedAt:   trigger.Timestamp,
		}); err != nil {
			log.Warn().
				Err(err).
				Str("project_id", projectID).
				Str("kind", DeliveryKindEmail).
				Str("target", recipient).
				Msg("failed to record email delivery")
		}
	}
	return nil
}

func emailContent(trigger alert.TriggerEvent) (string, string) {
	if trigger.EventType == alert.EventTypeTransaction {
		name := strings.TrimSpace(trigger.Transaction)
		if name == "" {
			name = firstNonEmpty(strings.TrimSpace(trigger.TraceID), trigger.EventID)
		}
		subject := fmt.Sprintf("[Urgentry Alert] Slow transaction %s", name)
		body := fmt.Sprintf("Performance alert triggered:\n  Rule: %s\n  Transaction: %s\n  Trace: %s\n  Event: %s\n  Duration: %.0fms\n  Time: %s",
			trigger.RuleID, name, trigger.TraceID, trigger.EventID, trigger.DurationMS, trigger.Timestamp.Format(time.RFC3339))
		body = appendProfileSummary(body, trigger.Profile)
		return subject, body
	}
	if trigger.EventType == alert.EventTypeMonitor {
		name := strings.TrimSpace(trigger.MonitorSlug)
		if name == "" {
			name = trigger.EventID
		}
		subject := fmt.Sprintf("[Urgentry Alert] Monitor %s missed", name)
		body := fmt.Sprintf("Monitor alert triggered:\n  Rule: %s\n  Monitor: %s\n  Status: %s\n  Event: %s\n  Time: %s",
			trigger.RuleID, name, trigger.Status, trigger.EventID, trigger.Timestamp.Format(time.RFC3339))
		return subject, body
	}
	if trigger.EventType == alert.EventTypeRelease {
		name := strings.TrimSpace(trigger.Release)
		if name == "" {
			name = trigger.EventID
		}
		subject := fmt.Sprintf("[Urgentry Alert] Release %s health changed", name)
		body := fmt.Sprintf("Release alert triggered:\n  Rule: %s\n  Release: %s\n  Crash-free: %.1f%%\n  Sessions: %d\n  Affected users: %d\n  Time: %s",
			trigger.RuleID, name, trigger.CrashFreeRate, trigger.SessionCount, trigger.AffectedUsers, trigger.Timestamp.Format(time.RFC3339))
		body = appendProfileSummary(body, trigger.Profile)
		return subject, body
	}
	subject := fmt.Sprintf("[Urgentry Alert] Rule %s triggered", trigger.RuleID)
	body := fmt.Sprintf("Alert triggered:\n  Rule: %s\n  Group: %s\n  Event: %s\n  Time: %s",
		trigger.RuleID, trigger.GroupID, trigger.EventID, trigger.Timestamp.Format(time.RFC3339))
	return subject, body
}

func slackText(trigger alert.TriggerEvent) string {
	if trigger.EventType == alert.EventTypeTransaction {
		name := strings.TrimSpace(trigger.Transaction)
		if name == "" {
			name = firstNonEmpty(strings.TrimSpace(trigger.TraceID), trigger.EventID)
		}
		text := fmt.Sprintf("Urgentry slow transaction alert: %s took %.0fms (rule %s)", name, trigger.DurationMS, trigger.RuleID)
		return appendSlackProfileSummary(text, trigger.Profile)
	}
	if trigger.EventType == alert.EventTypeMonitor {
		name := strings.TrimSpace(trigger.MonitorSlug)
		if name == "" {
			name = trigger.EventID
		}
		return fmt.Sprintf("Urgentry monitor alert: %s missed (rule %s)", name, trigger.RuleID)
	}
	if trigger.EventType == alert.EventTypeRelease {
		name := strings.TrimSpace(trigger.Release)
		if name == "" {
			name = trigger.EventID
		}
		text := fmt.Sprintf("Urgentry release alert: %s crash-free %.1f%% (rule %s)", name, trigger.CrashFreeRate, trigger.RuleID)
		return appendSlackProfileSummary(text, trigger.Profile)
	}
	return fmt.Sprintf("Urgentry alert fired: rule %s event %s group %s", trigger.RuleID, trigger.EventID, trigger.GroupID)
}

func appendProfileSummary(body string, profile *alert.ProfileContext) string {
	if profile == nil {
		return body
	}
	lines := []string{
		"  Profile: " + firstNonEmpty(profile.ProfileID, "linked"),
	}
	if strings.TrimSpace(profile.URL) != "" {
		lines = append(lines, "  Profile URL: "+strings.TrimSpace(profile.URL))
	}
	if strings.TrimSpace(profile.TopFunction) != "" {
		lines = append(lines, "  Top function: "+strings.TrimSpace(profile.TopFunction))
	}
	if profile.SampleCount > 0 {
		lines = append(lines, fmt.Sprintf("  Samples: %d", profile.SampleCount))
	}
	if profile.DurationNS > 0 {
		lines = append(lines, fmt.Sprintf("  Profile duration: %.1fms", float64(profile.DurationNS)/1_000_000))
	}
	return body + "\n" + strings.Join(lines, "\n")
}

func appendSlackProfileSummary(text string, profile *alert.ProfileContext) string {
	if profile == nil {
		return text
	}
	parts := []string{text, "profile " + firstNonEmpty(profile.ProfileID, "linked")}
	if strings.TrimSpace(profile.TopFunction) != "" {
		parts = append(parts, "top "+strings.TrimSpace(profile.TopFunction))
	}
	return strings.Join(parts, " · ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
