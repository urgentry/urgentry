package api

import (
	"database/sql"
	"net/http"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
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

		data, header, err := readMultipartFile(w, r, "file", maxSourceMapSize)
		if err != nil {
			writeMultipartError(w, err, "Invalid multipart form.")
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

// handleSourceMapDebug handles GET /api/0/projects/{org}/{proj}/events/{event_id}/source-map-debug/.
// Returns debug info about source map resolution for an event.
func handleSourceMapDebug(db *sql.DB, smStore sourcemap.Store, auth authFunc) http.HandlerFunc {
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

		evt, err := sqlite.GetProjectEvent(r.Context(), db, projectID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}

		resp := SourceMapDebugResponse{
			EventID: eventID,
		}

		// Determine the release from the event tags.
		release := evt.Tags["release"]
		resp.Release = release

		if release == "" {
			resp.Errors = append(resp.Errors, SourceMapDebugError{
				Type:    "no_release",
				Message: "Event does not have a release tag. Source maps require a release to resolve.",
			})
			httputil.WriteJSON(w, http.StatusOK, resp)
			return
		}

		resp.HasRelease = true

		// Check if any source map artifacts exist for this release.
		if smStore != nil {
			artifacts, err := smStore.ListByRelease(r.Context(), projectID, release)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to query source maps.")
				return
			}
			if len(artifacts) == 0 {
				resp.Errors = append(resp.Errors, SourceMapDebugError{
					Type:    "no_sourcemaps",
					Message: "No source map artifacts found for release " + release + ".",
				})
			}
		} else {
			resp.Errors = append(resp.Errors, SourceMapDebugError{
				Type:    "sourcemap_store_unavailable",
				Message: "Source map store is not configured.",
			})
		}

		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}
