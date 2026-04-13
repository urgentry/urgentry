package postgrescontrol

import (
	"context"
	"testing"
	"time"
)

func TestPrincipalShadowListsOnlyActiveMembers(t *testing.T) {
	db, fx := seedControlFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'owner', $4)`,
		"member-1", fx.OrgID, fx.UserID, now,
	); err != nil {
		t.Fatalf("seed active member: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, FALSE, $4, $4)`,
		"user-2", "inactive@example.com", "Inactive User", now,
	); err != nil {
		t.Fatalf("seed inactive user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES ($1, $2, $3, 'member', $4)`,
		"member-2", fx.OrgID, "user-2", now,
	); err != nil {
		t.Fatalf("seed inactive member: %v", err)
	}

	users, err := ListActiveUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListActiveUsers: %v", err)
	}
	if len(users) != 1 || users[0].ID != fx.UserID || users[0].Email != fx.UserEmail {
		t.Fatalf("unexpected active users: %+v", users)
	}

	members, err := ListOrganizationMembers(ctx, db)
	if err != nil {
		t.Fatalf("ListOrganizationMembers: %v", err)
	}
	if len(members) != 1 || members[0].UserID != fx.UserID || members[0].OrganizationSlug != fx.OrgSlug {
		t.Fatalf("unexpected organization members: %+v", members)
	}
}
