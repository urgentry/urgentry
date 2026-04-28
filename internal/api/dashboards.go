package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/auth"
	"urgentry/internal/discover"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func handleListDashboards(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		items, err := dashboards.ListDashboards(r.Context(), PathParam(r, "org_slug"), principal.User.ID)
		if err != nil {
			writeDashboardError(w, err, "Failed to list dashboards.")
			return
		}
		resp := make([]Dashboard, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapDashboard(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateDashboard(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body dashboardRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		item, err := dashboards.CreateDashboard(r.Context(), PathParam(r, "org_slug"), principal.User.ID, sqlite.DashboardInput{
			Title:       body.Title,
			Description: body.Description,
			Visibility:  sqlite.DashboardVisibility(body.Visibility),
			Config:      body.Config,
		})
		if err != nil {
			writeDashboardError(w, err, "Failed to create dashboard.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapDashboard(*item))
	}
}

func handleGetDashboard(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		item, err := dashboards.GetDashboard(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), principal.User.ID)
		if err != nil {
			writeDashboardError(w, err, "Failed to load dashboard.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapDashboard(*item))
	}
}

func handleUpdateDashboard(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body dashboardRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		item, err := dashboards.UpdateDashboard(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), principal.User.ID, sqlite.DashboardInput{
			Title:       body.Title,
			Description: body.Description,
			Visibility:  sqlite.DashboardVisibility(body.Visibility),
			Config:      body.Config,
		})
		if err != nil {
			writeDashboardError(w, err, "Failed to update dashboard.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapDashboard(*item))
	}
}

func handleDeleteDashboard(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		if err := dashboards.DeleteDashboard(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), principal.User.ID); err != nil {
			writeDashboardError(w, err, "Failed to delete dashboard.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCreateDashboardWidget(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body dashboardWidgetRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		item, err := dashboards.CreateWidget(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), principal.User.ID, body.toStore())
		if err != nil {
			writeDashboardError(w, err, "Failed to create dashboard widget.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapDashboardWidget(*item))
	}
}

func handleUpdateDashboardWidget(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		var body dashboardWidgetRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		item, err := dashboards.UpdateWidget(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), PathParam(r, "widget_id"), principal.User.ID, body.toStore())
		if err != nil {
			writeDashboardError(w, err, "Failed to update dashboard widget.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapDashboardWidget(*item))
	}
}

func handleDeleteDashboardWidget(dashboards analyticsservice.DashboardStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		principal := requireUserPrincipal(w, r)
		if principal == nil {
			return
		}
		if err := dashboards.DeleteWidget(r.Context(), PathParam(r, "org_slug"), PathParam(r, "dashboard_id"), PathParam(r, "widget_id"), principal.User.ID); err != nil {
			writeDashboardError(w, err, "Failed to delete dashboard widget.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type dashboardRequest struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	Config      json.RawMessage `json:"config"`
}

type dashboardWidgetRequest struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Kind          string          `json:"kind"`
	Position      int             `json:"position"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	SavedSearchID string          `json:"savedSearchId"`
	Query         discover.Query  `json:"query"`
	Config        json.RawMessage `json:"config"`
}

func (r dashboardWidgetRequest) toStore() sqlite.DashboardWidgetInput {
	return sqlite.DashboardWidgetInput{
		Title:         r.Title,
		Description:   r.Description,
		Kind:          sqlite.DashboardWidgetKind(r.Kind),
		Position:      r.Position,
		Width:         r.Width,
		Height:        r.Height,
		SavedSearchID: r.SavedSearchID,
		QueryDoc:      r.Query,
		Config:        r.Config,
	}
}

func requireUserPrincipal(w http.ResponseWriter, r *http.Request) *auth.Principal {
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.User == nil {
		httputil.WriteError(w, http.StatusForbidden, "Interactive or PAT user credential required.")
		return nil
	}
	return principal
}

func writeDashboardError(w http.ResponseWriter, err error, fallback string) {
	var validationErrs discover.ValidationErrors
	switch {
	case errors.Is(err, sqlite.ErrDashboardForbidden):
		httputil.WriteError(w, http.StatusForbidden, "You do not have permission to perform this action.")
	case errors.Is(err, sqlite.ErrDashboardNotFound):
		httputil.WriteError(w, http.StatusNotFound, "Dashboard not found.")
	case errors.As(err, &validationErrs):
		httputil.WriteError(w, http.StatusBadRequest, validationErrs.Error())
	case err != nil && isDashboardBadRequest(err):
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httputil.WriteError(w, http.StatusInternalServerError, fallback)
	}
}

func isDashboardBadRequest(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"required",
		"unsupported widget kind",
		"saved search not found",
		"scope must match organization",
		"outside the dashboard organization",
		"series widgets require",
		"do not support rollup queries",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
