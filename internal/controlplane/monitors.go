package controlplane

import (
	"context"
	"time"
)

type MonitorStore interface {
	ListAllMonitors(ctx context.Context, limit int) ([]Monitor, error)
	ListOrgMonitors(ctx context.Context, orgID string, limit int) ([]Monitor, error)
	ListMonitors(ctx context.Context, projectID string, limit int) ([]Monitor, error)
	UpsertMonitor(ctx context.Context, monitor *Monitor) (*Monitor, error)
	GetMonitor(ctx context.Context, projectID, slug string) (*Monitor, error)
	DeleteMonitor(ctx context.Context, projectID, slug string) error
	SaveCheckIn(ctx context.Context, checkIn *MonitorCheckIn, config *MonitorConfig) (*Monitor, error)
	ListCheckIns(ctx context.Context, projectID, slug string, limit int) ([]MonitorCheckIn, error)
	MarkMissed(ctx context.Context, now time.Time) ([]MonitorCheckIn, error)
}
