package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/integration"
	"urgentry/internal/sqlite"
	"urgentry/internal/telemetryquery"
)

type IssueRoutes struct {
	DB             *sql.DB
	Catalog        controlplane.CatalogStore
	IssueReads     controlplane.IssueReadStore
	Issues         controlplane.IssueWorkflowStore
	Hooks          *sqlite.HookStore
	Autofix        *sqlite.AutofixStore
	ExternalIssues integration.ExternalIssueStore
	Queries        telemetryquery.Service
	QueryGuard     sqlite.QueryGuard
	WithAuth       policyAuthFunc
}

func RegisterIssueRoutes(mux *http.ServeMux, routes IssueRoutes) {
	// Org issue queries and org-scoped issue actions.
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/", handleListOrganizationIssues(routes.Catalog, routes.Queries, routes.QueryGuard, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgQueryRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/issues/", handleBulkMutateOrgIssues(routes.DB, routes.IssueReads, routes.Issues, routes.Hooks, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/issues/", handleBulkDeleteOrgIssues(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/events/{event_id}/", handleGetIssueEvent(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath}), handleGetIssue(routes.DB, routes.IssueReads, routes.Issues, allowAllAuth)))
	mux.Handle("PUT /api/0/organizations/{org_slug}/issues/{issue_id}/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath}), handleUpdateIssue(routes.DB, routes.IssueReads, routes.Issues, routes.Hooks, allowAllAuth)))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/issues/{issue_id}/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath}), handleDeleteIssue(routes.Issues, allowAllAuth)))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/events/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath}), handleListIssueEvents(routes.DB, allowAllAuth)))
	if routes.Autofix != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/autofix/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath}), handleGetIssueAutofix(routes.Autofix, allowAllAuth)))
		mux.Handle("POST /api/0/organizations/{org_slug}/issues/{issue_id}/autofix/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceOrganizationPath}), handleStartIssueAutofix(routes.DB, routes.Autofix, allowAllAuth)))
	}
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/hashes/", handleListIssueHashes(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/", handleGetIssueTagDetail(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/tags/{key}/values/", handleListIssueTagValues(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath})))
	if routes.ExternalIssues != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/issues/{issue_id}/external-issues/", withOrgIssueScope(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceOrganizationPath}), handleListExternalIssues(routes.ExternalIssues, allowAllAuth)))
	}

	// Project issue collection actions.
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/issues/", handleListProjectIssues(routes.DB, routes.Catalog, routes.IssueReads, routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/issues/", handleBulkMutateProjectIssues(routes.Catalog, routes.DB, routes.IssueReads, routes.Issues, routes.Hooks, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceProjectPath})))
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/issues/", handleBulkDeleteProjectIssues(routes.Catalog, routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceProjectPath})))

	// Global issue detail aliases.
	mux.Handle("GET /api/0/issues/{issue_id}/", handleGetIssue(routes.DB, routes.IssueReads, routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("PUT /api/0/issues/{issue_id}/", handleUpdateIssue(routes.DB, routes.IssueReads, routes.Issues, routes.Hooks, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("DELETE /api/0/issues/{issue_id}/", handleDeleteIssue(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/events/", handleListIssueEvents(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/events/latest/", handleGetLatestIssueEvent(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/comments/", handleListIssueComments(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/comments/", handleCreateIssueComment(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("GET /api/0/issues/{issue_id}/activity/", handleListIssueActivity(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/merge/", handleMergeIssue(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
	mux.Handle("POST /api/0/issues/{issue_id}/unmerge/", handleUnmergeIssue(routes.Issues, routes.WithAuth(auth.Policy{Scope: auth.ScopeIssueWrite, Resource: auth.ResourceIssuePath})))
}
