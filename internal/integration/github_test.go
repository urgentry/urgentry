package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubIntegration_Interface(t *testing.T) {
	var _ Integration = (*GitHubIntegration)(nil)

	g := &GitHubIntegration{}
	if g.ID() != "github" {
		t.Fatalf("expected ID 'github', got %q", g.ID())
	}
	if g.Name() != "GitHub" {
		t.Fatalf("expected Name 'GitHub', got %q", g.Name())
	}
	schema := g.ConfigSchema()
	if len(schema) < 3 {
		t.Fatalf("expected at least 3 config fields, got %d", len(schema))
	}
	keys := make(map[string]bool)
	for _, f := range schema {
		keys[f.Key] = true
	}
	for _, k := range []string{"github_token", "github_owner", "github_repo"} {
		if !keys[k] {
			t.Errorf("missing required config key: %s", k)
		}
	}
}

func TestGitHubIntegration_OnEventSkipsNonCreated(t *testing.T) {
	g := &GitHubIntegration{}
	err := g.OnEvent(context.Background(), map[string]string{}, EventPayload{Action: "resolved"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubIntegration_OnEventNoFiles(t *testing.T) {
	g := &GitHubIntegration{}
	err := g.OnEvent(context.Background(), map[string]string{
		"github_token": "test",
		"github_owner": "test",
		"github_repo":  "test",
	}, EventPayload{Action: "created"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubIntegration_OnAlert(t *testing.T) {
	g := &GitHubIntegration{}
	err := g.OnAlert(context.Background(), nil, AlertPayload{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubIntegration_OnWebhookPush(t *testing.T) {
	g := &GitHubIntegration{}
	push := GitHubPushEvent{
		Ref:    "refs/heads/main",
		Before: "aaa",
		After:  "bbb",
		Commits: []GitHubPushCommit{
			{ID: "bbb", Message: "fix bug"},
		},
	}
	payload, _ := json.Marshal(push)
	resp, err := g.OnWebhook(context.Background(), nil, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result GitHubWebhookResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.OK {
		t.Error("expected OK=true")
	}
	if result.Ref != "refs/heads/main" {
		t.Errorf("expected ref 'refs/heads/main', got %q", result.Ref)
	}
	if result.Commits != 1 {
		t.Errorf("expected 1 commit, got %d", result.Commits)
	}
}

func TestGitHubIntegration_OnWebhookInvalid(t *testing.T) {
	g := &GitHubIntegration{}
	resp, err := g.OnWebhook(context.Background(), nil, []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(resp, &result)
	if result["ok"] != false {
		t.Error("expected ok=false for invalid payload")
	}
}

func TestExtractFiles(t *testing.T) {
	ev := EventPayload{
		Extra: map[string]string{
			"stacktrace_files": "src/app.js, lib/utils.py, main.go",
		},
	}
	files := extractFiles(ev)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	for _, f := range []string{"src/app.js", "lib/utils.py", "main.go"} {
		if _, ok := files[f]; !ok {
			t.Errorf("missing file: %s", f)
		}
	}
}

func TestExtractFilesEmpty(t *testing.T) {
	files := extractFiles(EventPayload{})
	if files != nil {
		t.Error("expected nil for empty event")
	}
}

func TestMatchCommitsToFiles(t *testing.T) {
	commits := []GitHubCommit{
		{SHA: "abc123", URL: "https://github.com/test/test/commit/abc123", Message: "fix bug in app.js", Author: GitHubAuthor{Login: "dev1"}},
		{SHA: "def456", URL: "https://github.com/test/test/commit/def456", Message: "update README", Author: GitHubAuthor{Email: "dev2@test.com"}},
		{SHA: "ghi789", URL: "https://github.com/test/test/commit/ghi789", Message: "refactor utils.py logic", Author: GitHubAuthor{Login: "dev3"}},
	}
	files := map[string]struct{}{
		"src/app.js":    {},
		"lib/utils.py":  {},
	}

	suspects := matchCommitsToFiles(commits, files)
	if len(suspects) != 2 {
		t.Fatalf("expected 2 suspects, got %d", len(suspects))
	}

	// First suspect should be the app.js commit
	if suspects[0].SHA != "abc123" {
		t.Errorf("expected SHA abc123, got %s", suspects[0].SHA)
	}
	if suspects[0].Author != "dev1" {
		t.Errorf("expected author dev1, got %s", suspects[0].Author)
	}

	// Second suspect should be the utils.py commit (author fallback to email)
	if suspects[1].SHA != "ghi789" {
		t.Errorf("expected SHA ghi789, got %s", suspects[1].SHA)
	}
	if suspects[1].Author != "dev3" {
		t.Errorf("expected author dev3, got %s", suspects[1].Author)
	}
}

func TestBasename(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"src/app.js", "app.js"},
		{"lib/utils.py", "utils.py"},
		{"main.go", "main.go"},
		{"a/b/c/deep.ts", "deep.ts"},
		{"", ""},
	}
	for _, tt := range tests {
		got := basename(tt.in)
		if got != tt.want {
			t.Errorf("basename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if firstLine("hello\nworld") != "hello" {
		t.Error("expected 'hello'")
	}
	if firstLine("single") != "single" {
		t.Error("expected 'single'")
	}
}

func TestGitHubClient_RecentCommits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth header")
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Error("missing accept header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]commitListItem{
			{
				SHA: "abc123",
				URL: "https://github.com/o/r/commit/abc123",
				Commit: struct {
					Message string       `json:"message"`
					Author  GitHubAuthor `json:"author"`
				}{
					Message: "test commit",
					Author:  GitHubAuthor{Login: "dev", Date: "2025-01-01T00:00:00Z"},
				},
			},
		})
	}))
	defer server.Close()

	client := newGitHubClient("test-token")
	client.baseURL = server.URL

	commits, err := client.RecentCommits(context.Background(), "o", "r", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0].SHA != "abc123" {
		t.Errorf("expected SHA abc123, got %s", commits[0].SHA)
	}
	if commits[0].Message != "test commit" {
		t.Errorf("expected message 'test commit', got %q", commits[0].Message)
	}
}

func TestGitHubClient_CompareCommits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commits": []commitListItem{
				{SHA: "bbb", Commit: struct {
					Message string       `json:"message"`
					Author  GitHubAuthor `json:"author"`
				}{Message: "second commit"}},
			},
			"files": []GitHubFileRef{
				{Filename: "src/app.js", Status: "modified"},
			},
		})
	}))
	defer server.Close()

	client := newGitHubClient("tok")
	client.baseURL = server.URL

	commits, files, err := client.CompareCommits(context.Background(), "o", "r", "aaa", "bbb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Filename != "src/app.js" {
		t.Errorf("expected filename src/app.js, got %s", files[0].Filename)
	}
}

func TestGitHubClient_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer server.Close()

	client := newGitHubClient("bad-token")
	client.baseURL = server.URL

	_, err := client.RecentCommits(context.Background(), "o", "r", 5)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !contains(err.Error(), "401") {
		t.Errorf("expected error to contain 401, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
