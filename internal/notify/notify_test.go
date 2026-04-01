package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/alert"
)

type memoryOutbox struct {
	records []*EmailNotification
}

func (m *memoryOutbox) RecordEmail(_ context.Context, notification *EmailNotification) error {
	m.records = append(m.records, notification)
	return nil
}

type memoryDeliveries struct {
	records []*DeliveryRecord
	err     error
}

func (m *memoryDeliveries) RecordDelivery(_ context.Context, delivery *DeliveryRecord) error {
	m.records = append(m.records, delivery)
	return m.err
}

func TestNotifyEmailRecordsOutbox(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{}
	n := NewNotifier(outbox, deliveries)

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:    "rule-1",
		GroupID:   "grp-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyEmail: %v", err)
	}
	if len(outbox.records) != 1 {
		t.Fatalf("expected 1 outbox record, got %d", len(outbox.records))
	}
	got := outbox.records[0]
	if got.ProjectID != "proj-1" || got.Recipient != "dev@example.com" {
		t.Fatalf("unexpected record: %+v", got)
	}
	if got.Status != DeliveryStatusQueued || got.Transport != "tiny-outbox" {
		t.Fatalf("unexpected status/transport: %+v", got)
	}
	if !strings.Contains(got.Body, "Rule: rule-1") {
		t.Fatalf("unexpected email body: %s", got.Body)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Kind != DeliveryKindEmail {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailRequiresOutbox(t *testing.T) {
	n := NewNotifier(nil, nil)
	if err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{}); err == nil {
		t.Fatal("expected error when no outbox is configured")
	}
}

func TestNotifySlackRecordsDelivery(t *testing.T) {
	deliveries := &memoryDeliveries{}
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifySlack(context.Background(), "proj-1", "https://hooks.slack.test/services/T000/B000/XYZ", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeTransaction,
		Transaction: "GET /checkout",
		DurationMS:  812,
		Timestamp:   time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifySlack: %v", err)
	}
	if len(deliveries.records) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries.records))
	}
	got := deliveries.records[0]
	if got.Kind != DeliveryKindSlack || got.Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery: %+v", got)
	}
}

func TestNotifyWebhookIncludesProfileContext(t *testing.T) {
	deliveries := &memoryDeliveries{}
	var payload map[string]any
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return &http.Response{
			StatusCode: 202,
			Body:       io.NopCloser(strings.NewReader("accepted")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifyWebhook(context.Background(), "proj-1", "https://hooks.example.test/alerts", alert.TriggerEvent{
		RuleID:      "rule-1",
		EventID:     "evt-1",
		EventType:   alert.EventTypeTransaction,
		Transaction: "GET /checkout",
		TraceID:     "trace-1",
		DurationMS:  812,
		Profile: &alert.ProfileContext{
			ProfileID:   "profile-1",
			URL:         "/profiles/profile-1/",
			TopFunction: "dbQuery",
			SampleCount: 9,
		},
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyWebhook: %v", err)
	}
	profile, ok := payload["profile"].(map[string]any)
	if !ok || profile["profileId"] != "profile-1" || profile["topFunction"] != "dbQuery" {
		t.Fatalf("unexpected webhook payload: %+v", payload)
	}
	if len(deliveries.records) != 1 || !strings.Contains(deliveries.records[0].PayloadJSON, "\"profileId\":\"profile-1\"") {
		t.Fatalf("unexpected delivery payload: %+v", deliveries.records)
	}
}

func TestNotifyWebhookSucceedsWhenDeliveryRecorderFails(t *testing.T) {
	deliveries := &memoryDeliveries{err: errors.New("delivery sink offline")}
	n := NewNotifier(nil, deliveries)
	n.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 202,
			Body:       io.NopCloser(strings.NewReader("accepted")),
			Header:     make(http.Header),
		}, nil
	})}

	err := n.NotifyWebhook(context.Background(), "proj-1", "https://hooks.example.test/alerts", alert.TriggerEvent{
		RuleID:    "rule-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyWebhook: %v", err)
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Status != DeliveryStatusDelivered {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

func TestNotifyEmailSucceedsWhenDeliveryRecorderFails(t *testing.T) {
	outbox := &memoryOutbox{}
	deliveries := &memoryDeliveries{err: errors.New("delivery sink offline")}
	n := NewNotifier(outbox, deliveries)

	err := n.NotifyEmail(context.Background(), "proj-1", "dev@example.com", alert.TriggerEvent{
		RuleID:    "rule-1",
		EventID:   "evt-1",
		Timestamp: time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NotifyEmail: %v", err)
	}
	if len(outbox.records) != 1 {
		t.Fatalf("expected 1 outbox record, got %d", len(outbox.records))
	}
	if len(deliveries.records) != 1 || deliveries.records[0].Kind != DeliveryKindEmail {
		t.Fatalf("unexpected delivery records: %+v", deliveries.records)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
