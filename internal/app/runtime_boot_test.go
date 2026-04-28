package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteBootstrapCredentialsUsesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not portable on Windows")
	}
	state := &runtimeState{dataDir: t.TempDir()}
	path, err := state.writeBootstrapCredentials(&bootstrapResult{
		Email:    "admin@example.com",
		Password: "generated-password",
		PAT:      "gpat_generated",
	})
	if err != nil {
		t.Fatalf("writeBootstrapCredentials: %v", err)
	}
	if path != filepath.Join(state.dataDir, "bootstrap-credentials.txt") {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", got)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials file: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"email=admin@example.com", "password=generated-password", "pat=gpat_generated"} {
		if !strings.Contains(text, want) {
			t.Fatalf("credentials file missing %q: %s", want, text)
		}
	}
}
