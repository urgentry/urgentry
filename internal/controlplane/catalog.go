package controlplane

import (
	"context"

	sharedstore "urgentry/internal/store"
)

type CatalogStore interface {
	ListOrganizations(ctx context.Context) ([]sharedstore.Organization, error)
	GetOrganization(ctx context.Context, slug string) (*sharedstore.Organization, error)
	UpdateOrganization(ctx context.Context, slug string, update sharedstore.OrganizationUpdate) (*sharedstore.Organization, error)
	ListProjects(ctx context.Context, orgSlug string) ([]sharedstore.Project, error)
	GetProject(ctx context.Context, orgSlug, projectSlug string) (*sharedstore.Project, error)
	ListTeams(ctx context.Context, orgSlug string) ([]sharedstore.Team, error)
	ListProjectKeys(ctx context.Context, orgSlug, projectSlug string) ([]sharedstore.ProjectKeyMeta, error)
	ListAllProjectKeys(ctx context.Context) ([]sharedstore.ProjectKeyMeta, error)
	CreateProject(ctx context.Context, orgSlug, teamSlug string, input sharedstore.ProjectCreateInput) (*sharedstore.Project, error)
	CreateProjectKey(ctx context.Context, orgSlug, projectSlug, label string) (*sharedstore.ProjectKeyMeta, error)
	GetProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string) (*sharedstore.ProjectKeyMeta, error)
	UpdateProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string, update sharedstore.ProjectKeyUpdate) (*sharedstore.ProjectKeyMeta, error)
	DeleteProjectKey(ctx context.Context, orgSlug, projectSlug, keyID string) error
	GetProjectSettings(ctx context.Context, orgSlug, projectSlug string) (*sharedstore.ProjectSettings, error)
	UpdateProjectSettings(ctx context.Context, orgSlug, projectSlug string, update sharedstore.ProjectSettingsUpdate) (*sharedstore.ProjectSettings, error)
	ListOrganizationAuditLogs(ctx context.Context, orgSlug string, limit int) ([]sharedstore.AuditLogEntry, error)
	ListEnvironments(ctx context.Context, orgSlug string) ([]string, error)
	ListProjectEnvironments(ctx context.Context, orgSlug, projectSlug string) ([]sharedstore.ProjectEnvironment, error)
	GetProjectEnvironment(ctx context.Context, orgSlug, projectSlug, envName string) (*sharedstore.ProjectEnvironment, error)
	UpdateProjectEnvironment(ctx context.Context, orgSlug, projectSlug, envName string, isHidden bool) (*sharedstore.ProjectEnvironment, error)
	ListProjectTeams(ctx context.Context, orgSlug, projectSlug string) ([]sharedstore.Team, error)
	AddProjectTeam(ctx context.Context, orgSlug, projectSlug, teamSlug string) (*sharedstore.Team, error)
	RemoveProjectTeam(ctx context.Context, orgSlug, projectSlug, teamSlug string) (bool, error)
	DeleteProject(ctx context.Context, orgSlug, projectSlug string) error
}
