package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// JiraIntegration creates Jira issues when alerts fire. It authenticates
// via the Jira REST API using basic auth (email + API token), which is
// the standard approach for Jira Cloud. OAuth 2.0 fields are stored in
// the config for future upgrade, but the current transport uses basic auth
// over TLS as recommended by Atlassian for API tokens.
type JiraIntegration struct{}

var _ Integration = (*JiraIntegration)(nil)

func (j *JiraIntegration) ID() string         { return "jira" }
func (j *JiraIntegration) Name() string        { return "Jira" }
func (j *JiraIntegration) Description() string { return "Create Jira issues from Urgentry alerts." }

func (j *JiraIntegration) ConfigSchema() []ConfigField {
	return []ConfigField{
		{Key: "jira_url", Label: "Jira URL", Type: "url", Required: true, Placeholder: "https://yourorg.atlassian.net", HelpText: "Base URL of your Jira instance."},
		{Key: "jira_email", Label: "Jira Email", Type: "string", Required: true, Placeholder: "you@example.com", HelpText: "Email address associated with the API token."},
		{Key: "jira_api_token", Label: "API Token", Type: "secret", Required: true, Placeholder: "ATATT...", HelpText: "Jira API token (Atlassian account settings > Security > API tokens)."},
		{Key: "jira_project_key", Label: "Project Key", Type: "string", Required: true, Placeholder: "PROJ", HelpText: "Jira project key where issues will be created."},
	}
}

// OnEvent is a no-op — Jira integration is alert-driven.
func (j *JiraIntegration) OnEvent(_ context.Context, _ map[string]string, _ EventPayload) error {
	return nil
}

// OnAlert creates a Jira issue when an alert fires. The created issue
// key (e.g. "PROJ-42") is returned in alert.Extra["jira_issue_key"] for
// downstream consumers to store on the Urgentry issue.
func (j *JiraIntegration) OnAlert(ctx context.Context, config map[string]string, alert AlertPayload) error {
	if alert.Action != "trigger" {
		return nil
	}

	client := newJiraClient(config)
	if err := client.validate(); err != nil {
		return err
	}

	issue := jiraCreateIssue{
		Fields: jiraIssueFields{
			Project:     jiraProject{Key: config["jira_project_key"]},
			Summary:     fmt.Sprintf("[Urgentry] %s: %s", alert.ProjectSlug, alert.RuleName),
			Description: jiraDescription(alert),
			IssueType:   jiraIssueType{Name: "Bug"},
		},
	}

	key, err := client.createIssue(ctx, issue)
	if err != nil {
		return fmt.Errorf("jira: create issue: %w", err)
	}

	// Store the Jira issue key so callers can persist it on the Urgentry issue.
	if alert.Extra == nil {
		alert.Extra = make(map[string]string)
	}
	alert.Extra["jira_issue_key"] = key
	return nil
}

// OnWebhook is a no-op — Jira integration is outbound-only.
func (j *JiraIntegration) OnWebhook(_ context.Context, _ map[string]string, _ []byte) ([]byte, error) {
	return []byte(`{"ok":true}`), nil
}

// ---------------------------------------------------------------------------
// Jira REST API client
// ---------------------------------------------------------------------------

type jiraClient struct {
	baseURL    string
	email      string
	apiToken   string
	httpClient *http.Client
}

func newJiraClient(config map[string]string) *jiraClient {
	return &jiraClient{
		baseURL:    config["jira_url"],
		email:      config["jira_email"],
		apiToken:   config["jira_api_token"],
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *jiraClient) validate() error {
	if c.baseURL == "" {
		return fmt.Errorf("jira: missing jira_url in config")
	}
	if c.email == "" {
		return fmt.Errorf("jira: missing jira_email in config")
	}
	if c.apiToken == "" {
		return fmt.Errorf("jira: missing jira_api_token in config")
	}
	return nil
}

func (c *jiraClient) basicAuth() string {
	return base64.StdEncoding.EncodeToString([]byte(c.email + ":" + c.apiToken))
}

func (c *jiraClient) createIssue(ctx context.Context, issue jiraCreateIssue) (string, error) {
	body, err := json.Marshal(issue)
	if err != nil {
		return "", fmt.Errorf("jira: marshal issue: %w", err)
	}

	url := c.baseURL + "/rest/api/2/issue"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("jira: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+c.basicAuth())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("jira: post: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return "", fmt.Errorf("jira: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("jira: API returned %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result jiraCreateIssueResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("jira: decode response: %w", err)
	}
	return result.Key, nil
}

// ---------------------------------------------------------------------------
// Jira API wire types
// ---------------------------------------------------------------------------

type jiraCreateIssue struct {
	Fields jiraIssueFields `json:"fields"`
}

type jiraIssueFields struct {
	Project     jiraProject   `json:"project"`
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	IssueType   jiraIssueType `json:"issuetype"`
}

type jiraProject struct {
	Key string `json:"key"`
}

type jiraIssueType struct {
	Name string `json:"name"`
}

type jiraCreateIssueResponse struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func jiraDescription(alert AlertPayload) string {
	desc := fmt.Sprintf("Alert rule *%s* fired on project *%s*.\n\n", alert.RuleName, alert.ProjectSlug)
	if alert.Message != "" {
		desc += fmt.Sprintf("Message: %s\n\n", alert.Message)
	}
	desc += fmt.Sprintf("Rule ID: %s\nOrganization: %s\n", alert.RuleID, alert.OrganizationID)
	if level, ok := alert.Extra["level"]; ok {
		desc += fmt.Sprintf("Level: %s\n", level)
	}
	return desc
}
