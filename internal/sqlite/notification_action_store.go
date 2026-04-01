package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// NotificationAction represents a configured notification action
// (email, Slack, PagerDuty, webhook) for an organization.
type NotificationAction struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organizationId"`
	ServiceType      string    `json:"serviceType"`
	TargetIdentifier string    `json:"targetIdentifier"`
	TargetDisplay    string    `json:"targetDisplay"`
	TriggerType      string    `json:"triggerType"`
	CreatedAt        time.Time `json:"dateCreated"`
}

// NotificationActionStore provides CRUD for notification actions.
type NotificationActionStore struct {
	db *sql.DB
}

// NewNotificationActionStore creates a SQLite-backed notification action store.
func NewNotificationActionStore(db *sql.DB) *NotificationActionStore {
	return &NotificationActionStore{db: db}
}

// List returns all notification actions for an organization.
func (s *NotificationActionStore) List(ctx context.Context, orgID string) ([]NotificationAction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, organization_id, service_type, target_identifier, target_display, trigger_type, created_at
		 FROM notification_actions
		 WHERE organization_id = ?
		 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []NotificationAction
	for rows.Next() {
		item, err := scanNotificationAction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get returns a single notification action by ID within an organization.
func (s *NotificationActionStore) Get(ctx context.Context, orgID, actionID string) (*NotificationAction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, organization_id, service_type, target_identifier, target_display, trigger_type, created_at
		 FROM notification_actions
		 WHERE organization_id = ? AND id = ?`,
		orgID, actionID,
	)
	item, err := scanNotificationAction(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// Create inserts a new notification action.
func (s *NotificationActionStore) Create(ctx context.Context, orgID, serviceType, targetIdentifier, targetDisplay, triggerType string) (*NotificationAction, error) {
	id := generateID()
	now := time.Now().UTC()
	if triggerType == "" {
		triggerType = "spike-protection"
	}
	if serviceType == "" {
		serviceType = "email"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_actions (id, organization_id, service_type, target_identifier, target_display, trigger_type, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, orgID, serviceType, targetIdentifier, targetDisplay, triggerType, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	return &NotificationAction{
		ID:               id,
		OrganizationID:   orgID,
		ServiceType:      serviceType,
		TargetIdentifier: targetIdentifier,
		TargetDisplay:    targetDisplay,
		TriggerType:      triggerType,
		CreatedAt:        now,
	}, nil
}

// Update modifies an existing notification action.
func (s *NotificationActionStore) Update(ctx context.Context, orgID, actionID, serviceType, targetIdentifier, targetDisplay, triggerType string) (*NotificationAction, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE notification_actions
		 SET service_type = ?, target_identifier = ?, target_display = ?, trigger_type = ?
		 WHERE organization_id = ? AND id = ?`,
		serviceType, targetIdentifier, targetDisplay, triggerType, orgID, actionID,
	)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, nil
	}
	return s.Get(ctx, orgID, actionID)
}

// Delete removes a notification action.
func (s *NotificationActionStore) Delete(ctx context.Context, orgID, actionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_actions WHERE organization_id = ? AND id = ?`,
		orgID, actionID,
	)
	return err
}

type notificationActionScanner interface {
	Scan(dest ...any) error
}

func scanNotificationAction(scanner notificationActionScanner) (NotificationAction, error) {
	var item NotificationAction
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ServiceType,
		&item.TargetIdentifier,
		&item.TargetDisplay,
		&item.TriggerType,
		&createdAt,
	); err != nil {
		return NotificationAction{}, err
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}
