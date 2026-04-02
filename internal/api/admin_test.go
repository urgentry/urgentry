package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
)

func newSQLiteAuthorizedServer(t *testing.T, db *sql.DB, deps Dependencies) (*httptest.Server, string) {
	return newSQLiteAuthorizedServerWithBootstrap(t, db, deps, "owner@example.com", "Owner", "gpat_test_admin_token")
}

func newSQLiteAuthorizedServerWithBootstrap(t *testing.T, db *sql.DB, deps Dependencies, email, displayName, patToken string) (*httptest.Server, string) {
	t.Helper()

	seedSQLiteAuth(t, db)

	authStore := sqlite.NewAuthStore(db)
	bootstrap, err := authStore.EnsureBootstrapAccess(context.Background(), sqlite.BootstrapOptions{
		DefaultOrganizationID: "test-org-id",
		Email:                 email,
		DisplayName:           displayName,
		Password:              "test-password-123",
		PersonalAccessToken:   patToken,
	})
	if err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	if bootstrap.PAT == "" {
		t.Fatal("bootstrap PAT is empty")
	}

	deps.DB = db
	deps.Auth = auth.NewAuthorizer(authStore, "urgentry_session", "urgentry_csrf", 30*24*time.Hour)
	deps.TokenManager = authStore
	deps = sqliteAuthorizedDependencies(t, db, deps)

	return httptest.NewServer(NewRouter(deps)), bootstrap.PAT
}

func authzJSONRequest(t *testing.T, ts *httptest.Server, method, path, token string, body any) *http.Response {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
	}

	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func orgMemberUserIDByEmail(t *testing.T, db *sql.DB, email string) string {
	t.Helper()

	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&userID); err != nil {
		t.Fatalf("lookup user %q: %v", email, err)
	}
	return userID
}

func addOrgOwner(t *testing.T, db *sql.DB, userID, email, displayName string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		userID, email, displayName, now, now,
	); err != nil {
		t.Fatalf("insert user %q: %v", email, err)
	}
	if _, err := db.Exec(
		`INSERT INTO organization_members (id, organization_id, user_id, role, created_at)
		 VALUES (?, 'test-org-id', ?, 'owner', ?)`,
		userID+"-membership", userID, now,
	); err != nil {
		t.Fatalf("insert owner membership %q: %v", email, err)
	}
}

func TestOrganizationMembershipLifecycle(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	createTeam := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/teams/", pat, map[string]any{
		"slug": "platform",
		"name": "Platform",
	})
	if createTeam.StatusCode != http.StatusCreated {
		t.Fatalf("create team status = %d, want 201", createTeam.StatusCode)
	}
	createTeam.Body.Close()

	createInvite := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/members/", pat, map[string]any{
		"email":    "new-user@example.com",
		"role":     "member",
		"teamSlug": "platform",
	})
	if createInvite.StatusCode != http.StatusCreated {
		t.Fatalf("create invite status = %d, want 201", createInvite.StatusCode)
	}

	var invite CreatedInvite
	decodeBody(t, createInvite, &invite)
	if invite.Token == "" {
		t.Fatal("expected invite token")
	}

	listMembers := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/members/", pat, nil)
	if listMembers.StatusCode != http.StatusOK {
		t.Fatalf("list org members status = %d, want 200", listMembers.StatusCode)
	}
	var pendingMembers []OrganizationMemberListEntry
	decodeBody(t, listMembers, &pendingMembers)
	if len(pendingMembers) != 2 {
		t.Fatalf("pending org member count = %d, want 2", len(pendingMembers))
	}
	foundPendingInvite := false
	for _, member := range pendingMembers {
		if member.Email != "new-user@example.com" {
			continue
		}
		foundPendingInvite = true
		if !member.Pending || member.InviteStatus != "pending" || member.Expired {
			t.Fatalf("pending invite = %+v, want pending/non-expired", member)
		}
		if len(member.Teams) != 1 || member.Teams[0] != "platform" {
			t.Fatalf("pending invite teams = %+v, want [platform]", member.Teams)
		}
	}
	if !foundPendingInvite {
		t.Fatalf("expected pending invite in org members: %+v", pendingMembers)
	}

	acceptInvite := authzJSONRequest(t, ts, http.MethodPost, "/api/0/invites/"+invite.Token+"/accept/", "", map[string]any{
		"displayName": "New User",
		"password":    "temporary-pass-123",
	})
	if acceptInvite.StatusCode != http.StatusCreated {
		t.Fatalf("accept invite status = %d, want 201", acceptInvite.StatusCode)
	}

	var accepted struct {
		User struct {
			ID          string
			Email       string
			DisplayName string
		}
	}
	decodeBody(t, acceptInvite, &accepted)
	if accepted.User.ID == "" {
		t.Fatal("expected accepted user id")
	}

	listMembers = authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/members/", pat, nil)
	if listMembers.StatusCode != http.StatusOK {
		t.Fatalf("list org members status = %d, want 200", listMembers.StatusCode)
	}
	var orgMembers []OrganizationMemberListEntry
	decodeBody(t, listMembers, &orgMembers)
	if len(orgMembers) != 2 {
		t.Fatalf("org member count = %d, want 2", len(orgMembers))
	}
	foundAcceptedUser := false
	for _, member := range orgMembers {
		if member.Email != "new-user@example.com" {
			continue
		}
		foundAcceptedUser = true
		if member.Pending || member.InviteStatus != "" || member.Expired {
			t.Fatalf("accepted member = %+v, want active member", member)
		}
		if len(member.Teams) != 1 || member.Teams[0] != "platform" {
			t.Fatalf("accepted member teams = %+v, want [platform]", member.Teams)
		}
	}
	if !foundAcceptedUser {
		t.Fatalf("expected accepted member in org members: %+v", orgMembers)
	}

	listTeamMembers := authzJSONRequest(t, ts, http.MethodGet, "/api/0/teams/test-org/platform/members/", pat, nil)
	if listTeamMembers.StatusCode != http.StatusOK {
		t.Fatalf("list team members status = %d, want 200", listTeamMembers.StatusCode)
	}
	var teamMembers []Member
	decodeBody(t, listTeamMembers, &teamMembers)
	if len(teamMembers) != 1 || teamMembers[0].Email != "new-user@example.com" {
		t.Fatalf("unexpected team members: %+v", teamMembers)
	}

	removeTeamMember := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/teams/test-org/platform/members/"+accepted.User.ID+"/", pat, nil)
	if removeTeamMember.StatusCode != http.StatusNoContent {
		t.Fatalf("remove team member status = %d, want 204", removeTeamMember.StatusCode)
	}
	removeTeamMember.Body.Close()

	removeOrgMember := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/members/"+accepted.User.ID+"/", pat, nil)
	if removeOrgMember.StatusCode != http.StatusNoContent {
		t.Fatalf("remove org member status = %d, want 204", removeOrgMember.StatusCode)
	}
	removeOrgMember.Body.Close()
}

func TestOrganizationMemberDeleteLastOwnerReturnsBadRequest(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	ownerID := orgMemberUserIDByEmail(t, db, "owner@example.com")
	resp := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/members/"+ownerID+"/", pat, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete last owner status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOrganizationMemberDeleteSecondOwnerReturnsNoContent(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	ownerID := orgMemberUserIDByEmail(t, db, "owner@example.com")
	addOrgOwner(t, db, "second-owner-id", "second-owner@example.com", "Second Owner")

	resp := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/members/"+ownerID+"/", pat, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete owner with second owner present status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOrganizationMemberDeleteMeAliasUsesAuthenticatedUser(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	ownerID := orgMemberUserIDByEmail(t, db, "owner@example.com")
	addOrgOwner(t, db, "second-owner-id", "second-owner@example.com", "Second Owner")

	resp := authzJSONRequest(t, ts, http.MethodDelete, "/api/0/organizations/test-org/members/me/", pat, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete me alias status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organization_members WHERE organization_id = 'test-org-id' AND user_id = ?`, ownerID).Scan(&count); err != nil {
		t.Fatalf("verify owner removal: %v", err)
	}
	if count != 0 {
		t.Fatalf("owner membership count = %d, want 0", count)
	}
}

func TestOrganizationMembershipListShowsExpiredPendingInvite(t *testing.T) {
	db := openTestSQLite(t)
	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	createInvite := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/members/", pat, map[string]any{
		"email": "expired-user@example.com",
		"role":  "member",
	})
	if createInvite.StatusCode != http.StatusCreated {
		t.Fatalf("create invite status = %d, want 201", createInvite.StatusCode)
	}

	var invite CreatedInvite
	decodeBody(t, createInvite, &invite)

	expiredAt := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE member_invites SET expires_at = ? WHERE id = ?`, expiredAt, invite.ID); err != nil {
		t.Fatalf("expire invite: %v", err)
	}

	listMembers := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/members/", pat, nil)
	if listMembers.StatusCode != http.StatusOK {
		t.Fatalf("list org members status = %d, want 200", listMembers.StatusCode)
	}

	var members []OrganizationMemberListEntry
	decodeBody(t, listMembers, &members)
	if len(members) != 2 {
		t.Fatalf("org member count = %d, want 2", len(members))
	}

	for _, member := range members {
		if member.Email != "expired-user@example.com" {
			continue
		}
		if !member.Pending || !member.Expired || member.InviteStatus != "pending" {
			t.Fatalf("expired invite = %+v, want pending expired invite", member)
		}
		return
	}

	t.Fatalf("expected expired invite in org members: %+v", members)
}
