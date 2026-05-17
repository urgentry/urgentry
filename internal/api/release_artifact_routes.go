package api

import (
	"database/sql"
	"net/http"

	"urgentry/internal/attachment"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/proguard"
	"urgentry/internal/sourcemap"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type ReleaseArtifactRoutes struct {
	DB               *sql.DB
	Catalog          controlplane.CatalogStore
	Releases         controlplane.ReleaseStore
	NativeControl    *sqlite.NativeControlStore
	ReleaseHealth    *sqlite.ReleaseHealthStore
	DebugFiles       *sqlite.DebugFileStore
	PreprodArtifacts *sqlite.PreprodArtifactStore
	Attachments      attachment.Store
	ProGuardStore    proguard.Store
	SourceMapStore   sourcemap.Store
	BlobStore        store.BlobStore
	WithAuth         policyAuthFunc
}

func RegisterReleaseArtifactRoutes(mux *http.ServeMux, routes ReleaseArtifactRoutes) {
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/", handleListReleases(routes.Catalog, routes.Releases, routes.NativeControl, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/", handleCreateRelease(routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/", handleGetRelease(routes.DB, routes.Catalog, routes.Releases, routes.NativeControl, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("DELETE /api/0/organizations/{org_slug}/releases/{version}/", handleDeleteRelease(routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("PUT /api/0/organizations/{org_slug}/releases/{version}/", handleUpdateRelease(routes.Catalog, routes.Releases, routes.NativeControl, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/deploys/", handleListReleaseDeploys(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/deploys/", handleCreateReleaseDeploy(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/commits/", handleListReleaseCommits(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/commits/", handleCreateReleaseCommit(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/suspects/", handleListReleaseSuspects(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
	mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/commitfiles/", handleListReleaseCommitFiles(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))

	if smStore, ok := routes.SourceMapStore.(*sqlite.SourceMapStore); ok && smStore != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/files/", handleListReleaseFiles(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("POST /api/0/organizations/{org_slug}/releases/{version}/files/", handleUploadReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleGetReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("PUT /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleUpdateReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("DELETE /api/0/organizations/{org_slug}/releases/{version}/files/{file_id}/", handleDeleteReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	}
	if routes.PreprodArtifacts != nil {
		mux.Handle("GET /api/0/organizations/{org_slug}/preprodartifacts/{artifact_id}/install-details/", handleGetPreprodArtifactInstallDetails(routes.DB, routes.PreprodArtifacts, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
		mux.Handle("GET /api/0/organizations/{org_slug}/preprodartifacts/{artifact_id}/size-analysis/", handleGetPreprodArtifactSizeAnalysis(routes.DB, routes.PreprodArtifacts, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgRead, Resource: auth.ResourceOrganizationPath})))
	}
	if routes.BlobStore != nil {
		mux.Handle("POST /api/0/organizations/{org_slug}/chunk-upload/", handleChunkUpload(routes.BlobStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseWrite, Resource: auth.ResourceOrganizationPath})))
	}

	if routes.ProGuardStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/", handleListProGuardMappings(routes.DB, routes.ProGuardStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/", handleUploadProGuardMapping(routes.DB, routes.ProGuardStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/proguard/{uuid}/", handleLookupProGuardMapping(routes.DB, routes.ProGuardStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/health/", handleGetReleaseHealth(routes.DB, routes.ReleaseHealth, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/sessions/", handleListReleaseSessions(routes.DB, routes.ReleaseHealth, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if routes.BlobStore != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/", handleListDebugFiles(routes.DB, routes.NativeControl, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/", handleUploadDebugFile(routes.DB, routes.DebugFiles, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/", handleDownloadDebugFile(routes.DB, routes.DebugFiles, routes.NativeControl, routes.BlobStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/reprocess/", handleReprocessDebugFile(routes.DB, routes.DebugFiles, routes.NativeControl, routes.WithAuth(auth.Policy{Scope: auth.ScopeOrgAdmin, Resource: auth.ResourceProjectPath})))
	}

	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/source-map-debug/", handleSourceMapDebug(routes.DB, routes.SourceMapStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if routes.Attachments != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/attachments/", handleUploadProjectAttachment(routes.DB, routes.Attachments, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		mux.Handle("GET /api/0/events/{event_id}/attachments/", handleListEventAttachments(routes.Attachments, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceEventPath})))
		mux.Handle("GET /api/0/events/{event_id}/attachments/{attachment_id}/", handleDownloadEventAttachment(routes.Attachments, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceEventPath})))
	}
	if routes.SourceMapStore != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/", handleUploadSourceMap(routes.DB, routes.SourceMapStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
		if smStore, ok := routes.SourceMapStore.(*sqlite.SourceMapStore); ok && smStore != nil {
			mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/", handleListProjectReleaseFiles(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
			mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/{file_id}/", handleGetProjectReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
			mux.Handle("PUT /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/{file_id}/", handleUpdateProjectReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath})))
			mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/{file_id}/", handleDeleteProjectReleaseFile(routes.Catalog, smStore, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath})))
		}
	}
	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/commits/", handleListProjectReleaseCommits(routes.Catalog, routes.Releases, routes.WithAuth(auth.Policy{Scope: auth.ScopeReleaseRead, Resource: auth.ResourceProjectPath})))

	mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/files/dsyms/", handleListDsyms(routes.DB, routes.DebugFiles, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	if routes.DebugFiles != nil {
		mux.Handle("POST /api/0/projects/{org_slug}/{proj_slug}/files/dsyms/", handleUploadDsym(routes.DB, routes.DebugFiles, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
	}
	mux.Handle("DELETE /api/0/projects/{org_slug}/{proj_slug}/files/dsyms/", handleDeleteDsyms(routes.DB, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectArtifactsWrite, Resource: auth.ResourceProjectPath, AllowAutomation: true})))
	if routes.PreprodArtifacts != nil {
		mux.Handle("GET /api/0/projects/{org_slug}/{proj_slug}/preprodartifacts/build-distribution/latest/", handleGetLatestPreprodArtifact(routes.DB, routes.PreprodArtifacts, routes.WithAuth(auth.Policy{Scope: auth.ScopeProjectRead, Resource: auth.ResourceProjectPath})))
	}
}
