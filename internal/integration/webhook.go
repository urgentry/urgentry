package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"urgentry/internal/outboundhttp"
)

// WebhookIntegration is the built-in generic outgoing webhook connector.
// When an event or alert fires it POSTs a JSON payload to the configured URL.
type WebhookIntegration struct{}

var _ Integration = (*WebhookIntegration)(nil)

func (w *WebhookIntegration) ID() string   { return "webhook" }
func (w *WebhookIntegration) Name() string { return "Webhook" }
func (w *WebhookIntegration) Description() string {
	return "Send JSON payloads to an external URL on events and alerts."
}

func (w *WebhookIntegration) ConfigSchema() []ConfigField {
	return []ConfigField{
		{Key: "url", Label: "Webhook URL", Type: "url", Required: true, Placeholder: "https://example.com/hook"},
		{Key: "secret", Label: "Signing Secret", Type: "secret", Required: false, HelpText: "Optional HMAC secret for payload verification."},
	}
}

func (w *WebhookIntegration) OnEvent(ctx context.Context, config map[string]string, event EventPayload) error {
	url := config["url"]
	if url == "" {
		return fmt.Errorf("webhook: missing url in config")
	}
	return postJSON(ctx, url, map[string]any{
		"type":  "event",
		"event": event,
	})
}

func (w *WebhookIntegration) OnAlert(ctx context.Context, config map[string]string, alert AlertPayload) error {
	url := config["url"]
	if url == "" {
		return fmt.Errorf("webhook: missing url in config")
	}
	return postJSON(ctx, url, map[string]any{
		"type":  "alert",
		"alert": alert,
	})
}

func (w *WebhookIntegration) OnWebhook(_ context.Context, _ map[string]string, _ []byte) ([]byte, error) {
	// Generic webhook is outbound-only; inbound is a no-op.
	return []byte(`{"ok":true}`), nil
}

func postJSON(ctx context.Context, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}
	if _, err := outboundhttp.ValidateTargetURL(url); err != nil {
		return fmt.Errorf("webhook: invalid target: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := outboundhttp.NewClient(10*time.Second, nil)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook: remote returned %d", resp.StatusCode)
	}
	return nil
}
