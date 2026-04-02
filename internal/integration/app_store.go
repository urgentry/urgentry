package integration

import (
	"context"
	"encoding/json"
	"time"
)

// AppRecord stores mutable Sentry App metadata layered over built-in integrations.
type AppRecord struct {
	ID             string          `json:"id"`
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	Author         string          `json:"author,omitempty"`
	Overview       string          `json:"overview,omitempty"`
	Scopes         []string        `json:"scopes"`
	Events         []string        `json:"events,omitempty"`
	Schema         json.RawMessage `json:"schema,omitempty"`
	AllowedOrigins []string        `json:"allowedOrigins,omitempty"`
	Status         string          `json:"status"`
	RedirectURL    string          `json:"redirectUrl,omitempty"`
	WebhookURL     string          `json:"webhookUrl,omitempty"`
	IsAlertable    bool            `json:"isAlertable"`
	VerifyInstall  bool            `json:"verifyInstall"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// Deleted reports whether the record is a tombstone.
func (a *AppRecord) Deleted() bool {
	return a != nil && a.DeletedAt != nil && !a.DeletedAt.IsZero()
}

// AppStore persists Sentry App overrides and tombstones.
type AppStore interface {
	Get(ctx context.Context, idOrSlug string) (*AppRecord, error)
	List(ctx context.Context) ([]*AppRecord, error)
	Upsert(ctx context.Context, app *AppRecord) error
	Delete(ctx context.Context, idOrSlug string) error
}
