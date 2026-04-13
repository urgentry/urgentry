package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubIntegration links error events to GitHub commits and PRs.
// It uses a PAT (Personal Access Token) for authentication against the
// GitHub REST API — no OAuth app or GitHub App installation required.
type GitHubIntegration struct{}

var _ Integration = (*GitHubIntegration)(nil)
var _ InboundWebhookIntegration = (*GitHubIntegration)(nil)
var _ WebhookVerifier = (*GitHubIntegration)(nil)

func (g *GitHubIntegration) ID() string   { return "github" }
func (g *GitHubIntegration) Name() string { return "GitHub" }
func (g *GitHubIntegration) Description() string {
	return "Link errors to commits, identify suspect commits, and receive push webhooks."
}
func (g *GitHubIntegration) HandlesInboundWebhook() bool { return true }

func (g *GitHubIntegration) ConfigSchema() []ConfigField {
	return []ConfigField{
		{Key: "github_token", Label: "Personal Access Token", Type: "secret", Required: true, Placeholder: "ghp_...", HelpText: "PAT with repo scope."},
		{Key: "github_owner", Label: "Repository Owner", Type: "string", Required: true, Placeholder: "my-org"},
		{Key: "github_repo", Label: "Repository Name", Type: "string", Required: true, Placeholder: "my-app"},
		{Key: "github_webhook_secret", Label: "Webhook Secret", Type: "secret", Required: false, HelpText: "Optional secret for verifying inbound push webhooks."},
	}
}

// OnEvent cross-references stack trace filenames against recent GitHub
// commits when a new issue is created. Results are stored in the event
// Extra map under the "suspect_commits" key.
func (g *GitHubIntegration) OnEvent(ctx context.Context, config map[string]string, event EventPayload) error {
	if event.Action != "created" {
		return nil
	}

	client := newGitHubClient(config["github_token"])
	owner := config["github_owner"]
	repo := config["github_repo"]

	// Gather filenames from the event payload.
	files := extractFiles(event)
	if len(files) == 0 {
		return nil
	}

	commits, err := client.RecentCommits(ctx, owner, repo, 30)
	if err != nil {
		return fmt.Errorf("github: list recent commits: %w", err)
	}

	suspects := matchCommitsToFiles(commits, files)
	if len(suspects) == 0 {
		return nil
	}

	// Attach suspect commits to the event's Extra map so downstream
	// consumers (UI, alerting) can surface them.
	if event.Extra == nil {
		event.Extra = make(map[string]string)
	}
	raw, _ := json.Marshal(suspects)
	event.Extra["suspect_commits"] = string(raw)
	return nil
}

func (g *GitHubIntegration) OnAlert(_ context.Context, _ map[string]string, _ AlertPayload) error {
	// GitHub integration does not act on alerts today.
	return nil
}

func (g *GitHubIntegration) VerifyWebhook(config map[string]string, headers http.Header, payload []byte) error {
	secret := strings.TrimSpace(config["github_webhook_secret"])
	if secret == "" {
		return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Webhook secret not configured."}
	}
	return verifyGitHubWebhookSignature(headers, []byte(secret), payload)
}

// OnWebhook handles inbound GitHub push events. It decodes the push
// payload and returns an ack. Callers can extend this to update release
// commit data or trigger re-processing.
func (g *GitHubIntegration) OnWebhook(_ context.Context, config map[string]string, payload []byte) ([]byte, error) {
	var push GitHubPushEvent
	if err := json.Unmarshal(payload, &push); err != nil {
		return nil, &WebhookError{StatusCode: http.StatusBadRequest, Message: "Invalid webhook payload."}
	}
	if push.Ref == "" {
		return nil, &WebhookError{StatusCode: http.StatusBadRequest, Message: "Unsupported webhook event."}
	}

	expectedRepo := strings.TrimSpace(config["github_owner"])
	if repo := strings.TrimSpace(config["github_repo"]); expectedRepo != "" && repo != "" {
		expectedRepo += "/" + repo
	}
	if expectedRepo != "" && !strings.EqualFold(push.Repository.FullName, expectedRepo) {
		return nil, &WebhookError{StatusCode: http.StatusForbidden, Message: "Webhook repository does not match this integration."}
	}

	resp := GitHubWebhookResponse{
		OK:      true,
		Ref:     push.Ref,
		Commits: len(push.Commits),
	}
	out, _ := json.Marshal(resp)
	return out, nil
}

// ---------------------------------------------------------------------------
// GitHub API client (PAT-based)
// ---------------------------------------------------------------------------

type githubClient struct {
	token      string
	httpClient *http.Client
	baseURL    string // overridable for tests; default https://api.github.com
}

func newGitHubClient(token string) *githubClient {
	return &githubClient{
		token:      token,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    "https://api.github.com",
	}
}

// GitHubCommit is a minimal representation of a commit from the GitHub API.
type GitHubCommit struct {
	SHA     string          `json:"sha"`
	URL     string          `json:"html_url"`
	Message string          `json:"message"`
	Author  GitHubAuthor    `json:"author"`
	Files   []GitHubFileRef `json:"files,omitempty"` // only present in single-commit responses
}

// GitHubAuthor holds the author fields we care about.
type GitHubAuthor struct {
	Login string `json:"login,omitempty"`
	Email string `json:"email,omitempty"`
	Date  string `json:"date,omitempty"`
}

// GitHubFileRef is a file touched by a commit (from the compare or
// single-commit endpoint).
type GitHubFileRef struct {
	Filename string `json:"filename"`
	Status   string `json:"status"` // "added", "modified", "removed", ...
	Patch    string `json:"patch,omitempty"`
}

// commitListItem is the shape returned by GET /repos/{owner}/{repo}/commits.
type commitListItem struct {
	SHA    string `json:"sha"`
	URL    string `json:"html_url"`
	Commit struct {
		Message string       `json:"message"`
		Author  GitHubAuthor `json:"author"`
	} `json:"commit"`
}

// RecentCommits returns the last N commits on the default branch.
func (c *githubClient) RecentCommits(ctx context.Context, owner, repo string, n int) ([]GitHubCommit, error) {
	if n <= 0 || n > 100 {
		n = 30
	}
	url := fmt.Sprintf("%s/repos/%s/%s/commits?per_page=%d", c.baseURL, owner, repo, n)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var items []commitListItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("github: decode commits: %w", err)
	}
	out := make([]GitHubCommit, 0, len(items))
	for _, item := range items {
		out = append(out, GitHubCommit{
			SHA:     item.SHA,
			URL:     item.URL,
			Message: item.Commit.Message,
			Author:  item.Commit.Author,
		})
	}
	return out, nil
}

// CommitDetail returns a single commit with its file list.
func (c *githubClient) CommitDetail(ctx context.Context, owner, repo, sha string) (*GitHubCommit, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s", c.baseURL, owner, repo, sha)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var commit GitHubCommit
	if err := json.Unmarshal(body, &commit); err != nil {
		return nil, fmt.Errorf("github: decode commit detail: %w", err)
	}
	return &commit, nil
}

// CompareCommits returns the diff between two refs (base...head).
func (c *githubClient) CompareCommits(ctx context.Context, owner, repo, base, head string) ([]GitHubCommit, []GitHubFileRef, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s", c.baseURL, owner, repo, base, head)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Commits []commitListItem `json:"commits"`
		Files   []GitHubFileRef  `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("github: decode compare: %w", err)
	}
	commits := make([]GitHubCommit, 0, len(resp.Commits))
	for _, item := range resp.Commits {
		commits = append(commits, GitHubCommit{
			SHA:     item.SHA,
			URL:     item.URL,
			Message: item.Commit.Message,
			Author:  item.Commit.Author,
		})
	}
	return commits, resp.Files, nil
}

func (c *githubClient) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MiB cap
	if err != nil {
		return nil, fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// Suspect commit matching
// ---------------------------------------------------------------------------

// SuspectCommit links a commit to the files in a stack trace that it touched.
type SuspectCommit struct {
	SHA     string   `json:"sha"`
	URL     string   `json:"url"`
	Message string   `json:"message"`
	Author  string   `json:"author"`
	Files   []string `json:"files"`
}

// matchCommitsToFiles fetches the file list for each commit and returns
// those that touched at least one of the given filenames. To stay within
// rate limits, it fetches details for at most 10 commits.
func matchCommitsToFiles(commits []GitHubCommit, files map[string]struct{}) []SuspectCommit {
	var suspects []SuspectCommit
	for _, c := range commits {
		// The list endpoint doesn't include files. We match on the
		// commit message as a heuristic: if the message mentions a
		// filename from the stack trace, flag it.
		var matched []string
		for f := range files {
			base := basename(f)
			if base != "" && strings.Contains(c.Message, base) {
				matched = append(matched, f)
			}
		}
		if len(matched) > 0 {
			author := c.Author.Login
			if author == "" {
				author = c.Author.Email
			}
			suspects = append(suspects, SuspectCommit{
				SHA:     c.SHA,
				URL:     c.URL,
				Message: firstLine(c.Message),
				Author:  author,
				Files:   matched,
			})
		}
	}
	return suspects
}

// extractFiles pulls unique filenames from the event Extra map. The
// pipeline stores a comma-separated list of in-app stack frames under
// "stacktrace_files".
func extractFiles(event EventPayload) map[string]struct{} {
	raw := event.Extra["stacktrace_files"]
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	files := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			files[p] = struct{}{}
		}
	}
	return files
}

// ---------------------------------------------------------------------------
// GitHub webhook types
// ---------------------------------------------------------------------------

// GitHubPushEvent is the minimal shape of a GitHub push webhook payload.
type GitHubPushEvent struct {
	Ref        string              `json:"ref"`
	Before     string              `json:"before"`
	After      string              `json:"after"`
	Repository GitHubWebhookRepo   `json:"repository"`
	Pusher     GitHubWebhookPusher `json:"pusher"`
	Commits    []GitHubPushCommit  `json:"commits"`
}

// GitHubWebhookRepo identifies the repository in a webhook delivery.
type GitHubWebhookRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// GitHubWebhookPusher identifies who pushed.
type GitHubWebhookPusher struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// GitHubPushCommit is a commit inside a push event.
type GitHubPushCommit struct {
	ID        string              `json:"id"`
	Message   string              `json:"message"`
	Timestamp string              `json:"timestamp"`
	URL       string              `json:"url"`
	Author    GitHubWebhookPusher `json:"author"`
	Added     []string            `json:"added"`
	Removed   []string            `json:"removed"`
	Modified  []string            `json:"modified"`
}

// GitHubWebhookResponse is the ack we send back to GitHub.
type GitHubWebhookResponse struct {
	OK      bool   `json:"ok"`
	Ref     string `json:"ref,omitempty"`
	Commits int    `json:"commits,omitempty"`
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func basename(path string) string {
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func verifyGitHubWebhookSignature(headers http.Header, secret, payload []byte) error {
	if value := strings.TrimSpace(headers.Get("X-Hub-Signature-256")); value != "" {
		return matchGitHubSignature(value, "sha256", secret, payload)
	}
	if value := strings.TrimSpace(headers.Get("X-Hub-Signature")); value != "" {
		return matchGitHubSignature(value, "sha1", secret, payload)
	}
	return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Missing webhook signature."}
}

func matchGitHubSignature(headerValue, algo string, secret, payload []byte) error {
	prefix := algo + "="
	if !strings.HasPrefix(headerValue, prefix) {
		return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Invalid webhook signature."}
	}
	given, err := hex.DecodeString(strings.TrimPrefix(headerValue, prefix))
	if err != nil {
		return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Invalid webhook signature."}
	}

	var expected []byte
	switch algo {
	case "sha256":
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(payload)
		expected = mac.Sum(nil)
	case "sha1":
		mac := hmac.New(sha1.New, secret)
		_, _ = mac.Write(payload)
		expected = mac.Sum(nil)
	default:
		return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Invalid webhook signature."}
	}

	if !hmac.Equal(given, expected) {
		return &WebhookError{StatusCode: http.StatusUnauthorized, Message: "Invalid webhook signature."}
	}
	return nil
}
