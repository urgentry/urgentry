package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func handleListOutcomes(db *sql.DB, outcomes *sqlite.OutcomeStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		items, err := outcomes.ListRecent(r.Context(), projectID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list outcomes.")
			return
		}
		resp := make([]Outcome, 0, len(items))
		for _, item := range items {
			resp = append(resp, Outcome{
				ID:          item.ID,
				ProjectID:   item.ProjectID,
				EventID:     item.EventID,
				Category:    item.Category,
				Reason:      item.Reason,
				Quantity:    item.Quantity,
				Source:      item.Source,
				Release:     item.Release,
				Environment: item.Environment,
				Payload:     item.PayloadJSON,
				RecordedAt:  item.RecordedAt,
				DateCreated: item.DateCreated,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}
