package api

import (
	"net/http"
	"strconv"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// feedbackResponse is the Sentry-compatible JSON shape for user feedback.
type feedbackResponse struct {
	ID          string `json:"id"`
	EventID     string `json:"eventId"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Comments    string `json:"comments"`
	DateCreated string `json:"dateCreated"`
}

func toFeedbackResponse(f *sqlite.Feedback) feedbackResponse {
	return feedbackResponse{
		ID:          f.ID,
		EventID:     f.EventID,
		Name:        f.Name,
		Email:       f.Email,
		Comments:    f.Comments,
		DateCreated: f.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListUserFeedback handles GET /api/0/projects/{org}/{proj}/user-feedback/.
func handleListUserFeedback(
	catalog controlplane.CatalogStore,
	feedbackStore *sqlite.FeedbackStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if feedbackStore == nil {
			httputil.WriteJSON(w, http.StatusOK, []feedbackResponse{})
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		limit := 25
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				limit = n
			}
		}

		items, err := feedbackStore.ListFeedback(r.Context(), project.ID, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list user feedback.")
			return
		}

		out := make([]feedbackResponse, 0, len(items))
		for i := range items {
			out = append(out, toFeedbackResponse(&items[i]))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// submitFeedbackRequest is the JSON body for submitting feedback.
type submitFeedbackRequest struct {
	EventID  string `json:"event_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Comments string `json:"comments"`
}

// handleSubmitUserFeedback handles POST /api/0/projects/{org}/{proj}/user-feedback/.
func handleSubmitUserFeedback(
	catalog controlplane.CatalogStore,
	feedbackStore *sqlite.FeedbackStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if feedbackStore == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Feedback store unavailable.")
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}

		var body submitFeedbackRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.EventID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: event_id")
			return
		}

		if err := feedbackStore.SaveFeedback(r.Context(), project.ID, body.EventID, body.Name, body.Email, body.Comments); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save feedback.")
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}
