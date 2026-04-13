package postgrescontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"urgentry/internal/integration"
)

var _ integration.AppStore = (*SentryAppStore)(nil)

// SentryAppStore persists Sentry App overrides and tombstones in PostgreSQL.
type SentryAppStore struct {
	db *sql.DB
}

// NewSentryAppStore creates a Postgres-backed Sentry App store.
func NewSentryAppStore(db *sql.DB) *SentryAppStore {
	return &SentryAppStore{db: db}
}

func (s *SentryAppStore) Get(ctx context.Context, idOrSlug string) (*integration.AppRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, author, overview, scopes_json::text, events_json::text, schema_json::text, allowed_origins_json::text,
		       status, COALESCE(redirect_url, ''), COALESCE(webhook_url, ''), is_alertable, verify_install, deleted_at, created_at, updated_at
		  FROM sentry_apps
		 WHERE id = $1 OR slug = $1
		 LIMIT 1`,
		strings.TrimSpace(idOrSlug),
	)
	return scanSentryAppRecord(row)
}

func (s *SentryAppStore) List(ctx context.Context) ([]*integration.AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, name, author, overview, scopes_json::text, events_json::text, schema_json::text, allowed_origins_json::text,
		       status, COALESCE(redirect_url, ''), COALESCE(webhook_url, ''), is_alertable, verify_install, deleted_at, created_at, updated_at
		  FROM sentry_apps
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*integration.AppRecord
	for rows.Next() {
		item, err := scanSentryAppRecordRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SentryAppStore) Upsert(ctx context.Context, app *integration.AppRecord) error {
	if app == nil {
		return nil
	}
	now := time.Now().UTC()
	app.ID = firstNonEmpty(app.ID, app.Slug)
	app.Slug = firstNonEmpty(app.Slug, app.ID)
	if app.ID == "" || app.Slug == "" {
		return nil
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	app.UpdatedAt = now
	if app.Status == "" {
		app.Status = "published"
	}
	schemaJSON := defaultJSON(app.Schema, []byte("null"))
	if len(schemaJSON) > 0 && string(schemaJSON) != "null" {
		var payload any
		if err := json.Unmarshal(schemaJSON, &payload); err != nil {
			return err
		}
		schemaJSON, _ = json.Marshal(payload)
	}
	scopesJSON, err := marshalJSON(app.Scopes)
	if err != nil {
		return err
	}
	eventsJSON, err := marshalJSON(app.Events)
	if err != nil {
		return err
	}
	allowedOriginsJSON, err := marshalJSON(app.AllowedOrigins)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sentry_apps
			(id, slug, name, author, overview, scopes_json, events_json, schema_json, allowed_origins_json,
			 status, redirect_url, webhook_url, is_alertable, verify_install, deleted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (id) DO UPDATE SET
			slug = excluded.slug,
			name = excluded.name,
			author = excluded.author,
			overview = excluded.overview,
			scopes_json = excluded.scopes_json,
			events_json = excluded.events_json,
			schema_json = excluded.schema_json,
			allowed_origins_json = excluded.allowed_origins_json,
			status = excluded.status,
			redirect_url = excluded.redirect_url,
			webhook_url = excluded.webhook_url,
			is_alertable = excluded.is_alertable,
			verify_install = excluded.verify_install,
			deleted_at = excluded.deleted_at,
			updated_at = excluded.updated_at`,
		app.ID,
		app.Slug,
		strings.TrimSpace(app.Name),
		nullIfEmpty(app.Author),
		nullIfEmpty(app.Overview),
		string(scopesJSON),
		string(eventsJSON),
		string(schemaJSON),
		string(allowedOriginsJSON),
		app.Status,
		nullIfEmpty(app.RedirectURL),
		nullIfEmpty(app.WebhookURL),
		app.IsAlertable,
		app.VerifyInstall,
		optionalTime(app.DeletedAt),
		app.CreatedAt.UTC(),
		app.UpdatedAt.UTC(),
	)
	return err
}

func (s *SentryAppStore) Delete(ctx context.Context, idOrSlug string) error {
	idOrSlug = strings.TrimSpace(idOrSlug)
	if idOrSlug == "" {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sentry_apps
			(id, slug, name, author, overview, scopes_json, events_json, schema_json, allowed_origins_json,
			 status, is_alertable, verify_install, deleted_at, created_at, updated_at)
		VALUES ($1, $2, '', NULL, NULL, '[]'::jsonb, '[]'::jsonb, 'null'::jsonb, '[]'::jsonb, 'deleted', FALSE, TRUE, $3, $3, $3)
		ON CONFLICT (id) DO UPDATE SET
			status = 'deleted',
			deleted_at = excluded.deleted_at,
			updated_at = excluded.updated_at`,
		idOrSlug,
		idOrSlug,
		now,
	)
	return err
}

func scanSentryAppRecord(row *sql.Row) (*integration.AppRecord, error) {
	var (
		item               integration.AppRecord
		scopesJSON         string
		eventsJSON         string
		schemaJSON         string
		allowedOriginsJSON string
		deletedAt          sql.NullTime
		createdAt          sql.NullTime
		updatedAt          sql.NullTime
	)
	err := row.Scan(
		&item.ID,
		&item.Slug,
		&item.Name,
		&item.Author,
		&item.Overview,
		&scopesJSON,
		&eventsJSON,
		&schemaJSON,
		&allowedOriginsJSON,
		&item.Status,
		&item.RedirectURL,
		&item.WebhookURL,
		&item.IsAlertable,
		&item.VerifyInstall,
		&deletedAt,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Scopes = decodeStringSlice(scopesJSON)
	item.Events = decodeStringSlice(eventsJSON)
	item.AllowedOrigins = decodeStringSlice(allowedOriginsJSON)
	item.Schema = json.RawMessage(defaultJSON([]byte(schemaJSON), []byte("null")))
	item.DeletedAt = nullTimePtr(deletedAt)
	item.CreatedAt = nullTime(createdAt)
	item.UpdatedAt = nullTime(updatedAt)
	return &item, nil
}

func scanSentryAppRecordRow(rows *sql.Rows) (*integration.AppRecord, error) {
	var (
		item               integration.AppRecord
		scopesJSON         string
		eventsJSON         string
		schemaJSON         string
		allowedOriginsJSON string
		deletedAt          sql.NullTime
		createdAt          sql.NullTime
		updatedAt          sql.NullTime
	)
	if err := rows.Scan(
		&item.ID,
		&item.Slug,
		&item.Name,
		&item.Author,
		&item.Overview,
		&scopesJSON,
		&eventsJSON,
		&schemaJSON,
		&allowedOriginsJSON,
		&item.Status,
		&item.RedirectURL,
		&item.WebhookURL,
		&item.IsAlertable,
		&item.VerifyInstall,
		&deletedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.Scopes = decodeStringSlice(scopesJSON)
	item.Events = decodeStringSlice(eventsJSON)
	item.AllowedOrigins = decodeStringSlice(allowedOriginsJSON)
	item.Schema = json.RawMessage(defaultJSON([]byte(schemaJSON), []byte("null")))
	item.DeletedAt = nullTimePtr(deletedAt)
	item.CreatedAt = nullTime(createdAt)
	item.UpdatedAt = nullTime(updatedAt)
	return &item, nil
}

func decodeStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
}

func defaultJSON(raw, fallback []byte) []byte {
	if len(raw) == 0 {
		return fallback
	}
	return raw
}
