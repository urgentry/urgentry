package sqlite

import (
	"context"
	"database/sql"
	"time"

	"urgentry/internal/alert"
)

// AlertHistory represents a record of an alert rule firing.
type AlertHistory struct {
	ID      string
	RuleID  string
	GroupID string
	EventID string
	FiredAt time.Time
}

// AlertHistoryStore persists alert trigger history to SQLite.
type AlertHistoryStore struct {
	db *sql.DB
}

// NewAlertHistoryStore creates a new AlertHistoryStore.
func NewAlertHistoryStore(db *sql.DB) *AlertHistoryStore {
	return &AlertHistoryStore{db: db}
}

// Record saves a trigger event to the alert_history table.
func (s *AlertHistoryStore) Record(ctx context.Context, trigger alert.TriggerEvent) error {
	id := generateID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_history (id, rule_id, group_id, event_id, fired_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, trigger.RuleID, trigger.GroupID, trigger.EventID,
		trigger.Timestamp.UTC().Format(time.RFC3339),
	)
	return err
}

// ListRecent returns the most recent alert firings.
func (s *AlertHistoryStore) ListRecent(ctx context.Context, limit int) ([]AlertHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.id, h.rule_id, h.group_id, h.event_id, h.fired_at
		 FROM alert_history h
		 ORDER BY h.fired_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []AlertHistory
	for rows.Next() {
		var h AlertHistory
		var groupID, eventID, firedAt sql.NullString
		if err := rows.Scan(&h.ID, &h.RuleID, &groupID, &eventID, &firedAt); err != nil {
			return nil, err
		}
		h.GroupID = nullStr(groupID)
		h.EventID = nullStr(eventID)
		h.FiredAt = parseTime(nullStr(firedAt))
		history = append(history, h)
	}
	return history, rows.Err()
}
