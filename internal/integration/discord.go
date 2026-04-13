package integration

import (
	"context"
	"fmt"
)

// DiscordIntegration sends notifications to Discord via webhook URLs.
// It formats payloads as Discord embeds, which are structurally similar
// to Slack attachments.
type DiscordIntegration struct{}

var _ Integration = (*DiscordIntegration)(nil)

func (d *DiscordIntegration) ID() string          { return "discord" }
func (d *DiscordIntegration) Name() string         { return "Discord" }
func (d *DiscordIntegration) Description() string  { return "Send alert and event notifications to a Discord channel via webhook." }

func (d *DiscordIntegration) ConfigSchema() []ConfigField {
	return []ConfigField{
		{Key: "webhook_url", Label: "Webhook URL", Type: "url", Required: true, Placeholder: "https://discord.com/api/webhooks/..."},
	}
}

func (d *DiscordIntegration) OnEvent(ctx context.Context, config map[string]string, event EventPayload) error {
	url := config["webhook_url"]
	if url == "" {
		return fmt.Errorf("discord: missing webhook_url in config")
	}
	embed := discordEmbed{
		Title:       fmt.Sprintf("[%s] %s", event.ProjectSlug, event.IssueTitle),
		Description: fmt.Sprintf("Action: %s\nIssue: %s\nEvent: %s", event.Action, event.IssueID, event.EventID),
		Color:       embedColorForAction(event.Action),
	}
	return postJSON(ctx, url, discordWebhookPayload{
		Username: "Urgentry",
		Embeds:   []discordEmbed{embed},
	})
}

func (d *DiscordIntegration) OnAlert(ctx context.Context, config map[string]string, alert AlertPayload) error {
	url := config["webhook_url"]
	if url == "" {
		return fmt.Errorf("discord: missing webhook_url in config")
	}
	embed := discordEmbed{
		Title:       fmt.Sprintf("[%s] Alert: %s", alert.ProjectSlug, alert.RuleName),
		Description: alert.Message,
		Color:       0xE74C3C, // red
	}
	return postJSON(ctx, url, discordWebhookPayload{
		Username: "Urgentry",
		Embeds:   []discordEmbed{embed},
	})
}

func (d *DiscordIntegration) OnWebhook(_ context.Context, _ map[string]string, _ []byte) ([]byte, error) {
	// Discord integration is outbound-only; inbound is a no-op.
	return []byte(`{"ok":true}`), nil
}

// ---------------------------------------------------------------------------
// Discord embed wire types
// ---------------------------------------------------------------------------

type discordWebhookPayload struct {
	Username string         `json:"username,omitempty"`
	Embeds   []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color,omitempty"`
}

func embedColorForAction(action string) int {
	switch action {
	case "created":
		return 0xE74C3C // red
	case "resolved":
		return 0x2ECC71 // green
	case "assigned":
		return 0x3498DB // blue
	default:
		return 0x95A5A6 // gray
	}
}

