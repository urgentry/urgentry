package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// NotificationAction is the Sentry-compatible API response shape.
type NotificationAction struct {
	ID               string    `json:"id"`
	ServiceType      string    `json:"serviceType"`
	TargetIdentifier string    `json:"targetIdentifier"`
	TargetDisplay    string    `json:"targetDisplay"`
	TriggerType      string    `json:"triggerType"`
	DateCreated      time.Time `json:"dateCreated"`
}

type notificationActionRequest struct {
	ServiceType      string `json:"serviceType"`
	TargetIdentifier string `json:"targetIdentifier"`
	TargetDisplay    string `json:"targetDisplay"`
	TriggerType      string `json:"triggerType"`
}

func handleListNotificationActions(catalog controlplane.CatalogStore, actions *sqlite.NotificationActionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		items, err := actions.List(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list notification actions.")
			return
		}
		resp := make([]NotificationAction, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapNotificationAction(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateNotificationAction(catalog controlplane.CatalogStore, actions *sqlite.NotificationActionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body notificationActionRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		serviceType := strings.TrimSpace(body.ServiceType)
		if serviceType == "" {
			serviceType = "email"
		}
		triggerType := strings.TrimSpace(body.TriggerType)
		if triggerType == "" {
			triggerType = "spike-protection"
		}
		item, err := actions.Create(
			r.Context(),
			org.ID,
			serviceType,
			strings.TrimSpace(body.TargetIdentifier),
			strings.TrimSpace(body.TargetDisplay),
			triggerType,
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create notification action.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapNotificationAction(*item))
	}
}

func handleGetNotificationAction(catalog controlplane.CatalogStore, actions *sqlite.NotificationActionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		actionID := PathParam(r, "action_id")
		item, err := actions.Get(r.Context(), org.ID, actionID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load notification action.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Notification action not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapNotificationAction(*item))
	}
}

func handleUpdateNotificationAction(catalog controlplane.CatalogStore, actions *sqlite.NotificationActionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body notificationActionRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		actionID := PathParam(r, "action_id")
		serviceType := strings.TrimSpace(body.ServiceType)
		if serviceType == "" {
			serviceType = "email"
		}
		triggerType := strings.TrimSpace(body.TriggerType)
		if triggerType == "" {
			triggerType = "spike-protection"
		}
		item, err := actions.Update(
			r.Context(),
			org.ID,
			actionID,
			serviceType,
			strings.TrimSpace(body.TargetIdentifier),
			strings.TrimSpace(body.TargetDisplay),
			triggerType,
		)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update notification action.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Notification action not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapNotificationAction(*item))
	}
}

func handleDeleteNotificationAction(catalog controlplane.CatalogStore, actions *sqlite.NotificationActionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		actionID := PathParam(r, "action_id")
		if err := actions.Delete(r.Context(), org.ID, actionID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete notification action.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func mapNotificationAction(item sqlite.NotificationAction) NotificationAction {
	return NotificationAction{
		ID:               item.ID,
		ServiceType:      item.ServiceType,
		TargetIdentifier: item.TargetIdentifier,
		TargetDisplay:    item.TargetDisplay,
		TriggerType:      item.TriggerType,
		DateCreated:      item.CreatedAt,
	}
}
