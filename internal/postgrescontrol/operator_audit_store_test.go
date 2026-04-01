package postgrescontrol

import (
	"testing"

	sharedstore "urgentry/internal/store"
)

func TestOperatorAuditStoreRecordAndList(t *testing.T) {
	t.Parallel()

	db := openMigratedTestDatabase(t)
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', now());
		INSERT INTO projects (id, organization_id, team_id, slug, name, platform, status, created_at, updated_at) VALUES ('proj-1', 'org-1', NULL, 'api', 'API', 'go', 'active', now(), now());
	`); err != nil {
		t.Fatalf("seed org/project: %v", err)
	}

	audits := NewOperatorAuditStore(db)
	if err := audits.Record(t.Context(), sharedstore.OperatorAuditRecord{
		Action:         "backup.capture",
		Status:         "succeeded",
		Source:         "compose",
		Actor:          "system",
		Detail:         "captured backup",
		MetadataJSON:   `{"dir":"/tmp/backup"}`,
		OrganizationID: "org-1",
		ProjectID:      "proj-1",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := audits.Record(t.Context(), sharedstore.OperatorAuditRecord{
		Action: "upgrade.apply",
	}); err != nil {
		t.Fatalf("Record(install-wide) error = %v", err)
	}

	items, err := audits.List(t.Context(), "acme", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("List() len = %d, want 2", len(items))
	}
	var installWide *sharedstore.OperatorAuditEntry
	var scoped *sharedstore.OperatorAuditEntry
	for i := range items {
		switch items[i].Action {
		case "upgrade.apply":
			installWide = &items[i]
		case "backup.capture":
			scoped = &items[i]
		}
	}
	if installWide == nil || installWide.Status != "succeeded" || installWide.Source != "system" {
		t.Fatalf("install-wide audit = %#v", installWide)
	}
	if scoped == nil || scoped.ProjectSlug != "api" || scoped.OrganizationSlug != "acme" {
		t.Fatalf("scoped audit = %#v", scoped)
	}
}
