package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"urgentry/internal/integration"
)

var _ integration.AppStore = (*SentryAppStore)(nil)

// SentryAppStore persists Sentry App overrides and tombstones in SQLite.
type SentryAppStore struct {
	db *sql.DB
}

// NewSentryAppStore creates a SQLite-backed Sentry App store.
func NewSentryAppStore(db *sql.DB) *SentryAppStore {
	return &SentryAppStore{db: db}
}

func (s *SentryAppStore) Get(ctx context.Context, idOrSlug string) (*integration.AppRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, author, overview, scopes_json, events_json, schema_json, allowed_origins_json,
		       status, redirect_url, webhook_url, is_alertable, verify_install, deleted_at, created_at, updated_at
		  FROM sentry_apps
		 WHERE id = ? OR slug = ?
		 LIMIT 1`,
		strings.TrimSpace(idOrSlug), strings.TrimSpace(idOrSlug),
	)
	return scanSentryAppRecord(row)
}

func (s *SentryAppStore) List(ctx context.Context) ([]*integration.AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, name, author, overview, scopes_json, events_json, schema_json, allowed_origins_json,
		       status, redirect_url, webhook_url, is_alertable, verify_install, deleted_at, created_at, updated_at
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
	id := firstNonEmptyText(app.ID, app.Slug)
	slug := firstNonEmptyText(app.Slug, app.ID)
	if id == "" || slug == "" {
		return nil
	}
	now := time.Now().UTC()
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	app.ID = id
	app.Slug = slug
	app.UpdatedAt = now
	if app.Status == "" {
		app.Status = "published"
	}
	if !json.Valid(defaultJSON(app.Schema, []byte("null"))) {
		return json.Unmarshal(app.Schema, new(any))
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
	schemaJSON := defaultJSON(app.Schema, []byte("null"))
	if len(schemaJSON) > 0 && string(schemaJSON) != "null" {
		schemaJSON, err = canonicalizeJSONObject(schemaJSON)
		if err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sentry_apps
			(id, slug, name, author, overview, scopes_json, events_json, schema_json, allowed_origins_json,
			 status, redirect_url, webhook_url, is_alertable, verify_install, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
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
		strings.TrimSpace(app.Author),
		strings.TrimSpace(app.Overview),
		string(scopesJSON),
		string(eventsJSON),
		string(schemaJSON),
		string(allowedOriginsJSON),
		strings.TrimSpace(app.Status),
		strings.TrimSpace(app.RedirectURL),
		strings.TrimSpace(app.WebhookURL),
		boolToInt(app.IsAlertable),
		boolToInt(app.VerifyInstall),
		formatOptionalTime(ptrTimeValue(app.DeletedAt)),
		app.CreatedAt.UTC().Format(time.RFC3339),
		app.UpdatedAt.UTC().Format(time.RFC3339),
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
			 status, redirect_url, webhook_url, is_alertable, verify_install, deleted_at, created_at, updated_at)
		VALUES (?, ?, '', '', '', '[]', '[]', 'null', '[]', 'deleted', '', '', 0, 1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			deleted_at = excluded.deleted_at,
			status = 'deleted',
			updated_at = excluded.updated_at`,
		idOrSlug,
		idOrSlug,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
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
		deletedAt          sql.NullString
		createdAt          string
		updatedAt          string
		isAlertable        int
		verifyInstall      int
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
		&isAlertable,
		&verifyInstall,
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
	item.IsAlertable = isAlertable != 0
	item.VerifyInstall = verifyInstall != 0
	item.Scopes = decodeStringSlice(scopesJSON)
	item.Events = decodeStringSlice(eventsJSON)
	item.AllowedOrigins = decodeStringSlice(allowedOriginsJSON)
	item.Schema = json.RawMessage(defaultJSON([]byte(schemaJSON), []byte("null")))
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	item.DeletedAt = parseOptionalTime(deletedAt)
	return &item, nil
}

func scanSentryAppRecordRow(rows *sql.Rows) (*integration.AppRecord, error) {
	var (
		item               integration.AppRecord
		scopesJSON         string
		eventsJSON         string
		schemaJSON         string
		allowedOriginsJSON string
		deletedAt          sql.NullString
		createdAt          string
		updatedAt          string
		isAlertable        int
		verifyInstall      int
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
		&isAlertable,
		&verifyInstall,
		&deletedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.IsAlertable = isAlertable != 0
	item.VerifyInstall = verifyInstall != 0
	item.Scopes = decodeStringSlice(scopesJSON)
	item.Events = decodeStringSlice(eventsJSON)
	item.AllowedOrigins = decodeStringSlice(allowedOriginsJSON)
	item.Schema = json.RawMessage(defaultJSON([]byte(schemaJSON), []byte("null")))
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	item.DeletedAt = parseOptionalTime(deletedAt)
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

func ptrTimeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
