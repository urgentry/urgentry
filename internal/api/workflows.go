package api

import (
	"encoding/json"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// workflowResponse is the JSON shape returned for a single workflow.
type workflowResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Triggers   json.RawMessage `json:"triggers"`
	Conditions json.RawMessage `json:"conditions"`
	Actions    json.RawMessage `json:"actions"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  string          `json:"dateCreated"`
}

func toWorkflowResponse(w *store.Workflow) workflowResponse {
	triggers := json.RawMessage(w.TriggersJSON)
	if !json.Valid(triggers) {
		triggers = json.RawMessage("[]")
	}
	conditions := json.RawMessage(w.ConditionsJSON)
	if !json.Valid(conditions) {
		conditions = json.RawMessage("[]")
	}
	actions := json.RawMessage(w.ActionsJSON)
	if !json.Valid(actions) {
		actions = json.RawMessage("[]")
	}
	return workflowResponse{
		ID:         w.ID,
		Name:       w.Name,
		Triggers:   triggers,
		Conditions: conditions,
		Actions:    actions,
		Enabled:    w.Enabled,
		CreatedAt:  w.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// handleListWorkflows handles GET /api/0/organizations/{org_slug}/workflows/.
func handleListWorkflows(
	catalog controlplane.CatalogStore,
	workflows store.WorkflowStore,
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
		items, err := workflows.ListWorkflows(r.Context(), org.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list workflows.")
			return
		}
		out := make([]workflowResponse, 0, len(items))
		for _, wf := range items {
			out = append(out, toWorkflowResponse(wf))
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

type createWorkflowRequest struct {
	Name       string          `json:"name"`
	Triggers   json.RawMessage `json:"triggers"`
	Conditions json.RawMessage `json:"conditions"`
	Actions    json.RawMessage `json:"actions"`
	Enabled    *bool           `json:"enabled"`
}

// handleCreateWorkflow handles POST /api/0/organizations/{org_slug}/workflows/.
func handleCreateWorkflow(
	catalog controlplane.CatalogStore,
	workflows store.WorkflowStore,
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
		var body createWorkflowRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: name")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		wf := &store.Workflow{
			OrgID:          org.ID,
			Name:           body.Name,
			TriggersJSON:   string(body.Triggers),
			ConditionsJSON: string(body.Conditions),
			ActionsJSON:    string(body.Actions),
			Enabled:        enabled,
		}
		if err := workflows.CreateWorkflow(r.Context(), wf); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create workflow.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, toWorkflowResponse(wf))
	}
}

type bulkWorkflowRequest struct {
	IDs     []string `json:"ids"`
	Enabled *bool    `json:"enabled"`
}

// handleBulkUpdateWorkflows handles PUT /api/0/organizations/{org_slug}/workflows/.
func handleBulkUpdateWorkflows(
	catalog controlplane.CatalogStore,
	workflows store.WorkflowStore,
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
		var body bulkWorkflowRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if len(body.IDs) == 0 {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: ids")
			return
		}
		if body.Enabled == nil {
			httputil.WriteError(w, http.StatusBadRequest, "Missing required field: enabled")
			return
		}
		if err := workflows.BulkUpdateWorkflows(r.Context(), org.ID, body.IDs, *body.Enabled); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update workflows.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleBulkDeleteWorkflows handles DELETE /api/0/organizations/{org_slug}/workflows/.
func handleBulkDeleteWorkflows(
	catalog controlplane.CatalogStore,
	workflows store.WorkflowStore,
	auth authFunc,
) http.HandlerFunc {
	return bulkDeleteByOrgHandler(catalog, auth, "Failed to delete workflows.", func(r *http.Request, orgID string, ids []string) error {
		return workflows.BulkDeleteWorkflows(r.Context(), orgID, ids)
	})
}

// handleGetWorkflow handles GET /api/0/organizations/{org_slug}/workflows/{workflow_id}/.
func handleGetWorkflow(
	workflows store.WorkflowStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		wf, err := workflows.GetWorkflow(r.Context(), PathParam(r, "workflow_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get workflow.")
			return
		}
		if wf == nil {
			httputil.WriteError(w, http.StatusNotFound, "Workflow not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toWorkflowResponse(wf))
	}
}

type updateWorkflowRequest struct {
	Name       string          `json:"name"`
	Triggers   json.RawMessage `json:"triggers"`
	Conditions json.RawMessage `json:"conditions"`
	Actions    json.RawMessage `json:"actions"`
	Enabled    *bool           `json:"enabled"`
}

// handleUpdateWorkflow handles PUT /api/0/organizations/{org_slug}/workflows/{workflow_id}/.
func handleUpdateWorkflow(
	workflows store.WorkflowStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		workflowID := PathParam(r, "workflow_id")
		existing, err := workflows.GetWorkflow(r.Context(), workflowID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to get workflow.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Workflow not found.")
			return
		}
		var body updateWorkflowRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.Name != "" {
			existing.Name = body.Name
		}
		if len(body.Triggers) > 0 {
			existing.TriggersJSON = string(body.Triggers)
		}
		if len(body.Conditions) > 0 {
			existing.ConditionsJSON = string(body.Conditions)
		}
		if len(body.Actions) > 0 {
			existing.ActionsJSON = string(body.Actions)
		}
		if body.Enabled != nil {
			existing.Enabled = *body.Enabled
		}
		if err := workflows.UpdateWorkflow(r.Context(), existing); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update workflow.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, toWorkflowResponse(existing))
	}
}

// handleDeleteWorkflow handles DELETE /api/0/organizations/{org_slug}/workflows/{workflow_id}/.
func handleDeleteWorkflow(
	workflows store.WorkflowStore,
	auth authFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		workflowID := PathParam(r, "workflow_id")
		if workflowID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Missing workflow ID.")
			return
		}
		if err := workflows.DeleteWorkflow(r.Context(), workflowID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete workflow.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
