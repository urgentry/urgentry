package selfhostedops

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"urgentry/internal/postgrescontrol"
	"urgentry/internal/store"
	"urgentry/internal/testpostgres"
)

var maintenancePostgres = testpostgres.NewProvider("selfhostedops-maintenance")
var migratedMaintenancePostgres = maintenancePostgres.NewTemplate("selfhostedops-maintenance-migrated", func(db *sql.DB) error {
	return postgrescontrol.Migrate(context.Background(), db)
})

func openMigratedMaintenanceDatabase(t *testing.T, prefix string) (*sql.DB, string) {
	t.Helper()
	return migratedMaintenancePostgres.OpenDatabaseWithDSN(t, prefix)
}

func TestMaintenanceWorkflow(t *testing.T) {
	db, dsn := openMigratedMaintenanceDatabase(t, "maintenance")
	now := time.Date(2026, time.March, 30, 23, 45, 0, 0, time.UTC)

	status, err := EnterMaintenance(t.Context(), dsn, "upgrade window", "ops-user", "cli", now)
	if err != nil {
		t.Fatalf("EnterMaintenance() error = %v", err)
	}
	if status.WritesOpen || status.DrainState != "draining" {
		t.Fatalf("EnterMaintenance() = %#v, want draining", status)
	}
	if status.Install == nil || !status.Install.MaintenanceMode || status.Install.MaintenanceReason != "upgrade window" {
		t.Fatalf("EnterMaintenance() install = %#v", status.Install)
	}

	status, err = LoadMaintenanceStatus(t.Context(), dsn)
	if err != nil {
		t.Fatalf("LoadMaintenanceStatus() error = %v", err)
	}
	if status.Install == nil || !status.Install.MaintenanceMode {
		t.Fatalf("LoadMaintenanceStatus() = %#v, want active maintenance", status)
	}

	status, err = LeaveMaintenance(t.Context(), dsn, "ops-user", "cli", now.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("LeaveMaintenance() error = %v", err)
	}
	if !status.WritesOpen || status.DrainState != "writes_open" {
		t.Fatalf("LeaveMaintenance() = %#v, want writes open", status)
	}
	if status.Install == nil || status.Install.MaintenanceMode || status.Install.MaintenanceReason != "" {
		t.Fatalf("LeaveMaintenance() install = %#v", status.Install)
	}

	items, err := postgrescontrol.NewOperatorAuditStore(db).List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("install audit count = %d, want 2", len(items))
	}
	if items[0].Action != "maintenance.disabled" || items[0].Actor != "ops-user" {
		t.Fatalf("latest install audit = %#v", items[0])
	}
	if items[1].Action != "maintenance.enabled" || items[1].Detail != "upgrade window" {
		t.Fatalf("first install audit = %#v", items[1])
	}
}

func TestEnterMaintenanceRequiresReason(t *testing.T) {
	_, dsn := openMigratedMaintenanceDatabase(t, "maintenance_reason")
	if _, err := EnterMaintenance(t.Context(), dsn, "", "ops-user", "cli", time.Now().UTC()); err == nil {
		t.Fatal("EnterMaintenance() error = nil, want reason failure")
	}
}

func TestBuildMaintenanceStatusDefaultsWithoutInstallState(t *testing.T) {
	status := buildMaintenanceStatus(nil)
	if !status.WritesOpen || status.DrainState != "writes_open" {
		t.Fatalf("buildMaintenanceStatus(nil) = %#v, want writes open", status)
	}
	if len(status.Steps) != 4 {
		t.Fatalf("step count = %d, want 4", len(status.Steps))
	}
}

func TestBuildMaintenanceStatusForActiveMaintenance(t *testing.T) {
	state := &store.InstallState{
		InstallID:         "install-1",
		MaintenanceMode:   true,
		MaintenanceReason: "upgrade",
	}

	status := buildMaintenanceStatus(state)
	if status.WritesOpen || status.DrainState != "draining" {
		t.Fatalf("buildMaintenanceStatus(active) = %#v, want draining", status)
	}
	if status.Install != state {
		t.Fatalf("install pointer was not preserved")
	}
	if len(status.Steps) != 4 {
		t.Fatalf("step count = %d, want 4", len(status.Steps))
	}
}
