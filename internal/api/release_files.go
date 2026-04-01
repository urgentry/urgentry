package api

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/pkg/id"
)

// releaseFileResponse matches the Sentry release file response shape.
type releaseFileResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Dist        *string           `json:"dist"`
	Headers     map[string]string `json:"headers"`
	Size        int64             `json:"size"`
	SHA1        string            `json:"sha1"`
	DateCreated time.Time         `json:"dateCreated"`
}

func artifactToFileResponse(a *sourcemap.Artifact) *releaseFileResponse {
	var dist *string
	if a.Dist != "" {
		dist = &a.Dist
	}
	return &releaseFileResponse{
		ID:          a.ID,
		Name:        a.Name,
		Dist:        dist,
		Headers:     map[string]string{"Content-Type": "application/octet-stream"},
		Size:        a.Size,
		SHA1:        a.Checksum,
		DateCreated: a.CreatedAt,
	}
}

// handleListReleaseFiles handles GET /api/0/organizations/{org_slug}/releases/{version}/files/.
func handleListReleaseFiles(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}

		files, err := smStore.ListByOrgRelease(r.Context(), orgRecord.ID, PathParam(r, "version"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list release files.")
			return
		}
		result := make([]*releaseFileResponse, 0, len(files))
		for _, f := range files {
			result = append(result, artifactToFileResponse(f))
		}
		page := Paginate(w, r, result)
		if page == nil {
			page = []*releaseFileResponse{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleUploadReleaseFile handles POST /api/0/organizations/{org_slug}/releases/{version}/files/.
func handleUploadReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}
		version := PathParam(r, "version")

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

		name := r.FormValue("name")
		if name == "" {
			name = header.Filename
		}

		hash := sha1.Sum(data)
		checksum := hex.EncodeToString(hash[:])

		artifact := &sourcemap.Artifact{
			ID:             id.New(),
			OrganizationID: orgRecord.ID,
			ReleaseID:      version,
			Name:           name,
			Size:           int64(len(data)),
			Checksum:       checksum,
			CreatedAt:      time.Now().UTC(),
		}

		if err := smStore.SaveOrgArtifact(r.Context(), artifact, data); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to save release file.")
			return
		}

		httputil.WriteJSON(w, http.StatusCreated, artifactToFileResponse(artifact))
	}
}

// handleGetReleaseFile handles GET /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/.
func handleGetReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}

		art, err := smStore.GetOrgArtifact(r.Context(), orgRecord.ID, PathParam(r, "version"), PathParam(r, "file_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load release file.")
			return
		}
		if art == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release file not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, artifactToFileResponse(art))
	}
}

// updateReleaseFileRequest is the JSON body for updating a release file.
type updateReleaseFileRequest struct {
	Name string `json:"name"`
}

// handleUpdateReleaseFile handles PUT /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/.
func handleUpdateReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}

		var body updateReleaseFileRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		art, err := smStore.UpdateArtifactName(r.Context(), orgRecord.ID, PathParam(r, "version"), PathParam(r, "file_id"), body.Name)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update release file.")
			return
		}
		if art == nil {
			httputil.WriteError(w, http.StatusNotFound, "Release file not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, artifactToFileResponse(art))
	}
}

// handleDeleteReleaseFile handles DELETE /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/.
func handleDeleteReleaseFile(catalog controlplane.CatalogStore, smStore *sqlite.SourceMapStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		orgRecord, ok := getOrganizationFromCatalog(w, r, catalog, org)
		if !ok {
			return
		}

		if err := smStore.DeleteOrgArtifact(r.Context(), orgRecord.ID, PathParam(r, "version"), PathParam(r, "file_id")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete release file.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
