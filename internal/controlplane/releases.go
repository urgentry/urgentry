package controlplane

import (
	"context"

	sharedstore "urgentry/internal/store"
)

type ReleaseStore interface {
	CreateRelease(ctx context.Context, orgSlug, version string) (*Release, error)
	ListReleases(ctx context.Context, orgID string, limit int) ([]Release, error)
	GetRelease(ctx context.Context, orgID, version string) (*Release, error)
	GetReleaseBySlug(ctx context.Context, orgSlug, version string) (*Release, error)
	AddDeploy(ctx context.Context, orgSlug, version string, deploy sharedstore.ReleaseDeploy) (*sharedstore.ReleaseDeploy, error)
	ListDeploys(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseDeploy, error)
	AddCommit(ctx context.Context, orgSlug, version string, commit sharedstore.ReleaseCommit) (*sharedstore.ReleaseCommit, error)
	ListCommits(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseCommit, error)
	ListSuspects(ctx context.Context, orgSlug, version string, limit int) ([]sharedstore.ReleaseSuspect, error)
}
