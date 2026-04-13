package postgrescontrol

import (
	"testing"
	"time"

	sharedstore "urgentry/internal/store"
)

func TestLifecycleStoreSyncAndMaintenance(t *testing.T) {
	t.Parallel()

	db := openMigratedTestDatabase(t)
	store := NewLifecycleStore(db)

	completed := true
	now := time.Date(2026, time.March, 30, 18, 0, 0, 0, time.UTC)
	state, err := store.SyncInstallState(t.Context(), sharedstore.InstallStateSync{
		Region:             "us",
		Environment:        "production",
		Version:            "v2.0.0",
		BootstrapCompleted: &completed,
		CapturedAt:         now,
	})
	if err != nil {
		t.Fatalf("SyncInstallState() error = %v", err)
	}
	if state == nil || state.InstallID == "" {
		t.Fatalf("SyncInstallState() = %#v", state)
	}
	if state.Region != "us" || state.Environment != "production" || state.Version != "v2.0.0" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if !state.BootstrapCompleted || !state.BootstrapCompletedAt.Equal(now) {
		t.Fatalf("bootstrap completion = %#v, want %s", state, now)
	}

	state, err = store.SetMaintenanceMode(t.Context(), true, "upgrade window", now.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("SetMaintenanceMode(enable) error = %v", err)
	}
	if !state.MaintenanceMode || state.MaintenanceReason != "upgrade window" {
		t.Fatalf("enabled maintenance state = %#v", state)
	}

	state, err = store.SetMaintenanceMode(t.Context(), false, "", now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("SetMaintenanceMode(disable) error = %v", err)
	}
	if state.MaintenanceMode || state.MaintenanceReason != "" || !state.MaintenanceStartedAt.IsZero() {
		t.Fatalf("disabled maintenance state = %#v", state)
	}
}
