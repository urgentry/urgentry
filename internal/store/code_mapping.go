package store

import (
	"context"
	"time"
)

// CodeMapping maps stack trace file paths to source code URLs in a repository.
// When a frame's filename starts with StackRoot, the matching prefix is
// replaced with SourceRoot and appended to the DefaultBranch URL pattern.
type CodeMapping struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	StackRoot     string    `json:"stackRoot"`     // prefix in stack frames, e.g. "src/"
	SourceRoot    string    `json:"sourceRoot"`     // prefix in repo, e.g. "app/src/"
	DefaultBranch string    `json:"defaultBranch"`  // e.g. "main"
	RepoURL       string    `json:"repoUrl"`        // e.g. "https://github.com/org/repo"
	CreatedAt     time.Time `json:"createdAt"`
}

// CodeMappingStore persists and retrieves code mapping configurations.
type CodeMappingStore interface {
	CreateCodeMapping(ctx context.Context, m *CodeMapping) error
	ListCodeMappings(ctx context.Context, projectID string) ([]*CodeMapping, error)
	DeleteCodeMapping(ctx context.Context, projectID, id string) error
}
