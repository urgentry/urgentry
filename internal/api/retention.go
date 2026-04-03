package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

type retentionActionRequest struct {
	Limit int `json:"limit"`
}

type RetentionArchiveEntry struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Surface     string `json:"surface"`
	RecordType  string `json:"recordType"`
	RecordID    string `json:"recordId"`
	ArchiveKey  string `json:"archiveKey,omitempty"`
	ArchivedAt  string `json:"archivedAt"`
	RestoredAt  string `json:"restoredAt,omitempty"`
	BlobPresent bool   `json:"blobPresent"`
}

type RetentionExecution struct {
	ProjectID            string                  `json:"projectId"`
	Surface              string                  `json:"surface"`
	StorageTier          string                  `json:"storageTier"`
	RetentionDays        int                     `json:"retentionDays"`
	ArchiveRetentionDays int                     `json:"archiveRetentionDays"`
	Archived             int64                   `json:"archived"`
	Deleted              int64                   `json:"deleted"`
	Restored             int64                   `json:"restored"`
	Archives             []RetentionArchiveEntry `json:"archives"`
}

func handleListRetentionArchives(db *sql.DB, retention *sqlite.RetentionStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		projectID, surface, ok := resolveRetentionTarget(w, r, db)
		if !ok {
			return
		}
		limit := positiveLimit(r.URL.Query().Get("limit"), 50, 200)
		entries, err := retention.ListSurfaceArchives(r.Context(), projectID, surface, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list retention archives.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapRetentionArchives(entries))
	}
}

func handleExecuteRetentionArchive(db *sql.DB, retention *sqlite.RetentionStore, audits *sqlite.AuditStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		projectID, surface, ok := resolveRetentionTarget(w, r, db)
		if !ok {
			return
		}
		var body retentionActionRequest
		if r.ContentLength > 0 {
			if err := decodeJSON(r, &body); err != nil {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
				return
			}
		}
		execution, err := retention.ExecuteSurface(r.Context(), projectID, surface)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httputil.WriteError(w, http.StatusNotFound, "Project not found.")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to execute retention archive.")
			return
		}
		entries, err := retention.ListSurfaceArchives(r.Context(), projectID, surface, normalizeRetentionLimit(body.Limit))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load retention archive outcomes.")
			return
		}
		recordRetentionAudit(r, audits, projectID, "retention."+string(surface)+".archive")
		httputil.WriteJSON(w, http.StatusOK, mapRetentionExecution(execution, 0, entries))
	}
}

func handleExecuteRetentionRestore(db *sql.DB, retention *sqlite.RetentionStore, audits *sqlite.AuditStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		projectID, surface, ok := resolveRetentionTarget(w, r, db)
		if !ok {
			return
		}
		var body retentionActionRequest
		if r.ContentLength > 0 {
			if err := decodeJSON(r, &body); err != nil {
				httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
				return
			}
		}
		limit := normalizeRetentionLimit(body.Limit)
		restored, err := retention.RestoreSurface(r.Context(), projectID, surface, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to restore retention archives.")
			return
		}
		entries, err := retention.ListSurfaceArchives(r.Context(), projectID, surface, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load restored archive outcomes.")
			return
		}
		recordRetentionAudit(r, audits, projectID, "retention."+string(surface)+".restore")
		httputil.WriteJSON(w, http.StatusOK, mapRetentionExecution(&sqlite.RetentionSurfaceExecution{
			ProjectID: projectID,
			Surface:   surface,
		}, restored, entries))
	}
}

func resolveRetentionTarget(w http.ResponseWriter, r *http.Request, db *sql.DB) (string, sharedstore.TelemetrySurface, bool) {
	projectID, err := projectIDFromSlugs(r, db, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
		return "", "", false
	}
	if projectID == "" {
		httputil.WriteError(w, http.StatusNotFound, "Project not found.")
		return "", "", false
	}
	surface, ok := parseTelemetrySurface(PathParam(r, "surface"))
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "Unsupported telemetry surface.")
		return "", "", false
	}
	return projectID, surface, true
}

func parseTelemetrySurface(raw string) (sharedstore.TelemetrySurface, bool) {
	surface := sharedstore.TelemetrySurface(strings.ToLower(strings.TrimSpace(raw)))
	for _, item := range sharedstore.TelemetrySurfaces() {
		if item == surface {
			return surface, true
		}
	}
	return "", false
}

func mapRetentionExecution(execution *sqlite.RetentionSurfaceExecution, restored int64, entries []sqlite.RetentionArchiveEntry) RetentionExecution {
	if execution == nil {
		return RetentionExecution{
			Restored: restored,
			Archives: mapRetentionArchives(entries),
		}
	}
	return RetentionExecution{
		ProjectID:            execution.ProjectID,
		Surface:              string(execution.Surface),
		StorageTier:          string(execution.StorageTier),
		RetentionDays:        execution.RetentionDays,
		ArchiveRetentionDays: execution.ArchiveRetentionDays,
		Archived:             execution.Archived,
		Deleted:              execution.Deleted,
		Restored:             restored,
		Archives:             mapRetentionArchives(entries),
	}
}

func mapRetentionArchives(entries []sqlite.RetentionArchiveEntry) []RetentionArchiveEntry {
	out := make([]RetentionArchiveEntry, 0, len(entries))
	for _, item := range entries {
		entry := RetentionArchiveEntry{
			ID:          item.ID,
			ProjectID:   item.ProjectID,
			Surface:     string(item.Surface),
			RecordType:  item.RecordType,
			RecordID:    item.RecordID,
			ArchiveKey:  item.ArchiveKey,
			ArchivedAt:  formatAPITime(item.ArchivedAt),
			BlobPresent: item.BlobPresent,
		}
		if !item.RestoredAt.IsZero() {
			entry.RestoredAt = formatAPITime(item.RestoredAt)
		}
		out = append(out, entry)
	}
	return out
}

func normalizeRetentionLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func positiveLimit(raw string, fallback, maxVal int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	if parsed > maxVal {
		return maxVal
	}
	return parsed
}

func recordRetentionAudit(r *http.Request, audits *sqlite.AuditStore, projectID, action string) {
	principal := auth.PrincipalFromContext(r.Context())
	_ = audits.Record(r.Context(), sqlite.AuditRecord{
		CredentialType: credentialKind(principal),
		CredentialID:   credentialID(principal),
		UserID:         userID(principal),
		ProjectID:      projectID,
		Action:         action,
		RequestPath:    r.URL.Path,
		RequestMethod:  r.Method,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
}

func formatAPITime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
