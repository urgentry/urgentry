package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	blobstore "urgentry/internal/blob"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

const maxDebugFileSize = 128 << 20 // 128 MB

type updateProjectSettingsRequest struct {
	Name                    string                                 `json:"name"`
	Platform                string                                 `json:"platform"`
	Status                  string                                 `json:"status"`
	EventRetentionDays      int                                    `json:"eventRetentionDays"`
	AttachmentRetentionDays int                                    `json:"attachmentRetentionDays"`
	DebugFileRetentionDays  int                                    `json:"debugFileRetentionDays"`
	TelemetryPolicies       []sharedstore.TelemetryRetentionPolicy `json:"telemetryPolicies"`
	ReplayPolicy            sharedstore.ReplayIngestPolicy         `json:"replayPolicy"`
}

func handleGetProjectSettings(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, err := catalog.GetProjectSettings(r.Context(), PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project settings.")
			return
		}
		if project == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, project)
	}
}

func handleUpdateProjectSettings(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body updateProjectSettingsRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}
		status := strings.TrimSpace(body.Status)
		if status == "" {
			status = "active"
		}
		if status != "active" && status != "disabled" {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid status.")
			return
		}
		if body.EventRetentionDays <= 0 {
			body.EventRetentionDays = 90
		}
		if body.AttachmentRetentionDays <= 0 {
			body.AttachmentRetentionDays = 30
		}
		if body.DebugFileRetentionDays <= 0 {
			body.DebugFileRetentionDays = 180
		}
		policies, err := sharedstore.CanonicalTelemetryPolicies(body.TelemetryPolicies, body.EventRetentionDays, body.AttachmentRetentionDays, body.DebugFileRetentionDays)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		project, err := catalog.UpdateProjectSettings(r.Context(), PathParam(r, "org_slug"), PathParam(r, "proj_slug"), sharedstore.ProjectSettingsUpdate{
			Name:                    strings.TrimSpace(body.Name),
			Platform:                strings.TrimSpace(body.Platform),
			Status:                  status,
			EventRetentionDays:      body.EventRetentionDays,
			AttachmentRetentionDays: body.AttachmentRetentionDays,
			DebugFileRetentionDays:  body.DebugFileRetentionDays,
			TelemetryPolicies:       policies,
			ReplayPolicy:            body.ReplayPolicy,
		})
		if err != nil {
			if sharedstore.IsInvalidReplayPolicy(err) {
				httputil.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update project settings.")
			return
		}
		if project == nil {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, project)
	}
}

func handleListAuditLogs(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		items, err := catalog.ListOrganizationAuditLogs(r.Context(), PathParam(r, "org_slug"), limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list audit logs.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleGetReleaseHealth(db *sql.DB, releaseHealth *sqlite.ReleaseHealthStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		summary, err := releaseHealth.GetReleaseHealth(r.Context(), projectID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release health.")
			return
		}
		resp := ReleaseHealth{
			ProjectID:        summary.ProjectID,
			Version:          summary.ReleaseVersion,
			SessionCount:     summary.SessionCount,
			ErroredSessions:  summary.ErroredSessions,
			CrashedSessions:  summary.CrashedSessions,
			AbnormalSessions: summary.AbnormalSessions,
			AffectedUsers:    summary.AffectedUsers,
			CrashFreeRate:    summary.CrashFreeRate,
		}
		if !summary.LastSessionAt.IsZero() {
			resp.LastSessionSeenAt = &summary.LastSessionAt
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleListReleaseSessions(db *sql.DB, releaseHealth *sqlite.ReleaseHealthStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		rows, err := releaseHealth.ListReleaseSessions(r.Context(), projectID, PathParam(r, "version"), limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release sessions.")
			return
		}
		items := make([]ReleaseSession, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapReleaseSession(row))
		}
		httputil.WriteJSON(w, http.StatusOK, items)
	}
}

func handleListDebugFiles(db *sql.DB, native *sqlite.NativeControlStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if native == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Debug file store unavailable.")
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		items, err := native.ListReleaseDebugFiles(r.Context(), org.ID, projectID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list debug files.")
			return
		}
		resp := make([]DebugFile, 0, len(items))
		for _, item := range items {
			if item.File == nil {
				continue
			}
			if kind != "" && item.File.Kind != kind {
				continue
			}
			resp = append(resp, mapDebugFile(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleUploadDebugFile(db *sql.DB, debugFiles *sqlite.DebugFileStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if debugFiles == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Debug file store unavailable.")
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		body, header, err := readMultipartFile(w, r, "file", maxDebugFileSize)
		if err != nil {
			writeMultipartError(w, err, "Invalid multipart form.")
			return
		}
		kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
		if kind == "" {
			kind = "native"
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = header.Filename
		}
		if name == "" {
			name = "symbols.bin"
		}
		debugID := strings.TrimSpace(r.FormValue("debug_id"))
		if debugID == "" {
			debugID = strings.TrimSpace(r.FormValue("uuid"))
		}
		contentType := strings.TrimSpace(r.FormValue("content_type"))
		if contentType == "" {
			contentType = header.Header.Get("Content-Type")
		}

		item := &sqlite.DebugFile{
			ProjectID:    projectID,
			ReleaseID:    PathParam(r, "version"),
			Kind:         kind,
			Name:         name,
			UUID:         debugID,
			CodeID:       strings.TrimSpace(r.FormValue("code_id")),
			BuildID:      strings.TrimSpace(r.FormValue("build_id")),
			ModuleName:   strings.TrimSpace(r.FormValue("module")),
			Architecture: strings.TrimSpace(r.FormValue("architecture")),
			Platform:     strings.TrimSpace(r.FormValue("platform")),
			ContentType:  contentType,
			Checksum:     strings.TrimSpace(r.FormValue("checksum")),
			CreatedAt:    time.Now().UTC(),
		}
		if err := debugFiles.Save(r.Context(), item, body); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save debug file.")
			return
		}
		status, err := debugFiles.SymbolicationStatus(r.Context(), item)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to classify debug file.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapDebugFile(sqlite.DebugFileProcessing{
			File:                item,
			SymbolicationStatus: status,
		}))
	}
}

func handleDownloadDebugFile(db *sql.DB, debugFiles *sqlite.DebugFileStore, native *sqlite.NativeControlStore, blobs sharedstore.BlobStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if blobs == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Debug file store unavailable.")
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		releaseVersion := PathParam(r, "version")
		identifier := PathParam(r, "debug_file_id")
		kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
		item, body, err := debugFiles.Get(r.Context(), identifier)
		directIDMatch := item != nil
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load debug file.")
			return
		}
		if item == nil {
			item, body, err = debugFiles.LookupByDebugID(r.Context(), projectID, releaseVersion, kind, identifier)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to load debug file.")
				return
			}
		}
		if item == nil {
			item, body, err = debugFiles.LookupByCodeID(r.Context(), projectID, releaseVersion, kind, identifier)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to load debug file.")
				return
			}
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Debug file not found.")
			return
		}
		if item.ProjectID != projectID || item.ReleaseID != releaseVersion || item.Kind == "proguard" || (kind != "" && item.Kind != kind) {
			httputil.WriteError(w, http.StatusNotFound, "Debug file not found.")
			return
		}
		if body == nil {
			body, _ = blobstore.NewResolver(db, blobs).Read(r.Context(), blobstore.DebugFile(projectID, item.ID, item.ObjectKey))
		}
		if !directIDMatch || body == nil {
			org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
			if err != nil || org == nil {
				httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
				return
			}
			statuses, err := native.ListReleaseDebugFiles(r.Context(), org.ID, projectID, releaseVersion)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to load debug file.")
				return
			}
			for _, status := range statuses {
				if status.File != nil && status.File.ID == item.ID {
					httputil.WriteJSON(w, http.StatusOK, mapDebugFile(status))
					return
				}
			}
			httputil.WriteJSON(w, http.StatusOK, mapDebugFile(sqlite.DebugFileProcessing{File: item}))
			return
		}
		contentType := item.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeAttachmentFilename(item.Name)+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func mapDebugFile(item sqlite.DebugFileProcessing) DebugFile {
	file := item.File
	if file == nil {
		return DebugFile{}
	}
	var dateReprocessed *time.Time
	if !item.ReprocessUpdatedAt.IsZero() {
		value := item.ReprocessUpdatedAt
		dateReprocessed = &value
	}
	return DebugFile{
		ID:                  file.ID,
		ProjectID:           file.ProjectID,
		ReleaseID:           file.ReleaseID,
		Kind:                file.Kind,
		Name:                file.Name,
		DebugID:             file.UUID,
		CodeID:              file.CodeID,
		SymbolicationStatus: item.SymbolicationStatus,
		ReprocessRunID:      item.ReprocessRunID,
		ReprocessStatus:     item.ReprocessStatus,
		ReprocessLastError:  item.ReprocessLastError,
		DateReprocessed:     dateReprocessed,
		ContentType:         file.ContentType,
		Size:                file.Size,
		Checksum:            file.Checksum,
		DateCreated:         file.CreatedAt,
	}
}

func mapReleaseSession(item sqlite.ReleaseSession) ReleaseSession {
	resp := ReleaseSession{
		ID:          item.ID,
		ProjectID:   item.ProjectID,
		Version:     item.Release,
		Environment: item.Environment,
		SessionID:   item.SessionID,
		DistinctID:  item.DistinctID,
		Status:      item.Status,
		Errors:      item.Errors,
		Duration:    item.Duration,
		UserAgent:   item.UserAgent,
		Attrs:       item.Attrs,
		Quantity:    item.Quantity,
		DateCreated: item.DateCreated,
	}
	if !item.StartedAt.IsZero() {
		resp.StartedAt = &item.StartedAt
	}
	return resp
}
