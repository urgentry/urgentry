package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/requestmeta"
	"urgentry/internal/sqlite"
)

const (
	inviteAcceptIPRateLimit    = 10
	inviteAcceptTokenRateLimit = 5
)

type orgMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

type teamMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

var validOrgRoles = map[string]bool{
	"owner":   true,
	"admin":   true,
	"manager": true,
	"member":  true,
}

var validTeamRoles = map[string]bool{
	"member":     true,
	"maintainer": true,
}

type inviteRequest struct {
	Email       string `json:"email"`
	Role        string `json:"role"`
	TeamSlug    string `json:"teamSlug"`
	DisplayName string `json:"displayName"`
}

type inviteAcceptRequest struct {
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

// handleListOrgMembers handles GET /api/0/organizations/{org_slug}/members/.
// handleListOrgUsers returns users in an organization (user objects, not member objects).
func handleListOrgUsers(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		items, err := admin.ListOrgMembers(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list users.")
			return
		}
		type UserResponse struct {
			ID          string     `json:"id"`
			Name        string     `json:"name"`
			Username    string     `json:"username"`
			Email       string     `json:"email"`
			IsActive    bool       `json:"isActive"`
			DateJoined  *time.Time `json:"dateJoined,omitempty"`
			HasPassAuth bool       `json:"has2fa"`
		}
		users := make([]UserResponse, 0, len(items))
		for _, item := range items {
			users = append(users, UserResponse{
				ID:       item.UserID,
				Name:     strings.TrimSpace(item.Name),
				Username: strings.TrimSpace(item.Name),
				Email:    item.Email,
				IsActive: true,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, users)
	}
}

func handleListOrgMembers(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		items, err := admin.ListOrgMembers(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization members.")
			return
		}
		invites, err := admin.ListInvites(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization members.")
			return
		}
		teamsByUser, err := admin.ListOrgMemberTeams(r.Context(), orgSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization members.")
			return
		}
		members := make([]*OrganizationMemberListEntry, 0, len(items)+len(invites))
		for _, item := range items {
			members = append(members, &OrganizationMemberListEntry{
				ID:             item.ID,
				UserID:         item.UserID,
				OrganizationID: item.OrganizationID,
				Email:          item.Email,
				Name:           item.Name,
				Role:           item.Role,
				Teams:          append([]string(nil), teamsByUser[item.UserID]...),
				DateCreated:    item.CreatedAt,
			})
		}
		now := time.Now().UTC()
		for _, invite := range invites {
			if invite == nil || invite.AcceptedAt != nil || strings.TrimSpace(invite.Status) != "pending" {
				continue
			}
			member := &OrganizationMemberListEntry{
				ID:             invite.ID,
				OrganizationID: invite.OrganizationID,
				Email:          invite.Email,
				Role:           invite.Role,
				Pending:        true,
				Expired:        invite.ExpiresAt != nil && invite.ExpiresAt.Before(now),
				InviteStatus:   invite.Status,
				DateCreated:    invite.CreatedAt,
			}
			if slug := strings.TrimSpace(invite.TeamSlug); slug != "" {
				member.Teams = []string{slug}
			}
			members = append(members, member)
		}
		sort.SliceStable(members, func(i, j int) bool {
			return members[i].DateCreated.Before(members[j].DateCreated)
		})
		if members == nil {
			members = []*OrganizationMemberListEntry{}
		}
		httputil.WriteJSON(w, http.StatusOK, members)
	}
}

// handleRemoveOrgMember handles DELETE /api/0/organizations/{org_slug}/members/{member_id}/.
func handleRemoveOrgMember(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		memberID := PathParam(r, "member_id")

		// Resolve "me" to actual user ID.
		principal := authPrincipalFromContext(r.Context())
		if principal != nil && principal.User != nil && memberID == "me" {
			memberID = principal.User.ID
		}

		// Check last owner protection by scanning all members.
		members, err := admin.ListOrgMembers(r.Context(), orgSlug)
		if err == nil {
			var targetRole string
			ownerCount := 0
			for _, m := range members {
				if m.Role == "owner" {
					ownerCount++
				}
				if m.ID == memberID || m.UserID == memberID {
					targetRole = m.Role
				}
			}
			if targetRole == "owner" && ownerCount <= 1 {
				httputil.WriteError(w, http.StatusBadRequest, "Cannot remove the last owner of an organization.")
				return
			}
		}

		ok, err := admin.RemoveOrgMember(r.Context(), orgSlug, memberID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to remove organization member.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Organization member not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetOrgMember handles GET /api/0/organizations/{org_slug}/members/{member_id}/.
func handleGetOrgMember(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		rec, err := admin.GetOrgMember(r.Context(), PathParam(r, "org_slug"), PathParam(r, "member_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization member.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization member not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, &Member{
			ID:             rec.ID,
			UserID:         rec.UserID,
			OrganizationID: rec.OrganizationID,
			Email:          rec.Email,
			Name:           rec.Name,
			Role:           rec.Role,
			DateCreated:    rec.CreatedAt,
		})
	}
}

// handleUpdateOrgMember handles PUT /api/0/organizations/{org_slug}/members/{member_id}/.
func handleUpdateOrgMember(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body orgMemberRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		role := strings.TrimSpace(body.Role)
		if role == "" || !validOrgRoles[role] {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid role. Must be one of: owner, admin, manager, member.")
			return
		}
		rec, err := admin.UpdateOrgMemberRole(r.Context(), PathParam(r, "org_slug"), PathParam(r, "member_id"), role)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update organization member.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization member not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, &Member{
			ID:             rec.ID,
			UserID:         rec.UserID,
			OrganizationID: rec.OrganizationID,
			Email:          rec.Email,
			Name:           rec.Name,
			Role:           rec.Role,
			DateCreated:    rec.CreatedAt,
		})
	}
}

// handleListTeamMembers handles GET /api/0/teams/{org_slug}/{team_slug}/members/.
func handleListTeamMembers(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := admin.ListTeamMembers(r.Context(), PathParam(r, "org_slug"), PathParam(r, "team_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list team members.")
			return
		}
		members := make([]*Member, 0, len(items))
		for _, item := range items {
			members = append(members, &Member{
				ID:             item.ID,
				UserID:         item.UserID,
				OrganizationID: item.OrganizationID,
				TeamID:         item.TeamID,
				Email:          item.Email,
				Name:           item.Name,
				Role:           item.Role,
				DateCreated:    item.CreatedAt,
			})
		}
		if members == nil {
			members = []*Member{}
		}
		httputil.WriteJSON(w, http.StatusOK, members)
	}
}

// handleAddTeamMember handles POST /api/0/teams/{org_slug}/{team_slug}/members/.
func handleAddTeamMember(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body teamMemberRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		body.UserID = strings.TrimSpace(body.UserID)
		if body.UserID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "User ID is required.")
			return
		}
		role := strings.TrimSpace(body.Role)
		if role != "" && !validTeamRoles[role] {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid role. Must be one of: member, maintainer.")
			return
		}
		rec, err := admin.AddTeamMember(r.Context(), PathParam(r, "org_slug"), PathParam(r, "team_slug"), body.UserID, role)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to add team member.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "User, organization, or team not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, &Member{
			ID:             rec.ID,
			UserID:         rec.UserID,
			OrganizationID: rec.OrganizationID,
			TeamID:         rec.TeamID,
			Email:          rec.Email,
			Name:           rec.Name,
			Role:           rec.Role,
			DateCreated:    rec.CreatedAt,
		})
	}
}

// handleRemoveTeamMember handles DELETE /api/0/teams/{org_slug}/{team_slug}/members/{member_id}/.
func handleRemoveTeamMember(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ok, err := admin.RemoveTeamMember(r.Context(), PathParam(r, "org_slug"), PathParam(r, "team_slug"), PathParam(r, "member_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to remove team member.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Team member not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListInvites handles GET /api/0/organizations/{org_slug}/invites/.
func handleListInvites(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		items, err := admin.ListInvites(r.Context(), PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list invites.")
			return
		}
		invites := make([]*Invite, 0, len(items))
		for _, item := range items {
			invites = append(invites, inviteFromRecord(item))
		}
		if invites == nil {
			invites = []*Invite{}
		}
		httputil.WriteJSON(w, http.StatusOK, invites)
	}
}

// handleCreateInvite handles POST /api/0/organizations/{org_slug}/members/ and /invites/.
func handleCreateInvite(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body inviteRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		body.Email = strings.TrimSpace(body.Email)
		if body.Email == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Email is required.")
			return
		}
		role := strings.TrimSpace(body.Role)
		if role != "" && !validOrgRoles[role] {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid role. Must be one of: owner, admin, manager, member.")
			return
		}
		principal := authPrincipalFromRequest(r)
		invite, token, err := admin.CreateInvite(r.Context(), PathParam(r, "org_slug"), body.Email, role, strings.TrimSpace(body.TeamSlug), principalUserID(principal))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create invite.")
			return
		}
		if invite == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization or team not found.")
			return
		}
		created := CreatedInvite{
			Invite: *inviteFromRecord(invite),
			Token:  token,
		}
		httputil.WriteJSON(w, http.StatusCreated, created)
	}
}

// handleRevokeInvite handles DELETE /api/0/organizations/{org_slug}/invites/{invite_id}/.
func handleRevokeInvite(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		ok, err := admin.RevokeInvite(r.Context(), PathParam(r, "org_slug"), PathParam(r, "invite_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to revoke invite.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Invite not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleAcceptInvite handles POST /api/0/invites/{invite_token}/accept/.
func handleAcceptInvite(admin controlplane.AdminStore) http.HandlerFunc {
	limiter := authpkg.NewFixedWindowRateLimiter(time.Minute)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		inviteToken := strings.TrimSpace(PathParam(r, "invite_token"))
		now := time.Now().UTC()
		if retryAfter, allowed := limiter.Allow("invite-accept:ip:"+requestClientIP(r), inviteAcceptIPRateLimit, now); !allowed {
			writeRateLimitError(w, retryAfter, "Too many invite accept attempts.")
			return
		}
		if retryAfter, allowed := limiter.Allow("invite-accept:token:"+inviteAcceptTokenKey(inviteToken), inviteAcceptTokenRateLimit, now); !allowed {
			writeRateLimitError(w, retryAfter, "Too many invite accept attempts.")
			return
		}
		var body inviteAcceptRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		result, err := admin.AcceptInvite(r.Context(), inviteToken, strings.TrimSpace(body.DisplayName), strings.TrimSpace(body.Password))
		if err != nil {
			switch err {
			case sqlite.ErrInviteNotFound:
				httputil.WriteError(w, http.StatusNotFound, "Invite not found.")
			case sqlite.ErrInviteConsumed:
				httputil.WriteError(w, http.StatusConflict, "Invite already accepted.")
			case sqlite.ErrInviteExpired:
				httputil.WriteError(w, http.StatusGone, "Invite expired.")
			default:
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to accept invite.")
			}
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, result)
	}
}

func writeRateLimitError(w http.ResponseWriter, retryAfter time.Duration, message string) {
	retryAfterSeconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		retryAfterSeconds++
	}
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	httputil.WriteError(w, http.StatusTooManyRequests, message)
}

func requestClientIP(r *http.Request) string {
	return requestmeta.ClientIP(r)
}

func inviteAcceptTokenKey(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:8])
}

type projectMemberRoleRequest struct {
	Role string `json:"role"`
}

// handleListProjectMembers handles GET /api/0/projects/{org_slug}/{proj_slug}/members/.
func handleListProjectMembers(admin controlplane.AdminStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		projSlug := PathParam(r, "proj_slug")
		items, err := admin.ListProjectMembers(r.Context(), orgSlug, projSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list project members.")
			return
		}
		members := make([]*ProjectMember, 0, len(items))
		for _, item := range items {
			members = append(members, &ProjectMember{
				ID:          item.ID,
				ProjectID:   item.ProjectID,
				UserID:      item.UserID,
				Email:       item.Email,
				Name:        item.Name,
				Role:        item.Role,
				DateCreated: item.CreatedAt,
			})
		}
		if members == nil {
			members = []*ProjectMember{}
		}
		httputil.WriteJSON(w, http.StatusOK, members)
	}
}

// handleUpdateProjectMemberRole handles PUT /api/0/projects/{org_slug}/{proj_slug}/members/{member_id}/.
func handleUpdateProjectMemberRole(admin controlplane.AdminStore, authCheck authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authCheck(w, r) {
			return
		}
		var body projectMemberRoleRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		role := strings.TrimSpace(body.Role)
		if !authpkg.IsValidProjectRole(role) {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid role. Must be one of: owner, admin, member, viewer.")
			return
		}
		orgSlug := PathParam(r, "org_slug")
		projSlug := PathParam(r, "proj_slug")
		memberID := PathParam(r, "member_id")
		rec, err := admin.UpdateProjectMemberRole(r.Context(), orgSlug, projSlug, memberID, role)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update project member role.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project member not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, &ProjectMember{
			ID:          rec.ID,
			ProjectID:   rec.ProjectID,
			UserID:      rec.UserID,
			Email:       rec.Email,
			Name:        rec.Name,
			Role:        rec.Role,
			DateCreated: rec.CreatedAt,
		})
	}
}

func inviteFromRecord(rec *sqlite.InviteRecord) *Invite {
	if rec == nil {
		return nil
	}
	invite := &Invite{
		ID:               rec.ID,
		OrganizationID:   rec.OrganizationID,
		OrganizationSlug: rec.OrganizationSlug,
		TeamID:           rec.TeamID,
		TeamSlug:         rec.TeamSlug,
		Email:            rec.Email,
		Role:             rec.Role,
		Status:           rec.Status,
		TokenPrefix:      rec.TokenPrefix,
		DateCreated:      rec.CreatedAt,
		AcceptedByUserID: rec.AcceptedByUserID,
	}
	invite.ExpiresAt = rec.ExpiresAt
	invite.AcceptedAt = rec.AcceptedAt
	return invite
}

func authPrincipalFromRequest(r *http.Request) *authpkg.Principal {
	return authpkg.PrincipalFromContext(r.Context())
}

func principalUserID(principal *authpkg.Principal) string {
	if principal != nil && principal.User != nil {
		return principal.User.ID
	}
	return ""
}
