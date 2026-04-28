package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	attachmentstore "urgentry/internal/attachment"
	"urgentry/internal/httputil"
)

const maxAttachmentUploadSize = 32 << 20 // 32 MB

func handleUploadProjectAttachment(db *sql.DB, store attachmentstore.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if store == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Attachment store unavailable.")
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}
		data, header, err := readMultipartFile(w, r, "file", maxAttachmentUploadSize)
		if err != nil {
			writeMultipartError(w, err, "Invalid multipart form.")
			return
		}
		eventID := strings.TrimSpace(r.FormValue("event_id"))
		if eventID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "event_id is required.")
			return
		}
		if !eventBelongsToProject(r, db, projectID, eventID) {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = header.Filename
		}
		if name == "" {
			name = "attachment.bin"
		}
		contentType := strings.TrimSpace(r.FormValue("content_type"))
		if contentType == "" {
			contentType = header.Header.Get("Content-Type")
		}

		item := &attachmentstore.Attachment{
			EventID:     eventID,
			ProjectID:   projectID,
			Name:        name,
			ContentType: contentType,
			CreatedAt:   time.Now().UTC(),
		}
		if err := store.SaveAttachment(r.Context(), item, data); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save attachment.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, Attachment{
			ID:          item.ID,
			EventID:     item.EventID,
			ProjectID:   item.ProjectID,
			Name:        item.Name,
			ContentType: item.ContentType,
			Size:        item.Size,
			DateCreated: item.CreatedAt,
		})
	}
}

func handleListEventAttachments(store attachmentstore.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if store == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Attachment store unavailable.")
			return
		}

		eventID := PathParam(r, "event_id")
		attachments, err := store.ListByEvent(r.Context(), eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list attachments.")
			return
		}

		resp := make([]*Attachment, 0, len(attachments))
		for _, a := range attachments {
			resp = append(resp, &Attachment{
				ID:          a.ID,
				EventID:     a.EventID,
				ProjectID:   a.ProjectID,
				Name:        a.Name,
				ContentType: a.ContentType,
				Size:        a.Size,
				DateCreated: a.CreatedAt,
			})
		}

		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleDownloadEventAttachment(store attachmentstore.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if store == nil {
			httputil.WriteError(w, http.StatusServiceUnavailable, "Attachment store unavailable.")
			return
		}

		eventID := PathParam(r, "event_id")
		attachmentID := PathParam(r, "attachment_id")

		a, data, err := store.GetAttachment(r.Context(), attachmentID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load attachment.")
			return
		}
		if a == nil || a.EventID != eventID {
			httputil.WriteError(w, http.StatusNotFound, "Attachment not found.")
			return
		}
		if data == nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Attachment payload unavailable.")
			return
		}

		contentType := a.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(data)), 10))
		w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeAttachmentFilename(a.Name)+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

func sanitizeAttachmentFilename(name string) string {
	if name == "" {
		return "attachment.bin"
	}
	sanitized := make([]rune, 0, len(name))
	for _, r := range name {
		switch r {
		case '"', '\n', '\r':
			continue
		default:
			sanitized = append(sanitized, r)
		}
	}
	if len(sanitized) == 0 {
		return "attachment.bin"
	}
	return string(sanitized)
}

func eventBelongsToProject(r *http.Request, db *sql.DB, projectID, eventID string) bool {
	var found int
	err := db.QueryRowContext(r.Context(),
		`SELECT 1 FROM events WHERE project_id = ? AND event_id = ?`,
		projectID, eventID,
	).Scan(&found)
	return err == nil && found == 1
}
