package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	scimcore "urgentry/internal/scim"
)

type scimOrganizationContextKey struct{}

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

const (
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
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
		if v := r.URL.Query().Get("startIndex"); v != "" {
			fmt.Sscanf(v, "%d", &startIndex)
		}
		if v := r.URL.Query().Get("count"); v != "" {
			fmt.Sscanf(v, "%d", &count)
		}
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
func handleSCIMCreateUser(store scimcore.UserStore) http.HandlerFunc {
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
		writeSCIMJSON(w, http.StatusCreated, recordToSCIM(*created))
	}
}

// handleSCIMPatchUser handles PATCH /api/0/organizations/{org_slug}/scim/v2/Users/{id}.
func handleSCIMPatchUser(store scimcore.UserStore) http.HandlerFunc {
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
		writeSCIMJSON(w, http.StatusOK, recordToSCIM(*updated))
	}
}

// RegisterSCIMRoutes registers org-scoped SCIM 2.0 /Users endpoints on the given mux.
func RegisterSCIMRoutes(mux *http.ServeMux, catalog controlplane.CatalogStore, store scimcore.UserStore, authorize authFunc) {
	if mux == nil || catalog == nil || store == nil || authorize == nil {
		return
	}
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Users", scimOrgAdminGuard(catalog, authorize, handleSCIMListUsers(store)))
	mux.Handle("POST /api/0/organizations/{org_slug}/scim/v2/Users", scimOrgAdminGuard(catalog, authorize, handleSCIMCreateUser(store)))
	mux.Handle("GET /api/0/organizations/{org_slug}/scim/v2/Users/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMGetUser(store)))
	mux.Handle("PATCH /api/0/organizations/{org_slug}/scim/v2/Users/{id}", scimOrgAdminGuard(catalog, authorize, handleSCIMPatchUser(store)))
}
