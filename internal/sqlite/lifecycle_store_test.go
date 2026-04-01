package sqlite

import (
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestLifecycleStoreSyncAndMaintenance(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	lifecycle := NewLifecycleStore(db)

	completed := true
	now := time.Date(2026, time.March, 30, 16, 0, 0, 0, time.UTC)
	state, err := lifecycle.SyncInstallState(t.Context(), store.InstallStateSync{
		Region:             "us-west-2",
		Environment:        "production",
		Version:            "v1.2.3",
		BootstrapCompleted: &completed,
		CapturedAt:         now,
	})
	if err != nil {
		t.Fatalf("SyncInstallState() error = %v", err)
	}
	if state == nil || state.InstallID == "" {
		t.Fatalf("SyncInstallState() = %#v", state)
	}
	if state.Region != "us-west-2" || state.Environment != "production" || state.Version != "v1.2.3" {
		t.Fatalf("unexpected synced state: %#v", state)
	}
	if !state.BootstrapCompleted || !state.BootstrapCompletedAt.Equal(now) {
		t.Fatalf("bootstrap state = %#v, want completed at %s", state, now)
	}

	changedAt := now.Add(10 * time.Minute)
	state, err = lifecycle.SetMaintenanceMode(t.Context(), true, "upgrade freeze", changedAt)
	if err != nil {
		t.Fatalf("SetMaintenanceMode(enable) error = %v", err)
	}
	if !state.MaintenanceMode || state.MaintenanceReason != "upgrade freeze" || !state.MaintenanceStartedAt.Equal(changedAt) {
		t.Fatalf("maintenance state = %#v", state)
	}

	state, err = lifecycle.SetMaintenanceMode(t.Context(), false, "", changedAt.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("SetMaintenanceMode(disable) error = %v", err)
	}
	if state.MaintenanceMode || state.MaintenanceReason != "" || !state.MaintenanceStartedAt.IsZero() {
		t.Fatalf("disabled maintenance state = %#v", state)
	}
	if !state.BootstrapCompleted {
		t.Fatalf("bootstrap completion should persist: %#v", state)
	}
}

func TestLifecycleStorePreservesBootstrapWhenSyncDoesNotSpecifyIt(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	lifecycle := NewLifecycleStore(db)

	completed := true
	firstAt := time.Date(2026, time.March, 30, 9, 0, 0, 0, time.UTC)
	if _, err := lifecycle.SyncInstallState(t.Context(), store.InstallStateSync{
		Region:             "us",
		Environment:        "production",
		Version:            "v1",
		BootstrapCompleted: &completed,
		CapturedAt:         firstAt,
	}); err != nil {
		t.Fatalf("initial SyncInstallState() error = %v", err)
	}

	state, err := lifecycle.SyncInstallState(t.Context(), store.InstallStateSync{
		Region:      "us",
		Environment: "production",
		Version:     "v2",
		CapturedAt:  firstAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("second SyncInstallState() error = %v", err)
	}
	if !state.BootstrapCompleted || !state.BootstrapCompletedAt.Equal(firstAt) {
		t.Fatalf("bootstrap fields were not preserved: %#v", state)
	}
	if state.Version != "v2" {
		t.Fatalf("Version = %q, want v2", state.Version)
	}
}
