package api

import (
	"database/sql"
	"net/http"
	"strings"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
)

// dsymResponse is the Sentry-compatible JSON shape for a project-level debug file.
type dsymResponse struct {
	ID          string `json:"id"`
	DebugID     string `json:"debugId,omitempty"`
	CodeID      string `json:"codeId,omitempty"`
	Name        string `json:"objectName"`
	Kind        string `json:"symbolType,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size"`
	Checksum    string `json:"sha1,omitempty"`
	DateCreated string `json:"dateCreated"`
}

func toDsymResponse(f *sqlite.DebugFile) dsymResponse {
	return dsymResponse{
		ID:          f.ID,
		DebugID:     f.UUID,
		CodeID:      f.CodeID,
		Name:        f.Name,
		Kind:        f.Kind,
		ContentType: f.ContentType,
		Size:        f.Size,
		Checksum:    f.Checksum,
		DateCreated: f.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListDsyms handles GET /api/0/projects/{org}/{proj}/files/dsyms/.
func handleListDsyms(db *sql.DB, debugFiles *sqlite.DebugFileStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if debugFiles == nil {
			httputil.WriteJSON(w, http.StatusOK, []dsymResponse{})
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}

		// List all debug files across all releases for this project.
		rows, err := db.QueryContext(r.Context(),
			`SELECT id, project_id, release_version, kind, uuid, code_id, name, content_type, object_key, size_bytes, checksum, created_at
			 FROM debug_files WHERE project_id = ? ORDER BY created_at DESC LIMIT 100`, projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list debug files.")
			return
		}
		defer rows.Close()

		var files []dsymResponse
		for rows.Next() {
			var f sqlite.DebugFile
			var uuid, codeID, contentType, checksum, createdAt sql.NullString
			if err := rows.Scan(&f.ID, &f.ProjectID, &f.ReleaseID, &f.Kind, &uuid, &codeID, &f.Name, &contentType, &f.ObjectKey, &f.Size, &checksum, &createdAt); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to scan debug files.")
				return
			}
			f.UUID = nullStrVal(uuid)
			f.CodeID = nullStrVal(codeID)
			f.ContentType = nullStrVal(contentType)
			f.Checksum = nullStrVal(checksum)
			if createdAt.Valid {
				f.CreatedAt = sqlutil.ParseDBTime(createdAt.String)
			}
			files = append(files, toDsymResponse(&f))
		}
		if files == nil {
			files = []dsymResponse{}
		}
		httputil.WriteJSON(w, http.StatusOK, files)
	}
}

// handleUploadDsym handles POST /api/0/projects/{org}/{proj}/files/dsyms/.
func handleUploadDsym(db *sql.DB, debugFiles *sqlite.DebugFileStore, auth authFunc) http.HandlerFunc {
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

		data, header, err := readMultipartFile(w, r, "file", maxDebugFileSize)
		if err != nil {
			writeMultipartError(w, err, "Invalid multipart form.")
			return
		}

		name := header.Filename
		if name == "" {
			name = "symbols.bin"
		}

		df := &sqlite.DebugFile{
			ProjectID: projectID,
			ReleaseID: "__project__",
			Kind:      "native",
			Name:      name,
		}
		if err := debugFiles.Save(r.Context(), df, data); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save debug file.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toDsymResponse(df))
	}
}

// handleDeleteDsyms handles DELETE /api/0/projects/{org}/{proj}/files/dsyms/.
func handleDeleteDsyms(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}

		// Accept ?id= query parameter for specific file deletion.
		ids := r.URL.Query()["id"]
		if len(ids) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing id query parameter.")
			return
		}

		for _, fileID := range ids {
			fileID = strings.TrimSpace(fileID)
			if fileID == "" {
				continue
			}
			_, _ = db.ExecContext(r.Context(),
				`DELETE FROM debug_files WHERE id = ? AND project_id = ?`, fileID, projectID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func nullStrVal(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
