package api

import (
	"database/sql"
	"io"
	"net/http"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sourcemap"
	"urgentry/pkg/id"
)

const maxSourceMapSize = 10 << 20 // 10 MB

// handleUploadSourceMap handles POST /api/0/projects/{org}/{proj}/releases/{version}/files/.
// Accepts multipart file uploads of source map files.
func handleUploadSourceMap(db *sql.DB, smStore sourcemap.Store, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		version := PathParam(r, "version")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}

		if err := r.ParseMultipartForm(maxSourceMapSize); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid multipart form: "+err.Error())
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Missing 'file' field in multipart form.")
			return
		}
		defer file.Close()

		data, err := io.ReadAll(io.LimitReader(file, maxSourceMapSize))
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Failed to read file.")
			return
		}

		// Use the provided name or fall back to the upload filename.
		name := r.FormValue("name")
		if name == "" {
			name = header.Filename
		}

		artifact := &sourcemap.Artifact{
			ID:        id.New(),
			ProjectID: projectID,
			ReleaseID: version,
			Name:      name,
			Size:      int64(len(data)),
			CreatedAt: time.Now().UTC(),
		}

		if err := smStore.SaveArtifact(r.Context(), artifact, data); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save source map.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, artifact)
	}
}
