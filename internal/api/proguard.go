package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/proguard"
	"urgentry/pkg/id"
)

const maxProGuardSize = 10 << 20 // 10 MB

// handleListProGuardMappings handles GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/.
func handleListProGuardMappings(db *sql.DB, pgStore proguard.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if pgStore == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "ProGuard store unavailable.")
			return
		}

		projectID, ok := resolveProjectIDForMapping(r, db)
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		mappings, err := pgStore.ListByRelease(r.Context(), projectID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list ProGuard mappings.")
			return
		}
		if mappings == nil {
			mappings = []*proguard.Mapping{}
		}
		httputil.WriteJSON(w, http.StatusOK, mappings)
	}
}

// handleLookupProGuardMapping handles GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/{uuid}/.
func handleLookupProGuardMapping(db *sql.DB, pgStore proguard.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if pgStore == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "ProGuard store unavailable.")
			return
		}

		projectID, ok := resolveProjectIDForMapping(r, db)
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		uuid := strings.TrimSpace(PathParam(r, "uuid"))
		if uuid == "" {
			httputil.WriteError(w, http.StatusBadRequest, "UUID is required.")
			return
		}

		mapping, _, err := pgStore.LookupByUUID(r.Context(), projectID, PathParam(r, "version"), uuid)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load ProGuard mapping.")
			return
		}
		if mapping == nil {
			httputil.WriteError(w, http.StatusNotFound, "ProGuard mapping not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapping)
	}
}

// handleUploadProGuardMapping handles POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/.
func handleUploadProGuardMapping(db *sql.DB, pgStore proguard.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if pgStore == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "ProGuard store unavailable.")
			return
		}

		projectID, ok := resolveProjectIDForMapping(r, db)
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		data, header, err := readMultipartFile(w, r, "file", maxProGuardSize)
		if err != nil {
			writeMultipartError(w, err, "Invalid multipart form.")
			return
		}

		uuid := strings.TrimSpace(r.FormValue("uuid"))
		if uuid == "" {
			uuid = strings.TrimSpace(r.FormValue("debug_id"))
		}
		if uuid == "" {
			httputil.WriteError(w, http.StatusBadRequest, "UUID is required.")
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = header.Filename
		}
		if name == "" {
			name = "mapping.txt"
		}

		mapping := &proguard.Mapping{
			ID:        id.New(),
			ProjectID: projectID,
			ReleaseID: PathParam(r, "version"),
			Name:      name,
			UUID:      uuid,
			CodeID:    strings.TrimSpace(r.FormValue("code_id")),
			Checksum:  strings.TrimSpace(r.FormValue("checksum")),
			Size:      int64(len(data)),
			CreatedAt: time.Now().UTC(),
		}

		if err := pgStore.SaveMapping(r.Context(), mapping, data); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save ProGuard mapping.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, mapping)
	}
}

func resolveProjectIDForMapping(r *http.Request, db *sql.DB) (string, bool) {
	org := PathParam(r, "org_slug")
	proj := PathParam(r, "proj_slug")
	projectID, err := projectIDFromSlugs(r, db, org, proj)
	if err == nil && projectID != "" {
		return projectID, true
	}
	return "", false
}
