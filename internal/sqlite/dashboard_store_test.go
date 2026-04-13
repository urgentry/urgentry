package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"urgentry/internal/discover"
)

func seedDashboardStoreAuth(t testing.TB, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, now); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES
		('user-admin', 'admin@example.com', 'Admin', 1, ?, ?),
		('user-member', 'member@example.com', 'Member', 1, ?, ?),
		('user-viewer', 'viewer@example.com', 'Viewer', 1, ?, ?)`,
		now, now, now, now, now, now,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES
		('mem-admin', 'org-1', 'user-admin', 'admin', ?),
		('mem-member', 'org-1', 'user-member', 'member', ?),
		('mem-viewer', 'org-1', 'user-viewer', 'viewer', ?)`,
		now, now, now,
	); err != nil {
		t.Fatalf("seed members: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at) VALUES ('proj-1', 'org-1', 'frontend', 'Frontend', 'go', 'active', ?)`, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func TestDashboardStoreVisibilityAndWidgets(t *testing.T) {
	db := openStoreTestDB(t)
	seedDashboardStoreAuth(t, db)
	ctx := context.Background()

	search, err := NewSearchStore(db).Save(ctx, "user-member", "acme", SavedSearchVisibilityPrivate, "Hot issues", "", "ValueError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := NewDashboardStore(db)
	privateDash, err := store.CreateDashboard(ctx, "acme", "user-admin", DashboardInput{
		Title:      "Ops",
		Visibility: DashboardVisibilityPrivate,
		Config:     []byte(`{"refreshSeconds":60,"filters":{"environment":"production"},"annotations":[{"level":"warning","text":"Watch checkout"}]}`),
	})
	if err != nil {
		t.Fatalf("CreateDashboard private: %v", err)
	}
	if !strings.Contains(string(privateDash.Config), `"refreshSeconds":60`) || !strings.Contains(string(privateDash.Config), `"environment":"production"`) {
		t.Fatalf("dashboard config = %s", string(privateDash.Config))
	}
	memberList, err := store.ListDashboards(ctx, "acme", "user-member")
	if err != nil {
		t.Fatalf("ListDashboards private: %v", err)
	}
	if len(memberList) != 0 {
		t.Fatalf("member should not see admin private dashboards: %+v", memberList)
	}

	sharedDash, err := store.UpdateDashboard(ctx, "acme", privateDash.ID, "user-admin", DashboardInput{
		Title:      privateDash.Title,
		Visibility: DashboardVisibilityOrganization,
		Config:     []byte(`{"refreshSeconds":300,"filters":{"environment":"production","release":"1.2.4"},"annotations":[{"level":"critical","text":"Release watch"}]}`),
	})
	if err != nil {
		t.Fatalf("UpdateDashboard shared: %v", err)
	}
	if !strings.Contains(string(sharedDash.Config), `"refreshSeconds":300`) || !strings.Contains(string(sharedDash.Config), `"release":"1.2.4"`) {
		t.Fatalf("shared dashboard config = %s", string(sharedDash.Config))
	}
	memberList, err = store.ListDashboards(ctx, "acme", "user-member")
	if err != nil {
		t.Fatalf("ListDashboards shared: %v", err)
	}
	if len(memberList) != 1 || memberList[0].ID != sharedDash.ID {
		t.Fatalf("member shared dashboards = %+v, want %s", memberList, sharedDash.ID)
	}
	if _, err := store.UpdateDashboard(ctx, "acme", sharedDash.ID, "user-member", DashboardInput{
		Title:      "Not allowed",
		Visibility: DashboardVisibilityOrganization,
	}); !errors.Is(err, ErrDashboardForbidden) {
		t.Fatalf("member update shared dashboard err = %v, want forbidden", err)
	}

	ownDash, err := store.CreateDashboard(ctx, "acme", "user-member", DashboardInput{
		Title:      "My Dashboard",
		Visibility: DashboardVisibilityPrivate,
	})
	if err != nil {
		t.Fatalf("CreateDashboard member: %v", err)
	}
	widget, err := store.CreateWidget(ctx, "acme", ownDash.ID, "user-member", DashboardWidgetInput{
		Title:         "Hot issues",
		Kind:          DashboardWidgetKindTable,
		Position:      1,
		SavedSearchID: search.ID,
		Config:        []byte(`{"thresholds":{"warning":1,"critical":2,"direction":"above"}}`),
	})
	if err != nil {
		t.Fatalf("CreateWidget: %v", err)
	}
	if widget.QueryVersion != discover.CurrentVersion || widget.QueryDoc.Dataset != discover.DatasetIssues {
		t.Fatalf("unexpected widget query: %+v", widget)
	}
	if !strings.Contains(string(widget.Config), `"critical":2`) || !strings.Contains(string(widget.Config), `"direction":"above"`) {
		t.Fatalf("widget config = %s", string(widget.Config))
	}
	got, err := store.GetDashboard(ctx, "acme", ownDash.ID, "user-member")
	if err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].ID != widget.ID {
		t.Fatalf("dashboard widgets = %+v", got.Widgets)
	}
	if !strings.Contains(string(got.Widgets[0].Config), `"warning":1`) {
		t.Fatalf("loaded widget config = %s", string(got.Widgets[0].Config))
	}

	duplicate, err := store.DuplicateDashboard(ctx, "acme", sharedDash.ID, "user-admin", "Ops copy")
	if err != nil {
		t.Fatalf("DuplicateDashboard: %v", err)
	}
	if !strings.Contains(string(duplicate.Config), `"refreshSeconds":300`) || duplicate.Visibility != DashboardVisibilityPrivate {
		t.Fatalf("duplicate dashboard = %+v", duplicate)
	}

	if err := store.DeleteDashboard(ctx, "acme", ownDash.ID, "user-member"); err != nil {
		t.Fatalf("DeleteDashboard: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dashboard_widgets WHERE dashboard_id = ?`, ownDash.ID).Scan(&count); err != nil {
		t.Fatalf("count widgets: %v", err)
	}
	if count != 0 {
		t.Fatalf("widget rows = %d, want 0", count)
	}
}

func TestDashboardStoreMigrationApplied(t *testing.T) {
	db := openStoreTestDB(t)
	for _, table := range []string{"dashboards", "dashboard_widgets"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("lookup table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE version = 42`).Scan(&applied); err != nil {
		t.Fatalf("lookup migration 42: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration 42 count = %d, want 1", applied)
	}
}

func TestDashboardStoreRejectsCrossOrgSavedSearch(t *testing.T) {
	db := openStoreTestDB(t)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES
		('org-0', 'alpha', 'Alpha', ?),
		('org-1', 'acme', 'Acme', ?)`,
		now.Add(-time.Hour).Format(time.RFC3339), now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed organizations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES ('user-member', 'member@example.com', 'Member', 1, ?, ?)`,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('mem-member', 'org-1', 'user-member', 'member', ?)`,
		now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	search, err := NewSearchStore(db).Save(context.Background(), "user-member", "", SavedSearchVisibilityPrivate, "Cross-org search", "", "ValueError", "all", "", "last_seen", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if search.QueryDoc.Scope.Organization != "alpha" {
		t.Fatalf("saved search org = %q, want alpha to prove rebinding", search.QueryDoc.Scope.Organization)
	}

	store := NewDashboardStore(db)
	dashboard, err := store.CreateDashboard(context.Background(), "acme", "user-member", DashboardInput{Title: "My Dashboard"})
	if err != nil {
		t.Fatalf("CreateDashboard: %v", err)
	}
	widget, err := store.CreateWidget(context.Background(), "acme", dashboard.ID, "user-member", DashboardWidgetInput{
		Title:         "Hot issues",
		Kind:          DashboardWidgetKindTable,
		SavedSearchID: search.ID,
	})
	if err == nil {
		t.Fatalf("CreateWidget unexpectedly succeeded: %+v", widget)
	}
}
