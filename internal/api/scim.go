package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

)

// ---------------------------------------------------------------------------
// SCIM 2.0 /Users provisioning endpoint
// ---------------------------------------------------------------------------

// SCIMUserStore abstracts user CRUD needed by the SCIM handler. Implementors
// map SCIM operations to the Urgentry user model.
type SCIMUserStore interface {
	SCIMListUsers(ctx context.Context, orgID string, startIndex, count int, filter string) ([]SCIMUserRecord, int, error)
	SCIMGetUser(ctx context.Context, orgID, userID string) (*SCIMUserRecord, error)
	SCIMCreateUser(ctx context.Context, orgID string, user SCIMUserRecord) (*SCIMUserRecord, error)
	SCIMPatchUser(ctx context.Context, orgID, userID string, ops []SCIMPatchOp) (*SCIMUserRecord, error)
}

// SCIMUserRecord is the Urgentry-side representation of a SCIM User.
type SCIMUserRecord struct {
	ID          string
	ExternalID  string
	Email       string
	DisplayName string
	GivenName   string
	FamilyName  string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SCIMPatchOp represents one RFC 7644 PATCH operation.
type SCIMPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// SCIMBearerAuth validates the bearer token for SCIM requests. It reuses
// the automation token pattern: callers supply a function that resolves a
// bearer token to an org ID (or returns an error).
type SCIMBearerAuth func(ctx context.Context, token string) (orgID string, err error)

// ---------------------------------------------------------------------------
// SCIM JSON wire types (RFC 7643 / 7644)
// ---------------------------------------------------------------------------

type scimListResponse struct {
	Schemas      []string        `json:"schemas"`
	TotalResults int             `json:"totalResults"`
	StartIndex   int             `json:"startIndex"`
	ItemsPerPage int             `json:"itemsPerPage"`
	Resources    []scimUserRepr  `json:"Resources"`
}

type scimUserRepr struct {
	Schemas    []string       `json:"schemas"`
	ID         string         `json:"id"`
	ExternalID string         `json:"externalId,omitempty"`
	UserName   string         `json:"userName"`
	Name       *scimName      `json:"name,omitempty"`
	DisplayName string        `json:"displayName,omitempty"`
	Emails     []scimEmail    `json:"emails,omitempty"`
	Active     bool           `json:"active"`
	Meta       scimMeta       `json:"meta"`
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
	Schemas    []string     `json:"schemas"`
	Operations []SCIMPatchOp `json:"Operations"`
}

type scimError struct {
	Schemas []string `json:"schemas"`
	Detail  string   `json:"detail"`
	Status  int      `json:"status"`
}

// ---------------------------------------------------------------------------
// Mapping helpers
// ---------------------------------------------------------------------------

const (
	scimUserSchema    = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema    = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema   = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema   = "urn:ietf:params:scim:api:messages:2.0:Error"
)

func recordToSCIM(rec SCIMUserRecord) scimUserRepr {
	r := scimUserRepr{
		Schemas:     []string{scimUserSchema},
		ID:          rec.ID,
		ExternalID:  rec.ExternalID,
		UserName:    rec.Email,
		DisplayName: rec.DisplayName,
		Active:      rec.Active,
		Meta: scimMeta{
			ResourceType: "User",
			Created:      rec.CreatedAt.Format(time.RFC3339),
			LastModified: rec.UpdatedAt.Format(time.RFC3339),
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

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func extractSCIMBearer(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

// handleSCIMListUsers handles GET /api/scim/v2/Users.
func handleSCIMListUsers(store SCIMUserStore, auth SCIMBearerAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractSCIMBearer(r)
		if token == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}
		orgID, err := auth(r.Context(), token)
		if err != nil {
			writeSCIMError(w, http.StatusUnauthorized, "Invalid bearer token.")
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

		records, total, err := store.SCIMListUsers(r.Context(), orgID, startIndex, count, filter)
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

// handleSCIMGetUser handles GET /api/scim/v2/Users/{id}.
func handleSCIMGetUser(store SCIMUserStore, auth SCIMBearerAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractSCIMBearer(r)
		if token == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}
		orgID, err := auth(r.Context(), token)
		if err != nil {
			writeSCIMError(w, http.StatusUnauthorized, "Invalid bearer token.")
			return
		}

		userID := PathParam(r, "id")
		rec, err := store.SCIMGetUser(r.Context(), orgID, userID)
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

// handleSCIMCreateUser handles POST /api/scim/v2/Users.
func handleSCIMCreateUser(store SCIMUserStore, auth SCIMBearerAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractSCIMBearer(r)
		if token == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}
		orgID, err := auth(r.Context(), token)
		if err != nil {
			writeSCIMError(w, http.StatusUnauthorized, "Invalid bearer token.")
			return
		}

		var body scimCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}

		// Resolve primary email.
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

		rec := SCIMUserRecord{
			ExternalID:  strings.TrimSpace(body.ExternalID),
			Email:       email,
			DisplayName: strings.TrimSpace(body.DisplayName),
			Active:      active,
		}
		if body.Name != nil {
			rec.GivenName = strings.TrimSpace(body.Name.GivenName)
			rec.FamilyName = strings.TrimSpace(body.Name.FamilyName)
		}
		if rec.DisplayName == "" && (rec.GivenName != "" || rec.FamilyName != "") {
			rec.DisplayName = strings.TrimSpace(rec.GivenName + " " + rec.FamilyName)
		}

		created, err := store.SCIMCreateUser(r.Context(), orgID, rec)
		if err != nil {
			if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "duplicate") {
				writeSCIMError(w, http.StatusConflict, "User already exists.")
				return
			}
			writeSCIMError(w, http.StatusInternalServerError, "Failed to create user.")
			return
		}
		writeSCIMJSON(w, http.StatusCreated, recordToSCIM(*created))
	}
}

// handleSCIMPatchUser handles PATCH /api/scim/v2/Users/{id}.
func handleSCIMPatchUser(store SCIMUserStore, auth SCIMBearerAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractSCIMBearer(r)
		if token == "" {
			writeSCIMError(w, http.StatusUnauthorized, "Bearer token required.")
			return
		}
		orgID, err := auth(r.Context(), token)
		if err != nil {
			writeSCIMError(w, http.StatusUnauthorized, "Invalid bearer token.")
			return
		}

		userID := PathParam(r, "id")

		var body scimPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}

		if len(body.Operations) == 0 {
			writeSCIMError(w, http.StatusBadRequest, "At least one operation is required.")
			return
		}

		updated, err := store.SCIMPatchUser(r.Context(), orgID, userID, body.Operations)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeSCIMError(w, http.StatusNotFound, "User not found.")
				return
			}
			writeSCIMError(w, http.StatusInternalServerError, "Failed to update user.")
			return
		}
		writeSCIMJSON(w, http.StatusOK, recordToSCIM(*updated))
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterSCIMRoutes registers SCIM 2.0 /Users endpoints on the given mux.
// The auth callback resolves a bearer token to an org ID.
func RegisterSCIMRoutes(mux *http.ServeMux, store SCIMUserStore, auth SCIMBearerAuth) {
	if store == nil || auth == nil {
		return
	}
	mux.Handle("GET /api/scim/v2/Users", handleSCIMListUsers(store, auth))
	mux.Handle("POST /api/scim/v2/Users", handleSCIMCreateUser(store, auth))
	mux.Handle("GET /api/scim/v2/Users/{id}", handleSCIMGetUser(store, auth))
	mux.Handle("PATCH /api/scim/v2/Users/{id}", handleSCIMPatchUser(store, auth))
}
