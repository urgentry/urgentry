package postgrescontrol

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/notify"
)

func TestNotificationStores_RecordAndList(t *testing.T) {
	db, fx := seedControlFixture(t)
	alerts := NewAlertStore(db)
	history := NewAlertHistoryStore(db)
	outbox := NewNotificationOutboxStore(db)
	deliveries := NewNotificationDeliveryStore(db)
	ctx := context.Background()

	rule := &alert.Rule{
		ProjectID: fx.ProjectID,
		Name:      "Notify",
		Status:    "active",
		RuleType:  "all",
		CreatedAt: time.Now().UTC(),
	}
	if err := alerts.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	trigger := alert.TriggerEvent{
		RuleID:    rule.ID,
		GroupID:   "grp-1",
		EventID:   "evt-1",
		Timestamp: time.Now().UTC(),
	}
	if err := history.Record(ctx, trigger); err != nil {
		t.Fatalf("AlertHistory.Record: %v", err)
	}

	sentAt := time.Now().UTC()
	if err := outbox.RecordEmail(ctx, &notify.EmailNotification{
		ProjectID: fx.ProjectID,
		RuleID:    rule.ID,
		GroupID:   "grp-1",
		EventID:   "evt-1",
		Recipient: "dev@example.com",
		Subject:   "Alert fired",
		Body:      "body",
		Status:    notify.DeliveryStatusQueued,
		SentAt:    &sentAt,
	}); err != nil {
		t.Fatalf("RecordEmail: %v", err)
	}

	statusCode := 202
	lastAttemptAt := time.Now().UTC()
	deliveredAt := time.Now().UTC()
	if err := deliveries.RecordDelivery(ctx, &notify.DeliveryRecord{
		ProjectID:      fx.ProjectID,
		RuleID:         rule.ID,
		GroupID:        "grp-1",
		EventID:        "evt-1",
		Kind:           notify.DeliveryKindWebhook,
		Target:         "https://hooks.example.com",
		Status:         notify.DeliveryStatusDelivered,
		Attempts:       2,
		ResponseStatus: &statusCode,
		LastAttemptAt:  &lastAttemptAt,
		DeliveredAt:    &deliveredAt,
	}); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}

	historyRows, err := history.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent history: %v", err)
	}
	if len(historyRows) != 1 || historyRows[0].RuleID != rule.ID {
		t.Fatalf("unexpected history rows: %+v", historyRows)
	}

	outboxRows, err := outbox.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent outbox: %v", err)
	}
	if len(outboxRows) != 1 || outboxRows[0].Recipient != "dev@example.com" {
		t.Fatalf("unexpected outbox rows: %+v", outboxRows)
	}

	deliveryRows, err := deliveries.ListRecent(ctx, fx.ProjectID, 10)
	if err != nil {
		t.Fatalf("ListRecent deliveries: %v", err)
	}
	if len(deliveryRows) != 1 || deliveryRows[0].Status != notify.DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery rows: %+v", deliveryRows)
	}
}
