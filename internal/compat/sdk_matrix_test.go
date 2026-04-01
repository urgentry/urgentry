//go:build integration

package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	stdhttputil "net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	sdkMatrixProjectID = "1"

	nodeSDKVersion   = "10.46.0"
	pythonSDKVersion = "2.56.0"
	goSDKVersion     = "v0.44.1"
	javaSDKVersion   = "8.16.0"
	rubySDKVersion   = "5.28.1"

	sdkRunnerTimeout = 4 * time.Minute
)

type sdkRunner struct {
	name     string
	version  string
	required bool // required runtimes must be present and pass; skipping one is a failure
	run      func(context.Context, *testing.T, string, string) (string, error)
}

type sdkMatrixEntry struct {
	name     string
	ok       bool
	skipped  bool
	required bool
	detail   string
}

type compatAPIEvent struct {
	ID       string `json:"id"`
	EventID  string `json:"eventID"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Platform string `json:"platform"`
}

func runScoredHarnessCheck(t *testing.T, dimension, name string, threshold float64, fn func(t *testing.T) (float64, string)) bool {
	t.Helper()

	score := 0.0
	detail := "compatibility check failed"
	ok := t.Run(name, func(t *testing.T) {
		score, detail = fn(t)
	})
	writeHarnessResult(t, dimension, name, score, detail)
	return ok && score >= threshold
}

func checkSDKMatrixCore(t *testing.T) (float64, string) {
	t.Helper()
	return runSDKMatrix(t, []sdkRunner{
		{name: "js-node", version: nodeSDKVersion, required: true, run: runNodeSDK},
		{name: "python", version: pythonSDKVersion, required: true, run: runPythonSDK},
		{name: "go", version: goSDKVersion, required: true, run: runGoSDK},
	})
}

func checkSDKMatrixExtended(t *testing.T) (float64, string) {
	t.Helper()
	if os.Getenv("URGENTRY_SDK_MATRIX_HERMETIC") == "1" {
		t.Skip("hermetic mode: skipping extended SDK matrix (live ecosystem installs)")
	}
	return runSDKMatrix(t, []sdkRunner{
		{name: "java", version: javaSDKVersion, run: runJavaSDK},
		{name: "dotnet", version: "latest", run: runDotNetSDK},
		{name: "ruby", version: rubySDKVersion, run: runRubySDK},
	})
}

func runSDKMatrix(t *testing.T, runners []sdkRunner) (float64, string) {
	t.Helper()

	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 600})
	defer srv.close()

	proxy := newSDKIngestProxy(t, srv.server.URL)
	defer proxy.Close()

	dsn := sdkDSN(t, proxy.URL, srv.projectKey, sdkMatrixProjectID)
	results := make([]sdkMatrixEntry, 0, len(runners))

	for _, runner := range runners {
		runner := runner
		result := sdkMatrixEntry{
			name:     runner.name,
			required: runner.required,
			detail:   fmt.Sprintf("fail (%s): runner aborted before recording a result", runner.version),
		}
		ok := t.Run(runner.name, func(t *testing.T) {
			marker := fmt.Sprintf("compat-sdk-%s-%d", runner.name, time.Now().UnixNano())
			ctx, cancel := context.WithTimeout(context.Background(), sdkRunnerTimeout)
			defer cancel()

			detail, err := runner.run(ctx, t, dsn, marker)
			if err != nil {
				if isUnavailableRuntime(err) {
					result = sdkMatrixEntry{
						name:     runner.name,
						skipped:  true,
						required: runner.required,
						detail:   fmt.Sprintf("skip (%s): %s", runner.version, err),
					}
					t.Skip(err)
					return
				}
				result = sdkMatrixEntry{
					name:   runner.name,
					detail: fmt.Sprintf("fail (%s): %s", runner.version, err),
				}
				return
			}

			evt, err := waitForSDKEvent(t, srv, marker)
			if err != nil {
				result = sdkMatrixEntry{
					name:   runner.name,
					detail: fmt.Sprintf("fail (%s): %s", runner.version, err),
				}
				return
			}

			entryDetail := fmt.Sprintf("ok (%s) event=%s platform=%s", runner.version, evt.EventID, evt.Platform)
			if detail != "" {
				entryDetail += " " + detail
			}
			result = sdkMatrixEntry{
				name:   runner.name,
				ok:     true,
				detail: entryDetail,
			}
		})
		if !ok && result.ok {
			result.ok = false
			result.detail = fmt.Sprintf("fail (%s): sdk runner subtest failed", runner.version)
		}
		results = append(results, result)
	}

	score, detail := summarizeSDKMatrix(results)
	t.Log(detail)
	return score, detail
}

func summarizeSDKMatrix(results []sdkMatrixEntry) (float64, string) {
	if len(results) == 0 {
		return 0, "0/0 live SDK runners passed"
	}

	passed := 0
	attempted := 0
	skipped := 0
	requiredMissing := 0
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if result.skipped {
			skipped++
			if result.required {
				requiredMissing++
				parts = append(parts, fmt.Sprintf("%s=REQUIRED_MISSING(%s)", result.name, result.detail))
			} else {
				parts = append(parts, fmt.Sprintf("%s=%s", result.name, result.detail))
			}
			continue
		}
		attempted++
		if result.ok {
			passed++
		}
		parts = append(parts, fmt.Sprintf("%s=%s", result.name, result.detail))
	}
	if attempted == 0 {
		return 0, fmt.Sprintf("0/0 live SDK runners attempted (%d skipped, %d required missing); %s", skipped, requiredMissing, strings.Join(parts, "; "))
	}

	// Required runtimes that were skipped count as failures.
	effectivePassed := passed
	effectiveTotal := attempted + requiredMissing
	score := roundSDKScore(float64(effectivePassed) / float64(effectiveTotal))
	detail := fmt.Sprintf("%d/%d live SDK runners passed (%d skipped", passed, attempted, skipped)
	if requiredMissing > 0 {
		detail += fmt.Sprintf(", %d required missing — install them or the matrix will fail", requiredMissing)
	}
	detail += "); " + strings.Join(parts, "; ")
	return score, detail
}

func roundSDKScore(value float64) float64 {
	return math.Round(value*100) / 100
}

func isUnavailableRuntime(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), "runtime unavailable:")
}

func newSDKIngestProxy(t *testing.T, target string) *httptest.Server {
	t.Helper()

	targetURL, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse proxy target: %v", err)
	}

	proxy := &stdhttputil.ReverseProxy{
		Rewrite: func(r *stdhttputil.ProxyRequest) {
			r.SetURL(targetURL)
			r.Out.URL.Path = rewriteSDKProjectPath(r.In.URL.Path)
			r.Out.URL.RawPath = r.Out.URL.Path
			r.Out.Host = targetURL.Host
		},
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "sdk ingest proxy: "+err.Error(), http.StatusBadGateway)
	}

	return httptest.NewServer(proxy)
}

func rewriteSDKProjectPath(rawPath string) string {
	const prefix = "/api/" + sdkMatrixProjectID + "/"
	if strings.HasPrefix(rawPath, prefix) {
		return strings.Replace(rawPath, prefix, "/api/default-project/", 1)
	}
	return rawPath
}

func sdkDSN(t *testing.T, baseURL, publicKey, projectID string) string {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse sdk dsn base: %v", err)
	}
	parsed.User = url.User(publicKey)
	parsed.Path = "/" + strings.TrimPrefix(path.Join(parsed.Path, projectID), "/")
	return parsed.String()
}

func waitForSDKEvent(t *testing.T, srv *compatServer, marker string) (*compatAPIEvent, error) {
	t.Helper()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		resp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/events/", srv.pat, nil, "")
		if resp.StatusCode != http.StatusOK {
			body := readResponseBody(resp)
			return nil, fmt.Errorf("list events status=%d body=%s", resp.StatusCode, truncateSDKOutput(body))
		}

		var events []compatAPIEvent
		err := json.NewDecoder(resp.Body).Decode(&events)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode events: %w", err)
		}

		for _, evt := range events {
			if strings.Contains(evt.Title, marker) || strings.Contains(evt.Message, marker) {
				return &evt, nil
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	return nil, fmt.Errorf("event marker %q not observed through project events API", marker)
}

func runNodeSDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		return "", fmt.Errorf("runtime unavailable: node not found")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return "", fmt.Errorf("runtime unavailable: npm not found")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fmt.Sprintf(`{
  "name": "urgentry-compat-node",
  "private": true,
  "dependencies": {
    "@sentry/node": "%s"
  }
}
`, nodeSDKVersion)), 0o644); err != nil {
		return "", fmt.Errorf("write package.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(`const Sentry = require("@sentry/node");

async function main() {
  Sentry.init({
    dsn: process.env.URGENTRY_DSN,
    sendClientReports: false,
  });
  Sentry.captureMessage(process.env.URGENTRY_MARKER);
  await Sentry.close(5000);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
`), 0o644); err != nil {
		return "", fmt.Errorf("write index.js: %w", err)
	}

	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "npm", "install", "--ignore-scripts", "--no-package-lock", "--quiet"); err != nil {
		return "", fmt.Errorf("npm install: %w", err)
	}
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "node", "index.js"); err != nil {
		return "", fmt.Errorf("node run: %w", err)
	}
	return "", nil
}

func runPythonSDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("python3"); err != nil {
		return "", fmt.Errorf("runtime unavailable: python3 not found")
	}

	dir := t.TempDir()
	siteDir := filepath.Join(dir, "site")
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(`import os

import sentry_sdk

sentry_sdk.init(
    dsn=os.environ["URGENTRY_DSN"],
    send_client_reports=False,
    _experiments={"transport_compression_level": 0},
)
sentry_sdk.capture_message(os.environ["URGENTRY_MARKER"])
sentry_sdk.flush(timeout=5.0)
`), 0o644); err != nil {
		return "", fmt.Errorf("write main.py: %w", err)
	}

	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "python3", "-m", "pip", "install", "--disable-pip-version-check", "--quiet", "--target", siteDir, "sentry-sdk=="+pythonSDKVersion); err != nil {
		return "", fmt.Errorf("pip install: %w", err)
	}
	env := append(sdkEnv(dsn, marker), "PYTHONPATH="+siteDir)
	if _, err := runSDKCommand(ctx, dir, env, "python3", "main.py"); err != nil {
		return "", fmt.Errorf("python run: %w", err)
	}
	return "", nil
}

func runGoSDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		return "", fmt.Errorf("runtime unavailable: go not found")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf(`module urgentrycompatrunner

go 1.25.0

require github.com/getsentry/sentry-go %s
`, goSDKVersion)), 0o644); err != nil {
		return "", fmt.Errorf("write go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"log"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

func main() {
	if err := sentry.Init(sentry.ClientOptions{Dsn: os.Getenv("URGENTRY_DSN")}); err != nil {
		log.Fatal(err)
	}
	sentry.CaptureMessage(os.Getenv("URGENTRY_MARKER"))
	sentry.Flush(5 * time.Second)
}
`), 0o644); err != nil {
		return "", fmt.Errorf("write main.go: %w", err)
	}

	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "go", "mod", "tidy"); err != nil {
		return "", fmt.Errorf("go mod tidy: %w", err)
	}
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "go", "run", "."); err != nil {
		return "", fmt.Errorf("go run: %w", err)
	}
	return "", nil
}

func runJavaSDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("java"); err != nil {
		return "", fmt.Errorf("runtime unavailable: java not found")
	}
	if _, err := exec.LookPath("javac"); err != nil {
		return "", fmt.Errorf("runtime unavailable: javac not found")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		return "", fmt.Errorf("runtime unavailable: curl not found")
	}

	dir := t.TempDir()
	jarPath := filepath.Join(dir, "sentry.jar")
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "curl", "-fsSL", "https://repo1.maven.org/maven2/io/sentry/sentry/"+javaSDKVersion+"/sentry-"+javaSDKVersion+".jar", "-o", jarPath); err != nil {
		return "", fmt.Errorf("download sentry-java jar: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CompatMatrixJava.java"), []byte(`import io.sentry.Sentry;

public final class CompatMatrixJava {
  public static void main(String[] args) {
    Sentry.init(options -> options.setDsn(System.getenv("URGENTRY_DSN")));
    Sentry.captureMessage(System.getenv("URGENTRY_MARKER"));
    Sentry.flush(5000);
    Sentry.close();
  }
}
`), 0o644); err != nil {
		return "", fmt.Errorf("write CompatMatrixJava.java: %w", err)
	}

	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "javac", "-cp", jarPath, "CompatMatrixJava.java"); err != nil {
		return "", fmt.Errorf("javac: %w", err)
	}
	classpath := jarPath + string(os.PathListSeparator) + dir
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "java", "-cp", classpath, "CompatMatrixJava"); err != nil {
		return "", fmt.Errorf("java run: %w", err)
	}
	return "", nil
}

func runDotNetSDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("dotnet"); err != nil {
		return "", fmt.Errorf("runtime unavailable: dotnet not found")
	}

	dir := t.TempDir()
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "dotnet", "new", "console", "--force"); err != nil {
		return "", fmt.Errorf("dotnet new console: %w", err)
	}
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "dotnet", "add", "package", "Sentry"); err != nil {
		return "", fmt.Errorf("dotnet add package: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Program.cs"), []byte(`using System;
using Sentry;

using (SentrySdk.Init(o => {
    o.Dsn = Environment.GetEnvironmentVariable("URGENTRY_DSN");
}))
{
    SentrySdk.CaptureMessage(Environment.GetEnvironmentVariable("URGENTRY_MARKER"));
    SentrySdk.FlushAsync(TimeSpan.FromSeconds(5)).GetAwaiter().GetResult();
}
`), 0o644); err != nil {
		return "", fmt.Errorf("write Program.cs: %w", err)
	}
	if _, err := runSDKCommand(ctx, dir, sdkEnv(dsn, marker), "dotnet", "run"); err != nil {
		return "", fmt.Errorf("dotnet run: %w", err)
	}
	return "", nil
}

func runRubySDK(ctx context.Context, t *testing.T, dsn, marker string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("ruby"); err != nil {
		return "", fmt.Errorf("runtime unavailable: ruby not found")
	}
	if _, err := exec.LookPath("gem"); err != nil {
		return "", fmt.Errorf("runtime unavailable: gem not found")
	}

	dir := t.TempDir()
	gemHome := filepath.Join(dir, "gems")
	if err := os.WriteFile(filepath.Join(dir, "main.rb"), []byte(`require "sentry-ruby"

Sentry.init do |config|
  config.dsn = ENV.fetch("URGENTRY_DSN")
  config.background_worker_threads = 0
end

Sentry.capture_message(ENV.fetch("URGENTRY_MARKER"))
Sentry.close
`), 0o644); err != nil {
		return "", fmt.Errorf("write main.rb: %w", err)
	}

	env := append(sdkEnv(dsn, marker), "GEM_HOME="+gemHome, "GEM_PATH="+gemHome)
	if _, err := runSDKCommand(ctx, dir, env, "gem", "install", "sentry-ruby", "-v", rubySDKVersion, "--no-document"); err != nil {
		return "", fmt.Errorf("gem install: %w", err)
	}
	if _, err := runSDKCommand(ctx, dir, env, "ruby", "main.rb"); err != nil {
		return "", fmt.Errorf("ruby run: %w", err)
	}
	return "", nil
}

func sdkEnv(dsn, marker string) []string {
	return []string{
		"URGENTRY_DSN=" + dsn,
		"URGENTRY_MARKER=" + marker,
		"NO_PROXY=127.0.0.1,localhost",
		"no_proxy=127.0.0.1,localhost",
	}
}

func runSDKCommand(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)

	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}

	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("timed out after %s", sdkRunnerTimeout)
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(output), fmt.Errorf("exit %d: %s", exitErr.ExitCode(), truncateSDKOutput(string(output)))
	}
	return string(output), fmt.Errorf("%v: %s", err, truncateSDKOutput(string(output)))
}

func truncateSDKOutput(output string) string {
	output = strings.Join(strings.Fields(strings.TrimSpace(output)), " ")
	if output == "" {
		return "no output"
	}
	if len(output) <= 240 {
		return output
	}
	return output[:237] + "..."
}

func readResponseBody(resp *http.Response) string {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestSDKMatrixSummary(t *testing.T) {
	score, detail := summarizeSDKMatrix([]sdkMatrixEntry{
		{name: "js-node", ok: true, detail: "ok"},
		{name: "python", ok: true, detail: "ok"},
		{name: "go", ok: false, detail: "fail"},
	})

	if score != 0.67 {
		t.Fatalf("score = %.2f, want 0.67", score)
	}
	if !strings.Contains(detail, "2/3 live SDK runners passed") {
		t.Fatalf("detail = %q, want pass count", detail)
	}
	if !strings.Contains(detail, "go=fail") {
		t.Fatalf("detail = %q, want failed runner detail", detail)
	}
}

func TestRewriteSDKProjectPath(t *testing.T) {
	got := rewriteSDKProjectPath("/api/1/envelope/")
	if got != "/api/default-project/envelope/" {
		t.Fatalf("rewriteSDKProjectPath = %q, want %q", got, "/api/default-project/envelope/")
	}
}
