package selfhostedops

import (
	"testing"

	"urgentry/internal/postgrescontrol"
	"urgentry/internal/store"
)

func TestRecordOperatorAction(t *testing.T) {
	db, dsn := openMigratedMaintenanceDatabase(t, "operator_action")

	receipt, err := RecordOperatorAction(t.Context(), dsn, store.OperatorAuditRecord{
		Action:       "secret.rotate",
		Source:       "ops",
		Actor:        "ops-user",
		Detail:       "rotated dashboard secret",
		MetadataJSON: `{"secret":"grafana-admin"}`,
	})
	if err != nil {
		t.Fatalf("RecordOperatorAction() error = %v", err)
	}
	if receipt.Action != "secret.rotate" || receipt.Actor != "ops-user" || receipt.Status != "succeeded" {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}

	items, err := postgrescontrol.NewOperatorAuditStore(db).List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].Action != "secret.rotate" {
		t.Fatalf("unexpected operator actions: %+v", items)
	}
}

func TestRecordOperatorActionRejectsInvalidMetadata(t *testing.T) {
	_, dsn := openMigratedMaintenanceDatabase(t, "operator_action_invalid")
	if _, err := RecordOperatorAction(t.Context(), dsn, store.OperatorAuditRecord{
		Action:       "backup.capture",
		MetadataJSON: "{not-json}",
	}); err == nil {
		t.Fatal("RecordOperatorAction() error = nil, want invalid metadata failure")
	}
}
