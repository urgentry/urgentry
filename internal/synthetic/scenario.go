package synthetic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Scenario struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Vars        map[string]string `yaml:"vars,omitempty" json:"vars,omitempty"`
	Server      HarnessOptions    `yaml:"server,omitempty" json:"server,omitempty"`
	Steps       []Step            `yaml:"steps" json:"steps"`
}

type Step struct {
	Name           string            `yaml:"name" json:"name"`
	Action         string            `yaml:"action" json:"action"`
	Method         string            `yaml:"method,omitempty" json:"method,omitempty"`
	Auth           string            `yaml:"auth,omitempty" json:"auth,omitempty"`
	Path           string            `yaml:"path,omitempty" json:"path,omitempty"`
	Query          map[string]string `yaml:"query,omitempty" json:"query,omitempty"`
	ContentType    string            `yaml:"content_type,omitempty" json:"content_type,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body           string            `yaml:"body,omitempty" json:"body,omitempty"`
	BodyFile       string            `yaml:"body_file,omitempty" json:"body_file,omitempty"`
	ExpectStatus   int               `yaml:"expect_status,omitempty" json:"expect_status,omitempty"`
	ExpectContains []string          `yaml:"expect_contains,omitempty" json:"expect_contains,omitempty"`
	CaptureJSON    map[string]string `yaml:"capture_json,omitempty" json:"capture_json,omitempty"`
	Waiter         string            `yaml:"waiter,omitempty" json:"waiter,omitempty"`
	WaitParams     map[string]string `yaml:"wait_params,omitempty" json:"wait_params,omitempty"`
}

type Result struct {
	Scenario string       `json:"scenario"`
	Steps    []StepResult `json:"steps,omitempty"`
}

type StepResult struct {
	Name       string            `json:"name"`
	Action     string            `json:"action"`
	StatusCode int               `json:"status_code,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
}

func ResolveScenarioPath(name string) (string, error) {
	candidates := make([]string, 0, 4)
	switch {
	case filepath.IsAbs(name):
		candidates = append(candidates, name)
	case strings.ContainsRune(name, os.PathSeparator):
		candidates = append(candidates, name, filepath.Join(RepoRoot(), name))
	default:
		candidates = append(candidates,
			filepath.Join(ScenariosRoot(), name),
			filepath.Join(ScenariosRoot(), name+".yaml"),
			filepath.Join(ScenariosRoot(), name+".yml"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("scenario %q not found", name)
}

func RunScenarioFile(ctx context.Context, path string) (Result, error) {
	resolvedPath, err := ResolveScenarioPath(path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return Result{}, err
	}
	var scenario Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(scenario.Name) == "" {
		return Result{}, fmt.Errorf("scenario name must not be empty")
	}
	if len(scenario.Steps) == 0 {
		return Result{}, fmt.Errorf("scenario steps must not be empty")
	}

	harness, err := NewHarness(scenario.Server)
	if err != nil {
		return Result{}, err
	}
	defer harness.Close()

	baseDir := filepath.Dir(resolvedPath)
	vars := map[string]string{
		"base_url":    harness.BaseURL(),
		"project_key": harness.ProjectKey(),
		"pat":         harness.PAT(),
		"email":       harness.Email(),
		"password":    harness.Password(),
	}
	for key, value := range scenario.Vars {
		vars[key] = value
	}

	result := Result{Scenario: scenario.Name, Steps: make([]StepResult, 0, len(scenario.Steps))}
	for _, step := range scenario.Steps {
		item, captures, err := runScenarioStep(ctx, harness, vars, baseDir, step)
		if err != nil {
			return result, fmt.Errorf("%s: %w", step.Name, err)
		}
		for key, value := range captures {
			vars[key] = value
		}
		result.Steps = append(result.Steps, item)
	}
	return result, nil
}

func runScenarioStep(ctx context.Context, harness *Harness, vars map[string]string, baseDir string, step Step) (StepResult, map[string]string, error) {
	switch step.Action {
	case "request":
		return runScenarioRequest(ctx, harness, vars, baseDir, step)
	case "wait", "wait_for":
		return StepResult{Name: step.Name, Action: step.Action}, nil, runScenarioWait(ctx, harness, vars, step)
	default:
		return StepResult{}, nil, fmt.Errorf("unsupported action %q", step.Action)
	}
}

func runScenarioRequest(ctx context.Context, harness *Harness, vars map[string]string, baseDir string, step Step) (StepResult, map[string]string, error) {
	body, err := loadScenarioBody(baseDir, step, vars)
	if err != nil {
		return StepResult{}, nil, err
	}
	path, err := renderScenarioString(step.Path, vars)
	if err != nil {
		return StepResult{}, nil, err
	}
	contentType, err := renderScenarioString(step.ContentType, vars)
	if err != nil {
		return StepResult{}, nil, err
	}
	headers, err := renderScenarioStringMap(step.Headers, vars)
	if err != nil {
		return StepResult{}, nil, err
	}
	query, err := renderScenarioStringMap(step.Query, vars)
	if err != nil {
		return StepResult{}, nil, err
	}
	resp, err := harness.DoRequest(ctx, Request{
		Method:      strings.ToUpper(strings.TrimSpace(step.Method)),
		Path:        path,
		Auth:        step.Auth,
		Body:        body,
		ContentType: contentType,
		Headers:     headers,
		Query:       query,
	})
	if err != nil {
		return StepResult{}, nil, err
	}
	if step.ExpectStatus > 0 && resp.StatusCode != step.ExpectStatus {
		return StepResult{}, nil, fmt.Errorf("status=%d want=%d body=%s", resp.StatusCode, step.ExpectStatus, resp.Body)
	}
	for _, fragment := range step.ExpectContains {
		rendered, err := renderScenarioString(fragment, vars)
		if err != nil {
			return StepResult{}, nil, err
		}
		if !bytes.Contains(resp.Body, []byte(rendered)) {
			return StepResult{}, nil, fmt.Errorf("response missing fragment %q", rendered)
		}
	}
	captures, err := captureJSONValues(step.CaptureJSON, resp.Body)
	if err != nil {
		return StepResult{}, nil, err
	}
	return StepResult{
		Name:       step.Name,
		Action:     step.Action,
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		Body:       string(resp.Body),
	}, captures, nil
}

func runScenarioWait(ctx context.Context, harness *Harness, vars map[string]string, step Step) error {
	params, err := renderScenarioStringMap(step.WaitParams, vars)
	if err != nil {
		return err
	}
	waiter, err := renderScenarioString(step.Waiter, vars)
	if err != nil {
		return err
	}
	switch waiter {
	case "event":
		return harness.WaitForEvent(ctx, params["event_id"])
	case "project_event_count":
		return harness.WaitForProjectEventCount(ctx, params["project_id"], atoiOrZero(params["want"]))
	case "transaction_count":
		return harness.WaitForTransactionCount(ctx, params["project_id"], atoiOrZero(params["want"]))
	case "trace":
		return harness.WaitForTrace(ctx, params["project_id"], params["trace_id"])
	case "session":
		return harness.WaitForSession(ctx, params["project_id"], params["release"])
	case "feedback":
		return harness.WaitForFeedback(ctx, params["project_id"], params["name"])
	case "check_in":
		return harness.WaitForCheckIn(ctx, params["project_id"], params["monitor_slug"])
	case "metric_bucket":
		return harness.WaitForMetricBucket(ctx, params["project_id"], params["name"])
	case "native_event_status":
		return harness.WaitForNativeEventStatus(ctx, params["project_id"], params["event_id"], params["status"])
	case "backfill_run_status":
		return harness.WaitForBackfillRunStatus(ctx, params["run_id"], splitCSV(params["status"])...)
	default:
		return fmt.Errorf("unsupported waiter %q", waiter)
	}
}

func loadScenarioBody(baseDir string, step Step, vars map[string]string) ([]byte, error) {
	switch {
	case step.BodyFile != "":
		path, err := renderScenarioString(step.BodyFile, vars)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		// Preserve binary and precompressed fixtures unless the file explicitly
		// uses template markers.
		if utf8.Valid(body) && bytes.Contains(body, []byte("{{")) {
			rendered, err := renderScenarioString(string(body), vars)
			if err != nil {
				return nil, err
			}
			return []byte(rendered), nil
		}
		return body, nil
	case step.Body != "":
		rendered, err := renderScenarioString(step.Body, vars)
		if err != nil {
			return nil, err
		}
		return []byte(rendered), nil
	default:
		return nil, nil
	}
}

func renderScenarioString(raw string, vars map[string]string) (string, error) {
	if !strings.Contains(raw, "{{") {
		return raw, nil
	}
	tmpl, err := template.New("synthetic").Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderScenarioStringMap(input map[string]string, vars map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(input))
	for _, key := range keys {
		rendered, err := renderScenarioString(input[key], vars)
		if err != nil {
			return nil, err
		}
		out[key] = rendered
	}
	return out, nil
}

func captureJSONValues(captures map[string]string, body []byte) (map[string]string, error) {
	if len(captures) == 0 {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode json response: %w", err)
	}
	out := make(map[string]string, len(captures))
	for target, key := range captures {
		if value, ok := payload[key]; ok {
			out[target] = fmt.Sprint(value)
		}
	}
	return out, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
