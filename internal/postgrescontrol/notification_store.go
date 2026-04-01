package postgrescontrol

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/notify"
	"urgentry/pkg/id"
)

// AlertHistory records alert trigger firings.
type AlertHistory struct {
	ID      string
	RuleID  string
	GroupID string
	EventID string
	FiredAt time.Time
}

// AlertHistoryStore persists alert trigger history in PostgreSQL.
type AlertHistoryStore struct {
	db *sql.DB
}

// NewAlertHistoryStore creates a history store backed by PostgreSQL.
func NewAlertHistoryStore(db *sql.DB) *AlertHistoryStore {
	return &AlertHistoryStore{db: db}
}

// Record stores one alert trigger event.
func (s *AlertHistoryStore) Record(ctx context.Context, trigger alert.TriggerEvent) error {
	firedAt := trigger.Timestamp
	if firedAt.IsZero() {
		firedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_history (id, rule_id, group_id, event_id, fired_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		id.New(), trigger.RuleID, strings.TrimSpace(trigger.GroupID), strings.TrimSpace(trigger.EventID), firedAt.UTC(),
	)
	return err
}

// ListRecent lists recent alert triggers.
func (s *AlertHistoryStore) ListRecent(ctx context.Context, limit int) ([]AlertHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rule_id, group_id, event_id, fired_at
		   FROM alert_history
		  ORDER BY fired_at DESC
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AlertHistory, 0, limit)
	for rows.Next() {
		var item AlertHistory
		var groupID, eventID sql.NullString
		var firedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.RuleID, &groupID, &eventID, &firedAt); err != nil {
			return nil, err
		}
		item.GroupID = nullString(groupID)
		item.EventID = nullString(eventID)
		item.FiredAt = nullTime(firedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// NotificationOutboxStore persists queued outbound emails.
type NotificationOutboxStore struct {
	db *sql.DB
}

// NewNotificationOutboxStore creates an outbox store backed by PostgreSQL.
func NewNotificationOutboxStore(db *sql.DB) *NotificationOutboxStore {
	return &NotificationOutboxStore{db: db}
}

// RecordEmail stores one outbound email notification.
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
		notification.Status = notify.DeliveryStatusQueued
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_outbox
			(id, project_id, rule_id, group_id, event_id, recipient, subject, body, transport, status, error, created_at, sent_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		notification.ID,
		notification.ProjectID,
		notification.RuleID,
		emptyString(notification.GroupID),
		emptyString(notification.EventID),
		notification.Recipient,
		notification.Subject,
		notification.Body,
		notification.Transport,
		notification.Status,
		notification.Error,
		notification.CreatedAt.UTC(),
		nullableTimePtr(notification.SentAt),
	)
	return err
}

// ListRecent lists recent outbox rows.
func (s *NotificationOutboxStore) ListRecent(ctx context.Context, limit int) ([]notify.EmailNotification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, rule_id, group_id, event_id, recipient, subject, body, transport, status, error, created_at, sent_at
		   FROM notification_outbox
		  ORDER BY created_at DESC
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]notify.EmailNotification, 0, limit)
	for rows.Next() {
		var item notify.EmailNotification
		var projectID, ruleID, groupID, eventID, recipient, subject, body, transport, status, errorText sql.NullString
		var createdAt, sentAt sql.NullTime
		if err := rows.Scan(&item.ID, &projectID, &ruleID, &groupID, &eventID, &recipient, &subject, &body, &transport, &status, &errorText, &createdAt, &sentAt); err != nil {
			return nil, err
		}
		item.ProjectID = nullString(projectID)
		item.RuleID = nullString(ruleID)
		item.GroupID = nullString(groupID)
		item.EventID = nullString(eventID)
		item.Recipient = nullString(recipient)
		item.Subject = nullString(subject)
		item.Body = nullString(body)
		item.Transport = nullString(transport)
		item.Status = nullString(status)
		item.Error = nullString(errorText)
		item.CreatedAt = nullTime(createdAt)
		item.SentAt = optionalNullTime(sentAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// NotificationDeliveryStore persists delivery attempts/results.
type NotificationDeliveryStore struct {
	db *sql.DB
}

// NewNotificationDeliveryStore creates a PostgreSQL-backed delivery store.
func NewNotificationDeliveryStore(db *sql.DB) *NotificationDeliveryStore {
	return &NotificationDeliveryStore{db: db}
}

// RecordDelivery stores one notification delivery attempt/result.
func (s *NotificationDeliveryStore) RecordDelivery(ctx context.Context, delivery *notify.DeliveryRecord) error {
	if delivery == nil {
		return nil
	}
	if delivery.ID == "" {
		delivery.ID = id.New()
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_deliveries
			(id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, created_at, last_attempt_at, delivered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		delivery.ID,
		delivery.ProjectID,
		emptyString(delivery.RuleID),
		emptyString(delivery.GroupID),
		emptyString(delivery.EventID),
		delivery.Kind,
		delivery.Target,
		delivery.Status,
		delivery.Attempts,
		nullableIntPtr(delivery.ResponseStatus),
		delivery.Error,
		delivery.CreatedAt.UTC(),
		nullableTimePtr(delivery.LastAttemptAt),
		nullableTimePtr(delivery.DeliveredAt),
	)
	return err
}

// ListRecent lists recent delivery attempts for a project.
func (s *NotificationDeliveryStore) ListRecent(ctx context.Context, projectID string, limit int) ([]notify.DeliveryRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, created_at, last_attempt_at, delivered_at
		   FROM notification_deliveries
		  WHERE project_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]notify.DeliveryRecord, 0, limit)
	for rows.Next() {
		var item notify.DeliveryRecord
		var ruleID, groupID, eventID, errorText sql.NullString
		var responseStatus sql.NullInt64
		var createdAt, lastAttemptAt, deliveredAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.ProjectID, &ruleID, &groupID, &eventID, &item.Kind, &item.Target, &item.Status, &item.Attempts, &responseStatus, &errorText, &createdAt, &lastAttemptAt, &deliveredAt); err != nil {
			return nil, err
		}
		item.RuleID = nullString(ruleID)
		item.GroupID = nullString(groupID)
		item.EventID = nullString(eventID)
		item.Error = nullString(errorText)
		item.CreatedAt = nullTime(createdAt)
		if responseStatus.Valid {
			value := int(responseStatus.Int64)
			item.ResponseStatus = &value
		}
		item.LastAttemptAt = optionalNullTime(lastAttemptAt)
		item.DeliveredAt = optionalNullTime(deliveredAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func emptyString(value string) string {
	return strings.TrimSpace(value)
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableIntPtr(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
