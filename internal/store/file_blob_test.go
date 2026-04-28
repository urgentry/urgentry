package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileBlobStoreCreatesOwnerOnlyDirectoriesAndFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not portable on Windows")
	}
	root := filepath.Join(t.TempDir(), "blobs")
	blobs, err := NewFileBlobStore(root)
	if err != nil {
		t.Fatalf("NewFileBlobStore: %v", err)
	}
	if err := blobs.Put(context.Background(), "project/event/payload.json", []byte("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "project"), 0o700)
	assertMode(t, filepath.Join(root, "project", "event"), 0o700)
	assertMode(t, filepath.Join(root, "project", "event", "payload.json"), 0o600)
}

func TestFileBlobStoreSafePathRejectsEscapes(t *testing.T) {
	blobs, err := NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileBlobStore: %v", err)
	}
	for _, key := range []string{"../escape", "/absolute", "nested/../../escape"} {
		if _, err := blobs.safePath(key); err == nil {
			t.Fatalf("safePath(%q) accepted an escaping key", key)
		}
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}
