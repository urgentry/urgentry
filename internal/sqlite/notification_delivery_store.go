package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/notify"
	"urgentry/pkg/id"
)

// NotificationDeliveryStore persists webhook/email delivery attempts.
type NotificationDeliveryStore struct {
	db *sql.DB
}

// NewNotificationDeliveryStore creates a SQLite-backed delivery store.
func NewNotificationDeliveryStore(db *sql.DB) *NotificationDeliveryStore {
	return &NotificationDeliveryStore{db: db}
}

// RecordDelivery stores one delivery attempt/result row.
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

	var lastAttemptAt any
	if delivery.LastAttemptAt != nil && !delivery.LastAttemptAt.IsZero() {
		lastAttemptAt = delivery.LastAttemptAt.UTC().Format(time.RFC3339)
	}
	var deliveredAt any
	if delivery.DeliveredAt != nil && !delivery.DeliveredAt.IsZero() {
		deliveredAt = delivery.DeliveredAt.UTC().Format(time.RFC3339)
	}
	var responseStatus any
	if delivery.ResponseStatus != nil {
		responseStatus = *delivery.ResponseStatus
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_deliveries
			(id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, payload_json, created_at, last_attempt_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.ID,
		delivery.ProjectID,
		nullIfEmpty(delivery.RuleID),
		nullIfEmpty(delivery.GroupID),
		nullIfEmpty(delivery.EventID),
		delivery.Kind,
		delivery.Target,
		delivery.Status,
		delivery.Attempts,
		responseStatus,
		nullIfEmpty(delivery.Error),
		nullIfEmpty(delivery.PayloadJSON),
		delivery.CreatedAt.UTC().Format(time.RFC3339),
		lastAttemptAt,
		deliveredAt,
	)
	return err
}

// ListRecent lists recent deliveries for a project, newest first.
func (s *NotificationDeliveryStore) ListRecent(ctx context.Context, projectID string, limit int) ([]notify.DeliveryRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, rule_id, group_id, event_id, kind, target, status, attempts, response_status, error, payload_json, created_at, last_attempt_at, delivered_at
		 FROM notification_deliveries
		 WHERE project_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []notify.DeliveryRecord
	for rows.Next() {
		var item notify.DeliveryRecord
		var ruleID, groupID, eventID, errorText, payloadJSON, createdAt, lastAttemptAt, deliveredAt sql.NullString
		var responseStatus sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&ruleID,
			&groupID,
			&eventID,
			&item.Kind,
			&item.Target,
			&item.Status,
			&item.Attempts,
			&responseStatus,
			&errorText,
			&payloadJSON,
			&createdAt,
			&lastAttemptAt,
			&deliveredAt,
		); err != nil {
			return nil, err
		}
		item.RuleID = nullStr(ruleID)
		item.GroupID = nullStr(groupID)
		item.EventID = nullStr(eventID)
		item.Error = nullStr(errorText)
		item.PayloadJSON = nullStr(payloadJSON)
		item.CreatedAt = parseTime(nullStr(createdAt))
		if responseStatus.Valid {
			value := int(responseStatus.Int64)
			item.ResponseStatus = &value
		}
		item.LastAttemptAt = parseOptionalTime(lastAttemptAt)
		item.DeliveredAt = parseOptionalTime(deliveredAt)
		deliveries = append(deliveries, item)
	}
	return deliveries, rows.Err()
}
