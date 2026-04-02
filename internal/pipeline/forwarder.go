package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"urgentry/internal/outboundhttp"

	"urgentry/internal/store"
)

// ForwardEvent is the envelope pushed to external systems.
type ForwardEvent struct {
	EventID   string            `json:"eventId"`
	EventType string            `json:"eventType"`
	ProjectID string            `json:"projectId"`
	GroupID   string            `json:"groupId,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// DataForwarder sends processed events to an external system.
type DataForwarder interface {
	ForwardEvent(ctx context.Context, event ForwardEvent) error
}

// WebhookForwarder POSTs event JSON to a configured URL.
type WebhookForwarder struct {
	URL        string
	HTTPClient *http.Client
}

// NewWebhookForwarder creates a forwarder that POSTs to the given URL.
func NewWebhookForwarder(url string) *WebhookForwarder {
	return &WebhookForwarder{
		URL:        url,
		HTTPClient: outboundhttp.NewClient(10*time.Second, nil),
	}
}

// ForwardEvent sends the event as a JSON POST to the configured webhook URL.
func (w *WebhookForwarder) ForwardEvent(ctx context.Context, event ForwardEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("forwarder: marshal event: %w", err)
	}
	if _, err := outboundhttp.ValidateTargetURL(w.URL); err != nil {
		return fmt.Errorf("forwarder: invalid target: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("forwarder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "urgentry-data-forwarder/1.0")

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("forwarder: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("forwarder: remote returned %d", resp.StatusCode)
	}
	return nil
}

// ForwardingDispatcher runs forwarding asynchronously after pipeline
// processing. It looks up active forwarding configs for the project and
// dispatches to the appropriate forwarder type.
type ForwardingDispatcher struct {
	Store store.ForwardingStore
}

// NewForwardingDispatcher creates a dispatcher backed by the given store.
func NewForwardingDispatcher(s store.ForwardingStore) *ForwardingDispatcher {
	return &ForwardingDispatcher{Store: s}
}

// Dispatch sends the event to all active forwarders for the project.
// Errors are logged but do not propagate — forwarding must not block
// the main pipeline.
func (d *ForwardingDispatcher) Dispatch(ctx context.Context, event ForwardEvent) {
	if d.Store == nil {
		return
	}

	configs, err := d.Store.ListForwardingByProject(ctx, event.ProjectID)
	if err != nil {
		log.Error().Err(err).Str("project_id", event.ProjectID).Msg("forwarder: list configs failed")
		return
	}

	for _, cfg := range configs {
		if cfg.Status != "active" {
			continue
		}
		go func(c *store.ForwardingConfig) {
			fwd := forwarderForConfig(c)
			if fwd == nil {
				log.Warn().Str("type", c.Type).Str("id", c.ID).Msg("forwarder: unknown forwarder type")
				return
			}
			if fwdErr := fwd.ForwardEvent(ctx, event); fwdErr != nil {
				log.Error().Err(fwdErr).
					Str("forwarder_id", c.ID).
					Str("url", c.URL).
					Str("project_id", event.ProjectID).
					Msg("forwarder: delivery failed")
			}
		}(cfg)
	}
}

// forwarderForConfig returns the appropriate DataForwarder for the config type.
func forwarderForConfig(cfg *store.ForwardingConfig) DataForwarder {
	switch cfg.Type {
	case "webhook":
		return NewWebhookForwarder(cfg.URL)
	default:
		return nil
	}
}
