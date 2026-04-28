package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compile-time interface check.
var _ BlobStore = (*FileBlobStore)(nil)

// FileBlobStore persists raw payloads to the local filesystem.
// Keys map directly to file paths under the configured directory
// (e.g., ~/.urgentry/blobs/<key>).
type FileBlobStore struct {
	dir string
}

// NewFileBlobStore creates a FileBlobStore rooted at dir, creating
// the directory if it does not exist.
func NewFileBlobStore(dir string) (*FileBlobStore, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(absDir, 0o700)
	return &FileBlobStore{dir: absDir}, nil
}

// safePath validates and resolves a blob key to a filesystem path,
// preventing path traversal attacks.
func (s *FileBlobStore) safePath(key string) (string, error) {
	cleaned := filepath.Clean(key)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid blob key: %q", key)
	}
	full := filepath.Join(s.dir, cleaned)
	// Verify the resolved path is still under s.dir.
	if !strings.HasPrefix(full, s.dir+string(filepath.Separator)) && full != s.dir {
		return "", fmt.Errorf("path traversal attempt: %q", key)
	}
	return full, nil
}

// Put writes data to a file identified by key.
// Intermediate directories are created as needed.
func (s *FileBlobStore) Put(_ context.Context, key string, data []byte) error {
	path, err := s.safePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	return os.WriteFile(path, data, 0o600)
}

// Get reads the full contents of the blob identified by key.
func (s *FileBlobStore) Get(_ context.Context, key string) ([]byte, error) {
	path, err := s.safePath(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

// Delete removes the blob identified by key.
func (s *FileBlobStore) Delete(_ context.Context, key string) error {
	path, err := s.safePath(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
