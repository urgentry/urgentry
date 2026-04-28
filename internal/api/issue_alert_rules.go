package api

import (
	"net/http"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/pkg/id"
)

// issueAlertRuleRequest is the Sentry-compatible request body for creating/updating
// issue alert rules at /api/0/projects/{org}/{proj}/rules/.
type issueAlertRuleRequest struct {
	Name        string            `json:"name"`
	Conditions  []alert.Condition `json:"conditions"`
	Actions     []alert.Action    `json:"actions"`
	Filters     []alert.Filter    `json:"filters"`
	ActionMatch string            `json:"actionMatch"`
	FilterMatch string            `json:"filterMatch"`
	Frequency   *int              `json:"frequency"`
	Environment *string           `json:"environment"`
	Status      string            `json:"status"`
}

// handleListIssueAlertRules handles GET /api/0/projects/{org_slug}/{proj_slug}/rules/.
func handleListIssueAlertRules(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rules, err := alerts.ListRules(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list alert rules.")
			return
		}
		if rules == nil {
			rules = []*alert.Rule{}
		}
		normalizeIssueAlertDefaults(rules)
		httputil.WriteJSON(w, http.StatusOK, rules)
	}
}

// handleGetIssueAlertRule handles GET /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/.
func handleGetIssueAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		rule, err := alerts.GetRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load alert rule.")
			return
		}
		if rule == nil || rule.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Alert rule not found.")
			return
		}
		normalizeIssueAlertDefault(rule)
		httputil.WriteJSON(w, http.StatusOK, rule)
	}
}

// handleCreateIssueAlertRule handles POST /api/0/projects/{org_slug}/{proj_slug}/rules/.
func handleCreateIssueAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		var body issueAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		now := time.Now().UTC()
		rule := &alert.Rule{
			ID:          id.New(),
			ProjectID:   projectID,
			Name:        strings.TrimSpace(body.Name),
			RuleType:    normalizeMatchType(body.ActionMatch),
			FilterMatch: normalizeMatchType(body.FilterMatch),
			Status:      normalizeAlertStatus(body.Status),
			Conditions:  body.Conditions,
			Actions:     body.Actions,
			Filters:     body.Filters,
			Frequency:   30,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if body.Frequency != nil {
			rule.Frequency = *body.Frequency
		}
		if body.Environment != nil {
			rule.Environment = *body.Environment
		}
		if rule.Conditions == nil {
			rule.Conditions = []alert.Condition{}
		}
		if rule.Actions == nil {
			rule.Actions = []alert.Action{{
				ID: "sentry.rules.actions.notify_event.NotifyEventAction",
			}}
		}
		if rule.Filters == nil {
			rule.Filters = []alert.Filter{}
		}

		if err := alerts.CreateRule(r.Context(), rule); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create alert rule.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, rule)
	}
}

// handleUpdateIssueAlertRule handles PUT /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/.
func handleUpdateIssueAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}

		existing, err := alerts.GetRule(r.Context(), r.PathValue("rule_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load alert rule.")
			return
		}
		if existing == nil || existing.ProjectID != projectID {
			httputil.WriteError(w, http.StatusNotFound, "Alert rule not found.")
			return
		}

		var body issueAlertRuleRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Name is required.")
			return
		}

		existing.Name = strings.TrimSpace(body.Name)
		existing.Status = normalizeAlertStatus(body.Status)
		existing.UpdatedAt = time.Now().UTC()

		if body.ActionMatch != "" {
			existing.RuleType = normalizeMatchType(body.ActionMatch)
		}
		if body.FilterMatch != "" {
			existing.FilterMatch = normalizeMatchType(body.FilterMatch)
		}
		if body.Conditions != nil {
			existing.Conditions = body.Conditions
		}
		if body.Actions != nil {
			existing.Actions = body.Actions
		}
		if body.Filters != nil {
			existing.Filters = body.Filters
		}
		if body.Frequency != nil {
			existing.Frequency = *body.Frequency
		}
		if body.Environment != nil {
			existing.Environment = *body.Environment
		}

		if err := alerts.UpdateRule(r.Context(), existing); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update alert rule.")
			return
		}
		normalizeIssueAlertDefault(existing)
		httputil.WriteJSON(w, http.StatusOK, existing)
	}
}

// handleDeleteIssueAlertRule handles DELETE /api/0/projects/{org_slug}/{proj_slug}/rules/{rule_id}/.
func handleDeleteIssueAlertRule(catalog controlplane.CatalogStore, alerts controlplane.AlertStore, auth authFunc) http.HandlerFunc {
	return deleteAlertRuleHandler(catalog, alerts, auth)
}

// normalizeMatchType normalises actionMatch / filterMatch to a valid value.
func normalizeMatchType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "any":
		return "any"
	case "none":
		return "none"
	default:
		return "all"
	}
}

// normalizeIssueAlertDefaults ensures Sentry-compatible defaults on a slice of rules.
func normalizeIssueAlertDefaults(rules []*alert.Rule) {
	for _, r := range rules {
		normalizeIssueAlertDefault(r)
	}
}

// normalizeIssueAlertDefault fills zero-value fields with Sentry-compatible defaults
// so the JSON response always includes the expected keys.
func normalizeIssueAlertDefault(r *alert.Rule) {
	if r.RuleType == "" {
		r.RuleType = "all"
	}
	if r.FilterMatch == "" {
		r.FilterMatch = "all"
	}
	if r.Conditions == nil {
		r.Conditions = []alert.Condition{}
	}
	if r.Actions == nil {
		r.Actions = []alert.Action{}
	}
	if r.Filters == nil {
		r.Filters = []alert.Filter{}
	}
}
