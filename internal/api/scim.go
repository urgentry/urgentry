package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	scimcore "urgentry/internal/scim"
	"urgentry/internal/sqlite"
)

type scimOrganizationContextKey struct{}

func parseSCIMPagination(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// ---------------------------------------------------------------------------
// SCIM JSON wire types (RFC 7643 / 7644)
// ---------------------------------------------------------------------------

type scimListResponse struct {
	Schemas      []string       `json:"schemas"`
	TotalResults int            `json:"totalResults"`
	StartIndex   int            `json:"startIndex"`
	ItemsPerPage int            `json:"itemsPerPage"`
	Resources    []scimUserRepr `json:"Resources"`
}

type scimUserRepr struct {
	Schemas     []string    `json:"schemas"`
	ID          string      `json:"id"`
	ExternalID  string      `json:"externalId,omitempty"`
	UserName    string      `json:"userName"`
	Name        *scimName   `json:"name,omitempty"`
	DisplayName string      `json:"displayName,omitempty"`
	Emails      []scimEmail `json:"emails,omitempty"`
	Active      bool        `json:"active"`
	Meta        scimMeta    `json:"meta"`
}

type scimName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type scimEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary"`
}

type scimMeta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

type scimCreateRequest struct {
	Schemas     []string    `json:"schemas"`
	ExternalID  string      `json:"externalId"`
	UserName    string      `json:"userName"`
	Name        *scimName   `json:"name"`
	DisplayName string      `json:"displayName"`
	Emails      []scimEmail `json:"emails"`
	Active      *bool       `json:"active"`
}

type scimPatchRequest struct {
	Schemas    []string           `json:"schemas"`
	Operations []scimcore.PatchOp `json:"Operations"`
}

type scimError struct {
	Schemas []string `json:"schemas"`
	Detail  string   `json:"detail"`
	Status  int      `json:"status"`
}

// ---------------------------------------------------------------------------
// SCIM Groups wire types
// ---------------------------------------------------------------------------

type scimGroupListResponse struct {
	Schemas      []string        `json:"schemas"`
	TotalResults int             `json:"totalResults"`
	StartIndex   int             `json:"startIndex"`
	ItemsPerPage int             `json:"itemsPerPage"`
	Resources    []scimGroupRepr `json:"Resources"`
}

type scimGroupRepr struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members,omitempty"`
	Meta        scimMeta          `json:"meta"`
}

type scimGroupMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

type scimGroupCreateRequest struct {
	Schemas     []string          `json:"schemas"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members,omitempty"`
}

const (
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
)

func recordToSCIM(rec scimcore.UserRecord) scimUserRepr {
	createdAt := ""
	if !rec.CreatedAt.IsZero() {
		createdAt = rec.CreatedAt.Format(time.RFC3339)
	}
	updatedAt := createdAt
	if !rec.UpdatedAt.IsZero() {
		updatedAt = rec.UpdatedAt.Format(time.RFC3339)
	}
	r := scimUserRepr{
		Schemas:     []string{scimUserSchema},
		ID:          rec.ID,
		ExternalID:  rec.ExternalID,
		UserName:    rec.Email,
		DisplayName: rec.DisplayName,
		Active:      rec.Active,
		Meta: scimMeta{
			ResourceType: "User",
			Created:      createdAt,
			LastModified: updatedAt,
		},
	}
	if rec.Email != "" {
		r.Emails = []scimEmail{{Value: rec.Email, Type: "work", Primary: true}}
	}
	if rec.GivenName != "" || rec.FamilyName != "" {
		r.Name = &scimName{GivenName: rec.GivenName, FamilyName: rec.FamilyName}
	}
	return r
}

func writeSCIMError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(scimError{
		Schemas: []string{scimErrorSchema},
		Detail:  detail,
		Status:  status,
	})
}

func writeSCIMJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func recordSCIMAudit(r *http.Request, audits *sqlite.AuditStore, orgID, action string) {
	if audits == nil || strings.TrimSpace(orgID) == "" || strings.TrimSpace(action) == "" {
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	record := sqlite.AuditRecord{
		OrganizationID: orgID,
		Action:         action,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	}
	if principal != nil {
		record.CredentialType = string(principal.Kind)
		record.CredentialID = principal.CredentialID
		if principal.User != nil {
			record.UserID = principal.User.ID
		}
	}
	_ = audits.Record(r.Context(), record)
}

func extractSCIMBearer(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func scimOrganizationID(ctx context.Context) string {
	orgID, _ := ctx.Value(scimOrganizationContextKey{}).(string)
	return strings.TrimSpace(orgID)
}

func withSCIMOrganization(r *http.Request, orgID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), scimOrganizationContextKey{}, strings.TrimSpace(orgID)))
}

func scimOrgAdminGuard(catalog controlplane.CatalogStore, authorize authFunc, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if extractSCIMBearer(r) == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}
		if !authorize(w, r) {
			return
		}
		org, err := catalog.GetOrganization(r.Context(), PathParam(r, "org_slug"))
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			writeSCIMError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		next.ServeHTTP(w, withSCIMOrganization(r, org.ID))
	}
}

// handleSCIMListUsers handles GET /api/0/organizations/{org_slug}/scim/v2/Users.
func handleSCIMListUsers(store scimcore.UserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		startIndex := 1
		count := 100
		startIndex = parseSCIMPagination(r.URL.Query().Get("startIndex"), startIndex)
		count = parseSCIMPagination(r.URL.Query().Get("count"), count)
		if startIndex < 1 {
			startIndex = 1
		}
		if count < 1 || count > 1000 {
			count = 100
		}
		filter := r.URL.Query().Get("filter")

		records, total, err := store.ListUsers(r.Context(), orgID, startIndex, count, filter)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to list users.")
			return
		}

		resources := make([]scimUserRepr, 0, len(records))
		for _, rec := range records {
			resources = append(resources, recordToSCIM(rec))
		}
		writeSCIMJSON(w, http.StatusOK, scimListResponse{
			Schemas:      []string{scimListSchema},
			TotalResults: total,
			StartIndex:   startIndex,
			ItemsPerPage: len(resources),
			Resources:    resources,
		})
	}
}

// handleSCIMGetUser handles GET /api/0/organizations/{org_slug}/scim/v2/Users/{id}.
func handleSCIMGetUser(store scimcore.UserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		rec, err := store.GetUser(r.Context(), orgID, PathParam(r, "id"))
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to get user.")
			return
		}
		if rec == nil {
			writeSCIMError(w, http.StatusNotFound, "User not found.")
			return
		}
		writeSCIMJSON(w, http.StatusOK, recordToSCIM(*rec))
	}
}

// handleSCIMCreateUser handles POST /api/0/organizations/{org_slug}/scim/v2/Users.
func handleSCIMCreateUser(store scimcore.UserStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		var body scimCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}

		email := strings.TrimSpace(body.UserName)
		if email == "" {
			for _, e := range body.Emails {
				if e.Primary || email == "" {
					email = strings.TrimSpace(e.Value)
				}
			}
		}
		if email == "" {
			writeSCIMError(w, http.StatusBadRequest, "userName or primary email is required.")
			return
		}

		active := true
		if body.Active != nil {
			active = *body.Active
		}

		rec := scimcore.UserRecord{
			ExternalID:  strings.TrimSpace(body.ExternalID),
			Email:       email,
			DisplayName: strings.TrimSpace(body.DisplayName),
			Active:      active,
		}
		if body.Name != nil {
			rec.GivenName = strings.TrimSpace(body.Name.GivenName)
			rec.FamilyName = strings.TrimSpace(body.Name.FamilyName)
		}
		scimcore.NormalizeUserRecord(&rec)

		created, err := store.CreateUser(r.Context(), orgID, rec)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "conflict") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				writeSCIMError(w, http.StatusConflict, "User already exists.")
				return
			}
			writeSCIMError(w, http.StatusInternalServerError, "Failed to create user.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.user.created")
		writeSCIMJSON(w, http.StatusCreated, recordToSCIM(*created))
	}
}

// handleSCIMPatchUser handles PATCH /api/0/organizations/{org_slug}/scim/v2/Users/{id}.
func handleSCIMPatchUser(store scimcore.UserStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		var body scimPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}
		if len(body.Operations) == 0 {
			writeSCIMError(w, http.StatusBadRequest, "At least one operation is required.")
			return
		}

		updated, err := store.PatchUser(r.Context(), orgID, PathParam(r, "id"), body.Operations)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				writeSCIMError(w, http.StatusNotFound, "User not found.")
				return
			}
			writeSCIMError(w, http.StatusInternalServerError, "Failed to update user.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.user.updated")
		writeSCIMJSON(w, http.StatusOK, recordToSCIM(*updated))
	}
}

// handleSCIMDeleteUser handles DELETE /api/0/organizations/{org_slug}/scim/v2/Users/{id}.
// Per RFC 7644 §3.6, this deactivates the user and removes their org membership.
func handleSCIMDeleteUser(store scimcore.UserStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		ok, err := store.DeleteUser(r.Context(), orgID, PathParam(r, "id"))
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to deprovision user.")
			return
		}
		if !ok {
			writeSCIMError(w, http.StatusNotFound, "User not found.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.user.deprovisioned")
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSCIMListGroups handles GET /api/0/organizations/{org_slug}/scim/v2/Groups.
func handleSCIMListGroups(admin controlplane.AdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		// Derive orgSlug from the path since admin store uses slug-based APIs.
		orgSlug := PathParam(r, "org_slug")
		teams, err := admin.ListTeams(r.Context(), orgSlug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to list groups.")
			return
		}

		startIndex := 1
		count := 100
		startIndex = parseSCIMPagination(r.URL.Query().Get("startIndex"), startIndex)
		count = parseSCIMPagination(r.URL.Query().Get("count"), count)
		if startIndex < 1 {
			startIndex = 1
		}
		if count < 1 || count > 1000 {
			count = 100
		}

		total := len(teams)
		offset := startIndex - 1
		if offset > total {
			offset = total
		}
		end := offset + count
		if end > total {
			end = total
		}
		page := teams[offset:end]

		resources := make([]scimGroupRepr, 0, len(page))
		for _, t := range page {
			resources = append(resources, teamRecordToSCIMGroup(t, nil))
		}
		writeSCIMJSON(w, http.StatusOK, scimGroupListResponse{
			Schemas:      []string{scimListSchema},
			TotalResults: total,
			StartIndex:   startIndex,
			ItemsPerPage: len(resources),
			Resources:    resources,
		})
	}
}

// handleSCIMCreateGroup handles POST /api/0/organizations/{org_slug}/scim/v2/Groups.
func handleSCIMCreateGroup(admin controlplane.AdminStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		var body scimGroupCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}

		name := strings.TrimSpace(body.DisplayName)
		if name == "" {
			writeSCIMError(w, http.StatusBadRequest, "displayName is required.")
			return
		}

		slug := scimSlugify(name)
		orgSlug := PathParam(r, "org_slug")
		rec, err := admin.CreateTeam(r.Context(), orgSlug, slug, name)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to create group.")
			return
		}
		if rec == nil {
			writeSCIMError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		// Add any requested members.
		for _, m := range body.Members {
			if strings.TrimSpace(m.Value) != "" {
				member, err := admin.AddTeamMember(r.Context(), orgSlug, slug, strings.TrimSpace(m.Value), "member")
				if err != nil {
					writeSCIMError(w, http.StatusInternalServerError, "Failed to add group member.")
					return
				}
				if member == nil {
					writeSCIMError(w, http.StatusBadRequest, "Group member not found.")
					return
				}
			}
		}

		members, err := admin.ListTeamMembers(r.Context(), orgSlug, slug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to load group members.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.group.created")
		group := teamRecordToSCIMGroup(rec, members)
		writeSCIMJSON(w, http.StatusCreated, group)
	}
}

// handleSCIMGetGroup handles GET /api/0/organizations/{org_slug}/scim/v2/Groups/{id}.
func handleSCIMGetGroup(admin controlplane.AdminStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		orgSlug := PathParam(r, "org_slug")
		teamSlug := PathParam(r, "id")
		rec, _, _, err := admin.GetTeam(r.Context(), orgSlug, teamSlug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to load group.")
			return
		}
		if rec == nil {
			writeSCIMError(w, http.StatusNotFound, "Group not found.")
			return
		}
		members, err := admin.ListTeamMembers(r.Context(), orgSlug, teamSlug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to load group members.")
			return
		}
		writeSCIMJSON(w, http.StatusOK, teamRecordToSCIMGroup(rec, members))
	}
}

// handleSCIMPatchGroup handles PATCH /api/0/organizations/{org_slug}/scim/v2/Groups/{id}.
func handleSCIMPatchGroup(admin controlplane.AdminStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		var body scimPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}

		orgSlug := PathParam(r, "org_slug")
		teamSlug := PathParam(r, "id")

		// Apply operations. We support "replace displayName" plus member add/remove/replace.
		for _, op := range body.Operations {
			kind := strings.ToLower(strings.TrimSpace(op.Op))
			if kind == "" {
				kind = "replace"
			}
			path := strings.ToLower(strings.TrimSpace(op.Path))

			switch {
			case (kind == "replace" || kind == "add") && path == "displayname":
				if name, ok := op.Value.(string); ok && strings.TrimSpace(name) != "" {
					newName := strings.TrimSpace(name)
					if _, err := admin.UpdateTeam(r.Context(), orgSlug, teamSlug, &newName, nil); err != nil {
						writeSCIMError(w, http.StatusInternalServerError, "Failed to update group.")
						return
					}
				}
			case kind == "replace" && path == "members":
				members, ok := scimMemberIDs(op.Value)
				if !ok {
					writeSCIMError(w, http.StatusBadRequest, "Invalid members patch value.")
					return
				}
				currentMembers, err := admin.ListTeamMembers(r.Context(), orgSlug, teamSlug)
				if err != nil {
					writeSCIMError(w, http.StatusInternalServerError, "Failed to load group members.")
					return
				}
				desired := make(map[string]struct{}, len(members))
				for _, userID := range members {
					desired[userID] = struct{}{}
				}
				for _, member := range currentMembers {
					if member == nil {
						continue
					}
					if _, keep := desired[member.UserID]; keep {
						delete(desired, member.UserID)
						continue
					}
					if _, err := admin.RemoveTeamMember(r.Context(), orgSlug, teamSlug, member.UserID); err != nil {
						writeSCIMError(w, http.StatusInternalServerError, "Failed to remove group member.")
						return
					}
				}
				for userID := range desired {
					member, err := admin.AddTeamMember(r.Context(), orgSlug, teamSlug, userID, "member")
					if err != nil {
						writeSCIMError(w, http.StatusInternalServerError, "Failed to add group member.")
						return
					}
					if member == nil {
						writeSCIMError(w, http.StatusBadRequest, "Group member not found.")
						return
					}
				}
			case kind == "add" && path == "members":
				memberIDs, ok := scimMemberIDs(op.Value)
				if !ok {
					writeSCIMError(w, http.StatusBadRequest, "Invalid members patch value.")
					return
				}
				for _, userID := range memberIDs {
					member, err := admin.AddTeamMember(r.Context(), orgSlug, teamSlug, userID, "member")
					if err != nil {
						writeSCIMError(w, http.StatusInternalServerError, "Failed to add group member.")
						return
					}
					if member == nil {
						writeSCIMError(w, http.StatusBadRequest, "Group member not found.")
						return
					}
				}
			case kind == "remove" && strings.HasPrefix(path, "members"):
				if userID := extractSCIMMemberFilter(path); userID != "" {
					if _, err := admin.RemoveTeamMember(r.Context(), orgSlug, teamSlug, userID); err != nil {
						writeSCIMError(w, http.StatusInternalServerError, "Failed to remove group member.")
						return
					}
				}
			}
		}

		rec, _, _, err := admin.GetTeam(r.Context(), orgSlug, teamSlug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to update group.")
			return
		}
		if rec == nil {
			writeSCIMError(w, http.StatusNotFound, "Group not found.")
			return
		}
		members, err := admin.ListTeamMembers(r.Context(), orgSlug, teamSlug)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to load group members.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.group.updated")
		writeSCIMJSON(w, http.StatusOK, teamRecordToSCIMGroup(rec, members))
	}
}

// handleSCIMDeleteGroup handles DELETE /api/0/organizations/{org_slug}/scim/v2/Groups/{id}.
func handleSCIMDeleteGroup(admin controlplane.AdminStore, audits *sqlite.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := scimOrganizationID(r.Context())
		if orgID == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}

		ok, err := admin.DeleteTeam(r.Context(), PathParam(r, "org_slug"), PathParam(r, "id"))
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "Failed to delete group.")
			return
		}
		if !ok {
			writeSCIMError(w, http.StatusNotFound, "Group not found.")
			return
		}
		recordSCIMAudit(r, audits, orgID, "scim.group.deleted")
		w.WriteHeader(http.StatusNoContent)
	}
}

func teamRecordToSCIMGroup(rec *controlplane.TeamRecord, members []*controlplane.TeamMemberRecord) scimGroupRepr {
	createdAt := ""
	if !rec.CreatedAt.IsZero() {
		createdAt = rec.CreatedAt.Format(time.RFC3339)
	}
	groupMembers := make([]scimGroupMember, 0, len(members))
	for _, member := range members {
		if member == nil {
			continue
		}
		groupMembers = append(groupMembers, scimGroupMember{
			Value:   member.UserID,
			Display: strings.TrimSpace(firstNonEmptyString(member.Name, member.Email, member.UserID)),
		})
	}
	return scimGroupRepr{
		Schemas:     []string{scimGroupSchema},
		ID:          rec.Slug,
		DisplayName: rec.Name,
		Members:     groupMembers,
		Meta: scimMeta{
			ResourceType: "Group",
			Created:      createdAt,
			LastModified: createdAt,
		},
	}
}

// scimSlugify converts a display name to a URL-safe slug.
func scimSlugify(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, slug)
	// Collapse repeated dashes.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "team"
	}
	return slug
}

// extractSCIMMemberFilter parses SCIM member path filters like
// `members[value eq "uid"]` and returns the user ID.
func extractSCIMMemberFilter(path string) string {
	// Expected format: members[value eq "uid"]
	start := strings.Index(path, `"`)
	if start < 0 {
		start = strings.Index(path, `'`)
	}
	if start < 0 {
		return ""
	}
	rest := path[start+1:]
	end := strings.IndexAny(rest, `"'`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func scimMemberIDs(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	memberIDs := make([]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		userID, ok := entry["value"].(string)
		if !ok || strings.TrimSpace(userID) == "" {
			return nil, false
		}
		memberIDs = append(memberIDs, strings.TrimSpace(userID))
	}
	return memberIDs, true
}

// RegisterSCIMRoutes registers org-scoped SCIM 2.0 /Users and /Groups endpoints on the given mux.
func RegisterSCIMRoutes(mux *http.ServeMux, catalog controlplane.CatalogStore, store scimcore.UserStore, audits *sqlite.AuditStore, authorize authFunc) {
	if mux == nil || catalog == nil || store == nil || authorize == nil {
		return
	}
	// Users
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Users", scimOrgAdminGuard(catalog, authorize, handleSCIMListUsers(store)))
	mux.Handle("POST /api/0/organizations/{org_slug}/scim/v2/Users", scimOrgAdminGuard(catalog, authorize, handleSCIMCreateUser(store, audits)))
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Users/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMGetUser(store)))
	mux.Handle("PATCH /api/0/organizations/{org_slug}/scim/v2/Users/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMPatchUser(store, audits)))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/scim/v2/Users/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMDeleteUser(store, audits)))
}

// RegisterSCIMGroupRoutes registers org-scoped SCIM 2.0 /Groups endpoints on the given mux.
// Separated from RegisterSCIMRoutes because Groups use the AdminStore, not the SCIM UserStore.
func RegisterSCIMGroupRoutes(mux *http.ServeMux, catalog controlplane.CatalogStore, admin controlplane.AdminStore, audits *sqlite.AuditStore, authorize authFunc) {
	if mux == nil || catalog == nil || admin == nil || authorize == nil {
		return
	}
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Groups", scimOrgAdminGuard(catalog, authorize, handleSCIMListGroups(admin)))
	mux.Handle("POST /api/0/organizations/{org_slug}/scim/v2/Groups", scimOrgAdminGuard(catalog, authorize, handleSCIMCreateGroup(admin, audits)))
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Groups/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMGetGroup(admin)))
	mux.Handle("PATCH /api/0/organizations/{org_slug}/scim/v2/Groups/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMPatchGroup(admin, audits)))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/scim/v2/Groups/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMDeleteGroup(admin, audits)))
}
