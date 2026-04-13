package auth

import (
	"context"
	"time"
)

// PersonalAccessTokenRecord is redacted PAT metadata safe to return from APIs.
type PersonalAccessTokenRecord struct {
	ID          string
	Label       string
	TokenPrefix string
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// AutomationTokenRecord is redacted automation token metadata safe to return from APIs.
type AutomationTokenRecord struct {
	ID              string
	ProjectID       string
	Label           string
	TokenPrefix     string
	Scopes          []string
	CreatedByUserID string
	CreatedAt       time.Time
	LastUsedAt      *time.Time
	ExpiresAt       *time.Time
	RevokedAt       *time.Time
}

// TokenManager manages PATs and project automation tokens.
type TokenManager interface {
	CreatePersonalAccessToken(ctx context.Context, userID, label string, scopes []string, expiresAt *time.Time, raw string) (string, error)
	ListPersonalAccessTokens(ctx context.Context, userID string) ([]PersonalAccessTokenRecord, error)
	RevokePersonalAccessToken(ctx context.Context, tokenID, userID string) error
	CreateAutomationToken(ctx context.Context, projectID, label, createdByUserID string, scopes []string, expiresAt *time.Time, raw string) (string, error)
	ListAutomationTokens(ctx context.Context, projectID string) ([]AutomationTokenRecord, error)
	RevokeAutomationToken(ctx context.Context, tokenID, projectID string) error
}
