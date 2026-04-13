package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

// SamplingRuleResponse is the JSON representation of a sampling rule.
type SamplingRuleResponse struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	SampleRate  float64         `json:"sampleRate"`
	Conditions  json.RawMessage `json:"conditions"`
	Active      bool            `json:"active"`
	DateCreated time.Time       `json:"dateCreated"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type samplingRuleRequest struct {
	SampleRate float64         `json:"sample_rate"`
	Conditions json.RawMessage `json:"conditions"`
	Active     *bool           `json:"active"`
}

func handleListSamplingRules(catalog controlplane.CatalogStore, sampling *sqlite.SamplingRuleStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rules, err := sampling.ListSamplingRules(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list sampling rules.")
			return
		}
		resp := make([]SamplingRuleResponse, 0, len(rules))
		for _, rule := range rules {
			resp = append(resp, mapSamplingRule(rule))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateSamplingRule(catalog controlplane.CatalogStore, sampling *sqlite.SamplingRuleStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		var body samplingRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid request body.")
			return
		}
		if body.SampleRate < 0.0 || body.SampleRate > 1.0 {
			httputil.WriteError(w, http.StatusBadRequest, "sample_rate must be between 0.0 and 1.0.")
			return
		}
		active := true
		if body.Active != nil {
			active = *body.Active
		}
		rule := &sqlite.SamplingRule{
			ProjectID:   projectID,
			SampleRate:  body.SampleRate,
			Conditions:  body.Conditions,
			Active:      active,
			DateCreated: time.Now().UTC(),
		}
		created, err := sampling.CreateSamplingRule(r.Context(), rule)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create sampling rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapSamplingRule(*created))
	}
}

func handleDeleteSamplingRule(catalog controlplane.CatalogStore, sampling *sqlite.SamplingRuleStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		ruleID := PathParam(r, "rule_id")
		if strings.TrimSpace(ruleID) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Rule ID is required.")
			return
		}
		existing, err := sampling.GetSamplingRule(r.Context(), ruleID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load sampling rule.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Sampling rule not found.")
			return
		}
		if err := sampling.DeleteSamplingRule(r.Context(), ruleID); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete sampling rule.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func mapSamplingRule(rule sqlite.SamplingRule) SamplingRuleResponse {
	conditions := rule.Conditions
	if len(conditions) == 0 {
		conditions = json.RawMessage("{}")
	}
	return SamplingRuleResponse{
		ID:          rule.ID,
		ProjectID:   rule.ProjectID,
		SampleRate:  rule.SampleRate,
		Conditions:  conditions,
		Active:      rule.Active,
		DateCreated: rule.DateCreated,
		UpdatedAt:   rule.UpdatedAt,
	}
}
