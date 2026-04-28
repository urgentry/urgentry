package api

import (
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// externalUserResponse is the JSON shape returned for a single external user mapping.
type externalUserResponse struct {
	ID           string `json:"id"`
	UserID       string `json:"userId"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
	CreatedAt    string `json:"dateCreated"`
}

func toExternalUserResponse(u *store.ExternalUser) externalUserResponse {
	return externalUserResponse{
		ID:           u.ID,
		UserID:       u.UserID,
		Provider:     u.Provider,
		ExternalID:   u.ExternalID,
		ExternalName: u.ExternalName,
		CreatedAt:    u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

type createExternalUserRequest struct {
	UserID       string `json:"userId"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
}

// handleCreateExternalUser handles POST /api/0/organizations/{org_slug}/external-users/.
func handleCreateExternalUser(
	catalog controlplane.CatalogStore,
	externalUsers store.ExternalUserStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body createExternalUserRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.Provider == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: provider")
			return
		}
		u := &store.ExternalUser{
			OrgID:        org.ID,
			UserID:       body.UserID,
			Provider:     body.Provider,
			ExternalID:   body.ExternalID,
			ExternalName: body.ExternalName,
		}
		if err := externalUsers.CreateExternalUser(r.Context(), u); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create external user mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toExternalUserResponse(u))
	}
}

type updateExternalUserRequest struct {
	UserID       string `json:"userId"`
	Provider     string `json:"provider"`
	ExternalID   string `json:"externalId"`
	ExternalName string `json:"externalName"`
}

// handleUpdateExternalUser handles PUT /api/0/organizations/{org_slug}/external-users/{id}/.
func handleUpdateExternalUser(
	externalUsers store.ExternalUserStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extUserID := PathParam(r, "id")
		if extUserID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing external user ID.")
			return
		}
		var body updateExternalUserRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		u := &store.ExternalUser{
			ID:           extUserID,
			UserID:       body.UserID,
			Provider:     body.Provider,
			ExternalID:   body.ExternalID,
			ExternalName: body.ExternalName,
		}
		if err := externalUsers.UpdateExternalUser(r.Context(), u); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update external user mapping.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toExternalUserResponse(u))
	}
}

// handleDeleteExternalUser handles DELETE /api/0/organizations/{org_slug}/external-users/{id}/.
func handleDeleteExternalUser(
	externalUsers store.ExternalUserStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		extUserID := PathParam(r, "id")
		if extUserID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing external user ID.")
			return
		}
		if err := externalUsers.DeleteExternalUser(r.Context(), extUserID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete external user mapping.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
