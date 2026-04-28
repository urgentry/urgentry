package api

import (
	"encoding/json"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// detectorResponse is the JSON shape returned for a single detector.
type detectorResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	State     string          `json:"state"`
	OwnerID   string          `json:"ownerId,omitempty"`
	CreatedAt string          `json:"dateCreated"`
}

func toDetectorResponse(d *store.Detector) detectorResponse {
	cfg := json.RawMessage(d.ConfigJSON)
	if !json.Valid(cfg) {
		cfg = json.RawMessage("{}")
	}
	return detectorResponse{
		ID:        d.ID,
		Name:      d.Name,
		Type:      d.Type,
		Config:    cfg,
		State:     d.State,
		OwnerID:   d.OwnerID,
		CreatedAt: d.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListDetectors handles GET /api/0/organizations/{org_slug}/detectors/.
func handleListDetectors(
	catalog controlplane.CatalogStore,
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		items, err := detectors.ListDetectors(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list detectors.")
			return
		}
		out := make([]detectorResponse, 0, len(items))
		for _, d := range items {
			out = append(out, toDetectorResponse(d))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

type createDetectorRequest struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	OwnerID string          `json:"ownerId"`
}

// handleCreateDetector handles POST /api/0/organizations/{org_slug}/detectors/.
func handleCreateDetector(
	catalog controlplane.CatalogStore,
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body createDetectorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: name")
			return
		}
		if body.Type == "" {
			body.Type = "metric"
		}
		cfgStr := "{}"
		if len(body.Config) > 0 {
			cfgStr = string(body.Config)
		}
		d := &store.Detector{
			OrgID:      org.ID,
			Name:       body.Name,
			Type:       body.Type,
			ConfigJSON: cfgStr,
			OwnerID:    body.OwnerID,
		}
		if err := detectors.CreateDetector(r.Context(), d); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create detector.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toDetectorResponse(d))
	}
}

type bulkDetectorRequest struct {
	IDs   []string `json:"ids"`
	State string   `json:"state"`
}

// handleBulkUpdateDetectors handles PUT /api/0/organizations/{org_slug}/detectors/.
func handleBulkUpdateDetectors(
	catalog controlplane.CatalogStore,
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body bulkDetectorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if len(body.IDs) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: ids")
			return
		}
		if body.State == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: state")
			return
		}
		if err := detectors.BulkUpdateDetectors(r.Context(), org.ID, body.IDs, body.State); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update detectors.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleBulkDeleteDetectors handles DELETE /api/0/organizations/{org_slug}/detectors/.
func handleBulkDeleteDetectors(
	catalog controlplane.CatalogStore,
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return bulkDeleteByOrgHandler(catalog, auth, "Failed to delete detectors.", func(r *http.Request, orgID string, ids []string) error {
		return detectors.BulkDeleteDetectors(r.Context(), orgID, ids)
	})
}

// bulkDeleteByOrgHandler is a shared handler factory for bulk-delete endpoints that
// decode a JSON body with an "ids" field and call deleteFn with the org ID and IDs.
func bulkDeleteByOrgHandler(
	catalog controlplane.CatalogStore,
	auth authFunc,
	deleteErrMsg string,
	deleteFn func(r *http.Request, orgID string, ids []string) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if len(body.IDs) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: ids")
			return
		}
		if err := deleteFn(r, org.ID, body.IDs); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, deleteErrMsg)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetDetector handles GET /api/0/organizations/{org_slug}/detectors/{detector_id}/.
func handleGetDetector(
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		d, err := detectors.GetDetector(r.Context(), PathParam(r, "detector_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get detector.")
			return
		}
		if d == nil {
			httputil.WriteError(w, http.StatusNotFound, "Detector not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toDetectorResponse(d))
	}
}

type updateDetectorRequest struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	State   string          `json:"state"`
	OwnerID string          `json:"ownerId"`
}

// handleUpdateDetector handles PUT /api/0/organizations/{org_slug}/detectors/{detector_id}/.
func handleUpdateDetector(
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		detectorID := PathParam(r, "detector_id")
		existing, err := detectors.GetDetector(r.Context(), detectorID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get detector.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Detector not found.")
			return
		}
		var body updateDetectorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if body.Name != "" {
			existing.Name = body.Name
		}
		if body.Type != "" {
			existing.Type = body.Type
		}
		if len(body.Config) > 0 {
			existing.ConfigJSON = string(body.Config)
		}
		if body.State != "" {
			existing.State = body.State
		}
		if body.OwnerID != "" {
			existing.OwnerID = body.OwnerID
		}
		if err := detectors.UpdateDetector(r.Context(), existing); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update detector.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toDetectorResponse(existing))
	}
}

// handleDeleteDetector handles DELETE /api/0/organizations/{org_slug}/detectors/{detector_id}/.
func handleDeleteDetector(
	detectors store.DetectorStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		detectorID := PathParam(r, "detector_id")
		if detectorID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing detector ID.")
			return
		}
		if err := detectors.DeleteDetector(r.Context(), detectorID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete detector.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
