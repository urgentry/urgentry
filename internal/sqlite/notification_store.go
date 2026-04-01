package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/notify"
	"urgentry/pkg/id"
)

// NotificationOutboxStore persists Tiny-mode outbound email notifications.
type NotificationOutboxStore struct {
	db *sql.DB
}

// NewNotificationOutboxStore creates a store backed by SQLite.
func NewNotificationOutboxStore(db *sql.DB) *NotificationOutboxStore {
	return &NotificationOutboxStore{db: db}
}

// RecordEmail stores an outbound email notification.
func (s *NotificationOutboxStore) RecordEmail(ctx context.Context, notification *notify.EmailNotification) error {
	if notification == nil {
		return nil
	}
	if notification.ID == "" {
		notification.ID = id.New()
	}
	if notification.Transport == "" {
		notification.Transport = "tiny-outbox"
	}
	if notification.Status == "" {
		notification.Status = "queued"
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = time.Now().UTC()
	}

	var sentAt any
	if notification.SentAt != nil {
		sentAt = notification.SentAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_outbox
			(id, project_id, rule_id, group_id, event_id, recipient, subject, body, transport, status, error, created_at, sent_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		notification.ID,
		notification.ProjectID,
		notification.RuleID,
		notification.GroupID,
		notification.EventID,
		notification.Recipient,
		notification.Subject,
		notification.Body,
		notification.Transport,
		notification.Status,
		notification.Error,
		notification.CreatedAt.UTC().Format(time.RFC3339),
		sentAt,
	)
	return err
}

// ListRecent returns the most recent outbound emails, newest first.
func (s *NotificationOutboxStore) ListRecent(ctx context.Context, limit int) ([]notify.EmailNotification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, rule_id, group_id, event_id, recipient, subject, body, transport, status, error, created_at, sent_at
		 FROM notification_outbox
		 ORDER BY created_at DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []notify.EmailNotification
	for rows.Next() {
		var item notify.EmailNotification
		var projectID, ruleID, groupID, eventID, recipient, subject, body, transport, status, errMsg, createdAt, sentAt sql.NullString
		if err := rows.Scan(
			&item.ID,
			&projectID,
			&ruleID,
			&groupID,
			&eventID,
			&recipient,
			&subject,
			&body,
			&transport,
			&status,
			&errMsg,
			&createdAt,
			&sentAt,
		); err != nil {
			return nil, err
		}
		item.ProjectID = nullStr(projectID)
		item.RuleID = nullStr(ruleID)
		item.GroupID = nullStr(groupID)
		item.EventID = nullStr(eventID)
		item.Recipient = nullStr(recipient)
		item.Subject = nullStr(subject)
		item.Body = nullStr(body)
		item.Transport = nullStr(transport)
		item.Status = nullStr(status)
		item.Error = nullStr(errMsg)
		item.CreatedAt = parseTime(nullStr(createdAt))
		if s := nullStr(sentAt); s != "" {
			t := parseTime(s)
			item.SentAt = &t
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
