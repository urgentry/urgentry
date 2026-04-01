package sqlite

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/auth"
)

func TestPrincipalShadowStoreUpsertUserRefreshesIdentity(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewPrincipalShadowStore(db)
	user := &auth.User{ID: "user-1", Email: "owner@example.com", DisplayName: "Owner"}
	if err := store.UpsertUser(context.Background(), user); err != nil {
		t.Fatalf("UpsertUser() error = %v", err)
	}
	user.Email = "owner+updated@example.com"
	user.DisplayName = "Owner Updated"
	if err := store.UpsertUser(context.Background(), user); err != nil {
		t.Fatalf("UpsertUser() second call error = %v", err)
	}

	var email, name string
	if err := db.QueryRow(`SELECT email, display_name FROM users WHERE id = 'user-1'`).Scan(&email, &name); err != nil {
		t.Fatalf("query user shadow: %v", err)
	}
	if email != "owner+updated@example.com" || name != "Owner Updated" {
		t.Fatalf("shadow user = (%q, %q), want updated values", email, name)
	}
}

func TestPrincipalShadowStoreUpsertAndDeleteOrganizationMember(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewPrincipalShadowStore(db)
	if err := store.UpsertOrganizationMember(context.Background(), OrgMemberRecord{
		ID:               "mem-1",
		OrganizationID:   "org-1",
		OrganizationSlug: "acme",
		UserID:           "user-1",
		Email:            "member@example.com",
		Name:             "Member",
		Role:             "owner",
		CreatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertOrganizationMember() error = %v", err)
	}

	var role string
	if err := db.QueryRow(`SELECT role FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-1'`).Scan(&role); err != nil {
		t.Fatalf("query membership shadow: %v", err)
	}
	if role != "owner" {
		t.Fatalf("role = %q, want owner", role)
	}

	if err := store.DeleteOrganizationMember(context.Background(), "org-1", "user-1"); err != nil {
		t.Fatalf("DeleteOrganizationMember() error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-1'`).Scan(&count); err != nil {
		t.Fatalf("count membership shadow: %v", err)
	}
	if count != 0 {
		t.Fatalf("membership count = %d, want 0", count)
	}
}
