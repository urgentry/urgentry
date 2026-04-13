package controlplane

import (
	"context"

	"urgentry/internal/alert"
	"urgentry/internal/notify"
)

type AlertStore interface {
	CreateRule(ctx context.Context, rule *alert.Rule) error
	GetRule(ctx context.Context, id string) (*alert.Rule, error)
	ListRules(ctx context.Context, projectID string) ([]*alert.Rule, error)
	UpdateRule(ctx context.Context, rule *alert.Rule) error
	DeleteRule(ctx context.Context, id string) error
}

// MetricAlertStore persists metric alert rules that trigger on aggregate conditions.
type MetricAlertStore interface {
	CreateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error
	GetMetricAlertRule(ctx context.Context, id string) (*alert.MetricAlertRule, error)
	ListMetricAlertRules(ctx context.Context, projectID string) ([]*alert.MetricAlertRule, error)
	ListAllActiveMetricAlertRules(ctx context.Context) ([]*alert.MetricAlertRule, error)
	UpdateMetricAlertRule(ctx context.Context, r *alert.MetricAlertRule) error
	DeleteMetricAlertRule(ctx context.Context, id string) error
}

type NotificationOutboxStore interface {
	RecordEmail(ctx context.Context, notification *notify.EmailNotification) error
	ListRecent(ctx context.Context, limit int) ([]notify.EmailNotification, error)
}

type NotificationDeliveryStore interface {
	RecordDelivery(ctx context.Context, delivery *notify.DeliveryRecord) error
	ListRecent(ctx context.Context, projectID string, limit int) ([]notify.DeliveryRecord, error)
}
