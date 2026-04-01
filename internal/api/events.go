package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// handleListProjectEvents handles GET /api/0/projects/{org_slug}/{proj_slug}/events/.
func handleListProjectEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		events, err := listRecentEventsFromDB(r, db, projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list events.")
			return
		}
		page := Paginate(w, r, events)
		if page == nil {
			page = []*Event{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetProjectEvent handles GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/.
func handleGetProjectEvent(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		eventID := PathParam(r, "event_id")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		evt, err := getEventFromDB(r, db, projectID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, evt)
	}
}

// ---------------------------------------------------------------------------
// SQLite query helpers for events
// ---------------------------------------------------------------------------

func listRecentEventsFromDB(r *http.Request, db *sql.DB, projectID string, limit int) ([]*Event, error) {
	rows, err := sqlite.ListProjectEvents(r.Context(), db, projectID, limit)
	if err != nil {
		return nil, err
	}
	return apiEventsFromWebEvents(rows), nil
}

func getEventFromDB(r *http.Request, db *sql.DB, projectID, eventID string) (*Event, error) {
	row, err := sqlite.GetProjectEvent(r.Context(), db, projectID, eventID)
	if err == sql.ErrNoRows || row == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return apiEventFromWebEvent(*row), nil
}
