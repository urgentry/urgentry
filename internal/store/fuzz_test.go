package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzFileBlobStoreSafePath(f *testing.F) {
	for _, seed := range []string{"project/event.json", "../escape", "/absolute", `windows\path`, ""} {
		f.Add(seed)
	}
	root := f.TempDir()
	blobs, err := NewFileBlobStore(root)
	if err != nil {
		f.Fatalf("NewFileBlobStore: %v", err)
	}
	f.Fuzz(func(t *testing.T, key string) {
		path, err := blobs.safePath(key)
		if err != nil {
			return
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("Rel: %v", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("safePath(%q) escaped root: %s", key, path)
		}
	})
}
