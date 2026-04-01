package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"urgentry/internal/config"
)

func TestRunProfileWritesSummary(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "profile")
	runProfile([]string{
		"--scenario=store-basic-error",
		"--kind=none",
		"--iterations=1",
		"--gomaxprocs=1",
		"--out-dir=" + outDir,
		"--project-id=test-project",
	})

	if _, err := os.Stat(filepath.Join(outDir, "summary.json")); err != nil {
		t.Fatalf("summary.json: %v", err)
	}
}

func TestMainVersionCommand(t *testing.T) {
	if os.Getenv("URGENTRY_MAIN_TEST") == "version" {
		config.Version = "test-version"
		config.Commit = "abc1234"
		config.BuildDate = "2024-01-01T00:00:00Z"
		os.Args = []string{"urgentry", "version"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainVersionCommand")
	cmd.Env = append(os.Environ(), "URGENTRY_MAIN_TEST=version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version command: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "urgentry test-version") {
		t.Fatalf("output = %q, want urgentry test-version", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "commit:") {
		t.Fatalf("output = %q, want commit line", strings.TrimSpace(string(out)))
	}
}

func TestMainUsageCommand(t *testing.T) {
	if os.Getenv("URGENTRY_MAIN_TEST") == "usage" {
		os.Args = []string{"urgentry"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainUsageCommand")
	cmd.Env = append(os.Environ(), "URGENTRY_MAIN_TEST=usage")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected usage subprocess to exit non-zero")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2", exitErr.ExitCode())
	}
	if !strings.Contains(string(out), "usage: urgentry <command>") {
		t.Fatalf("output = %q, want usage text", string(out))
	}
}

func TestServeInvalidRole(t *testing.T) {
	if os.Getenv("URGENTRY_MAIN_TEST") == "serve-invalid" {
		os.Args = []string{"urgentry", "serve", "--role=invalid"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestServeInvalidRole")
	cmd.Env = append(os.Environ(), "URGENTRY_MAIN_TEST=serve-invalid")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected invalid serve subprocess to fail")
	}
	if !strings.Contains(string(out), "invalid role") {
		t.Fatalf("output = %q, want invalid role message", string(out))
	}
}

func TestMainDispatchSubprocess(t *testing.T) {
	if os.Getenv("URGENTRY_MAIN_TEST") != "dispatch" {
		return
	}

	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("URGENTRY_MAIN_TEST_ARGS")), &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	os.Args = append([]string{"urgentry"}, args...)
	main()
}

func TestWriteJSONFormatsIndentedOutput(t *testing.T) {
	restore, read := captureStdout(t)
	writeJSON(struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}{
		Name:  "tiny",
		Count: 2,
	})
	restore()

	out := read()
	want := "{\n  \"name\": \"tiny\",\n  \"count\": 2\n}\n"
	if out != want {
		t.Fatalf("writeJSON output = %q, want %q", out, want)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "tiny", "later"); got != "tiny" {
		t.Fatalf("firstNonEmpty(...) = %q, want tiny", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty(empty) = %q, want empty string", got)
	}
}

func TestMainSelfHostedBackupPlanUsesEnvDefaults(t *testing.T) {
	out, err := runMainDispatch(t, []string{"self-hosted", "backup-plan"}, map[string]string{
		"URGENTRY_TELEMETRY_BACKEND": "timescale",
		"URGENTRY_BLOB_BACKEND":      "file",
		"URGENTRY_ASYNC_BACKEND":     "sqlite",
		"URGENTRY_CACHE_BACKEND":     "sqlite",
	})
	if err != nil {
		t.Fatalf("backup-plan command: %v (%s)", err, out)
	}

	var payload map[string]any
	if err := json.Unmarshal(trimTestProcessOutput(out), &payload); err != nil {
		t.Fatalf("decode backup-plan json: %v\n%s", err, out)
	}
	if payload["telemetryBackend"] != "timescale" {
		t.Fatalf("telemetryBackend = %#v, want timescale", payload["telemetryBackend"])
	}
	if payload["blobBackend"] != "file" {
		t.Fatalf("blobBackend = %#v, want file", payload["blobBackend"])
	}
	if payload["asyncBackend"] != "sqlite" {
		t.Fatalf("asyncBackend = %#v, want sqlite", payload["asyncBackend"])
	}
	if payload["cacheBackend"] != "sqlite" {
		t.Fatalf("cacheBackend = %#v, want sqlite", payload["cacheBackend"])
	}
}

func TestMainSelfHostedRollbackPlanWritesJSON(t *testing.T) {
	out, err := runMainDispatch(t, []string{
		"self-hosted",
		"rollback-plan",
		"--current-control-version=3",
		"--target-control-version=2",
		"--current-telemetry-version=4",
		"--target-telemetry-version=3",
	}, nil)
	if err != nil {
		t.Fatalf("rollback-plan command: %v (%s)", err, out)
	}

	var payload map[string]any
	if err := json.Unmarshal(trimTestProcessOutput(out), &payload); err != nil {
		t.Fatalf("decode rollback-plan json: %v\n%s", err, out)
	}
	if payload["currentControlVersion"] != float64(3) {
		t.Fatalf("currentControlVersion = %#v, want 3", payload["currentControlVersion"])
	}
	if payload["targetControlVersion"] != float64(2) {
		t.Fatalf("targetControlVersion = %#v, want 2", payload["targetControlVersion"])
	}
	if payload["currentTelemetryVersion"] != float64(4) {
		t.Fatalf("currentTelemetryVersion = %#v, want 4", payload["currentTelemetryVersion"])
	}
	if payload["targetTelemetryVersion"] != float64(3) {
		t.Fatalf("targetTelemetryVersion = %#v, want 3", payload["targetTelemetryVersion"])
	}
}

func TestMainSelfHostedUsageAndFailures(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		env           map[string]string
		wantExitCode  int
		wantSubstring string
	}{
		{
			name:          "missing-subcommand",
			args:          []string{"self-hosted"},
			wantExitCode:  2,
			wantSubstring: "usage: urgentry self-hosted <command> [args]",
		},
		{
			name:          "unknown-subcommand",
			args:          []string{"self-hosted", "bogus"},
			wantExitCode:  2,
			wantSubstring: "commands:",
		},
		{
			name:          "invalid-backend",
			args:          []string{"self-hosted", "backup-plan", "--telemetry-backend=bogus"},
			wantExitCode:  1,
			wantSubstring: "invalid telemetry backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runMainDispatch(t, tt.args, tt.env)
			if err == nil {
				t.Fatalf("expected command to fail, output=%s", out)
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("error type = %T, want *exec.ExitError", err)
			}
			if exitErr.ExitCode() != tt.wantExitCode {
				t.Fatalf("exit code = %d, want %d (%s)", exitErr.ExitCode(), tt.wantExitCode, out)
			}
			if !strings.Contains(string(out), tt.wantSubstring) {
				t.Fatalf("output = %q, want substring %q", out, tt.wantSubstring)
			}
		})
	}
}

func runMainDispatch(t *testing.T, args []string, extraEnv map[string]string) ([]byte, error) {
	t.Helper()

	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatchSubprocess")
	cmd.Env = append(os.Environ(),
		"URGENTRY_MAIN_TEST=dispatch",
		"URGENTRY_MAIN_TEST_ARGS="+string(encodedArgs),
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.CombinedOutput()
}

func trimTestProcessOutput(out []byte) []byte {
	if idx := bytes.Index(out, []byte("\nPASS")); idx >= 0 {
		return out[:idx]
	}
	return out
}

func captureStdout(t *testing.T) (func(), func() string) {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer

	restore := func() {
		_ = writer.Close()
		os.Stdout = original
	}
	read := func() string {
		payload, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read stdout: %v", err)
		}
		_ = reader.Close()
		return string(payload)
	}
	return restore, read
}
