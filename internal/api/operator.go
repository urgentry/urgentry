package api

import (
	"net/http"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

func handleGetOperatorOverview(operators store.OperatorStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if operators == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Operator overview unavailable.")
			return
		}
		overview, err := operators.Overview(r.Context(), PathParam(r, "org_slug"), 8)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load operator overview.")
			return
		}
		if overview == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, overview)
	}
}

func handleGetOperatorDiagnostics(operators store.OperatorStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if operators == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Operator diagnostics unavailable.")
			return
		}
		overview, err := operators.Overview(r.Context(), PathParam(r, "org_slug"), 8)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to build operator diagnostics.")
			return
		}
		if overview == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, store.BuildOperatorDiagnosticsBundle(overview, time.Now().UTC()))
	}
}
