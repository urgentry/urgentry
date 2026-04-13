package store

import (
	"context"
	"time"
)

type InstallState struct {
	InstallID            string    `json:"installId"`
	Region               string    `json:"region,omitempty"`
	Environment          string    `json:"environment,omitempty"`
	Version              string    `json:"version,omitempty"`
	BootstrapCompleted   bool      `json:"bootstrapCompleted"`
	BootstrapCompletedAt time.Time `json:"bootstrapCompletedAt,omitempty"`
	MaintenanceMode      bool      `json:"maintenanceMode"`
	MaintenanceReason    string    `json:"maintenanceReason,omitempty"`
	MaintenanceStartedAt time.Time `json:"maintenanceStartedAt,omitempty"`
	DateCreated          time.Time `json:"dateCreated,omitempty"`
	DateUpdated          time.Time `json:"dateUpdated,omitempty"`
}

type InstallStateSync struct {
	Region             string
	Environment        string
	Version            string
	BootstrapCompleted *bool
	CapturedAt         time.Time
}

type LifecycleStore interface {
	GetInstallState(ctx context.Context) (*InstallState, error)
	SyncInstallState(ctx context.Context, sync InstallStateSync) (*InstallState, error)
	SetMaintenanceMode(ctx context.Context, enabled bool, reason string, changedAt time.Time) (*InstallState, error)
}
