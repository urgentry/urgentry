package api

import (
	"database/sql"
	"net/http"
	"strings"

	authpkg "urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func handleReprocessDebugFile(db *sql.DB, debugFiles *sqlite.DebugFileStore, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if debugFiles == nil || native == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Debug file store unavailable.")
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		releaseVersion := PathParam(r, "version")
		debugFileID := strings.TrimSpace(PathParam(r, "debug_file_id"))
		if debugFileID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Debug file ID is required.")
			return
		}

		item, _, err := debugFiles.Get(r.Context(), debugFileID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load debug file.")
			return
		}
		if item == nil || item.ProjectID != projectID || item.ReleaseID != releaseVersion {
			httputil.WriteError(w, http.StatusNotFound, "Debug file not found.")
			return
		}
		if strings.EqualFold(item.Kind, "proguard") {
			httputil.WriteError(w, http.StatusBadRequest, "Native reprocessing is not available for ProGuard mappings.")
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		principal := authpkg.PrincipalFromContext(r.Context())
		run, err := native.CreateRun(r.Context(), sqlite.CreateNativeReprocessRun{
			OrganizationID: org.ID,
			ProjectID:      projectID,
			ReleaseVersion: releaseVersion,
			DebugFileID:    debugFileID,
			Principal:      principal,
			RequestedVia:   "debug_file_reprocess_api",
			RequestPath:    r.URL.Path,
			RequestMethod:  r.Method,
			IPAddress:      r.RemoteAddr,
			UserAgent:      r.UserAgent(),
		})
		if err != nil {
			if sqlite.IsBackfillConflict(err) {
				httputil.WriteError(w, http.StatusConflict, "A conflicting native reprocessing job is already active.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create native reprocessing job.")
			return
		}
		httputil.WriteJSON(w, http.StatusAccepted, mapBackfillRun(*run))
	}
}
