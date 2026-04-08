package synthetic

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PayloadManifest struct {
	SourceFiles []string      `json:"source_files"`
	Cases       []PayloadCase `json:"cases"`
}

type PayloadCase struct {
	ID            string          `json:"id"`
	Family        string          `json:"family"`
	Format        string          `json:"format"`
	Method        string          `json:"method"`
	RouteTemplate string          `json:"route_template"`
	ContentType   string          `json:"content_type"`
	Encoding      string          `json:"encoding,omitempty"`
	AuthMode      string          `json:"auth_mode"`
	SourcePath    string          `json:"source_path,omitempty"`
	Builder       string          `json:"builder,omitempty"`
	Expected      PayloadExpected `json:"expected"`
	ScenarioHints []string        `json:"scenario_hints,omitempty"`
}

type PayloadExpected struct {
	StatusCode       int    `json:"status_code"`
	EventID          string `json:"event_id,omitempty"`
	EventType        string `json:"event_type,omitempty"`
	TraceID          string `json:"trace_id,omitempty"`
	SessionRelease   string `json:"session_release,omitempty"`
	FeedbackName     string `json:"feedback_name,omitempty"`
	CheckInSlug      string `json:"check_in_slug,omitempty"`
	ProjectEventGoal int    `json:"project_event_goal,omitempty"`
}

func generatePayloadManifest(repoRoot string) (PayloadManifest, error) {
	fixtureRoots := []string{
		filepath.Join(repoRoot, "eval", "fixtures", "store"),
		filepath.Join(repoRoot, "eval", "fixtures", "envelopes"),
		filepath.Join(repoRoot, "eval", "fixtures", "negative"),
	}
	cases := make([]PayloadCase, 0, 64)
	for _, root := range fixtureRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return PayloadManifest{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			rel := relRepoPath(filepath.Join(root, name))
			switch filepath.Base(root) {
			case "store":
				cases = append(cases, PayloadCase{
					ID:            "store/" + base,
					Family:        "store",
					Format:        "json",
					Method:        "POST",
					RouteTemplate: "/api/{project_id}/store/",
					ContentType:   "application/json",
					AuthMode:      "sentry_auth",
					SourcePath:    rel,
					Expected: PayloadExpected{
						StatusCode:       200,
						ProjectEventGoal: 1,
					},
					ScenarioHints: []string{"compat", "ingest", "errors"},
				})
			case "envelopes":
				cases = append(cases, PayloadCase{
					ID:            "envelope/" + base,
					Family:        "envelope",
					Format:        strings.TrimPrefix(ext, "."),
					Method:        "POST",
					RouteTemplate: "/api/{project_id}/envelope/",
					ContentType:   "application/x-sentry-envelope",
					AuthMode:      "sentry_auth",
					SourcePath:    rel,
					Expected: PayloadExpected{
						StatusCode:       200,
						ProjectEventGoal: 1,
					},
					ScenarioHints: []string{"compat", "ingest", "envelope"},
				})
			case "negative":
				contentType := "application/octet-stream"
				if ext == ".json" || ext == ".envelope" {
					contentType = "application/json"
				}
				cases = append(cases, PayloadCase{
					ID:            "negative/" + base,
					Family:        "negative",
					Format:        strings.TrimPrefix(ext, "."),
					Method:        "POST",
					RouteTemplate: "/api/{project_id}/store/",
					ContentType:   contentType,
					AuthMode:      "sentry_auth",
					SourcePath:    rel,
					Expected: PayloadExpected{
						StatusCode: 400,
					},
					ScenarioHints: []string{"compat", "negative"},
				})
			}
		}
	}

	cases = append(cases,
		PayloadCase{
			ID:            "dynamic/session_basic",
			Family:        "session",
			Format:        "json-envelope-item",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			AuthMode:      "sentry_auth",
			Builder:       "session_basic",
			Expected:      PayloadExpected{StatusCode: 200, SessionRelease: "synthetic@1.0.0"},
			ScenarioHints: []string{"compat", "release-health"},
		},
		PayloadCase{
			ID:            "dynamic/session_aggregate_basic",
			Family:        "session",
			Format:        "json-envelope-item",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			AuthMode:      "sentry_auth",
			Builder:       "session_aggregate_basic",
			Expected:      PayloadExpected{StatusCode: 200, SessionRelease: "synthetic@2.0.0"},
			ScenarioHints: []string{"compat", "release-health"},
		},
		PayloadCase{
			ID:            "dynamic/user_feedback_basic",
			Family:        "feedback",
			Format:        "json-envelope-item",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			AuthMode:      "sentry_auth",
			Builder:       "user_feedback_basic",
			Expected:      PayloadExpected{StatusCode: 200, FeedbackName: "Synthetic Reporter"},
			ScenarioHints: []string{"compat", "feedback"},
		},
		PayloadCase{
			ID:            "dynamic/check_in_basic",
			Family:        "check_in",
			Format:        "json-envelope-item",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			AuthMode:      "sentry_auth",
			Builder:       "check_in_basic",
			Expected:      PayloadExpected{StatusCode: 200, CheckInSlug: "synthetic-cron"},
			ScenarioHints: []string{"compat", "monitors"},
		},
		PayloadCase{
			ID:            "dynamic/client_report_basic",
			Family:        "client_report",
			Format:        "json-envelope-item",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/envelope/",
			ContentType:   "application/x-sentry-envelope",
			AuthMode:      "sentry_auth",
			Builder:       "client_report_basic",
			Expected:      PayloadExpected{StatusCode: 200},
			ScenarioHints: []string{"compat", "outcomes"},
		},
		PayloadCase{
			ID:            "dynamic/otlp_trace_basic",
			Family:        "otlp_traces",
			Format:        "json",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/otlp/v1/traces/",
			ContentType:   "application/json",
			AuthMode:      "sentry_auth",
			Builder:       "otlp_trace_basic",
			Expected:      PayloadExpected{StatusCode: 200, TraceID: "0102030405060708090a0b0c0d0e0f10"},
			ScenarioHints: []string{"compat", "traces"},
		},
		PayloadCase{
			ID:            "dynamic/otlp_log_basic",
			Family:        "otlp_logs",
			Format:        "json",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/otlp/v1/logs/",
			ContentType:   "application/json",
			AuthMode:      "sentry_auth",
			Builder:       "otlp_log_basic",
			Expected:      PayloadExpected{StatusCode: 200, EventType: "log"},
			ScenarioHints: []string{"compat", "logs"},
		},
		PayloadCase{
			ID:            "dynamic/security_csp_basic",
			Family:        "security_report",
			Format:        "json",
			Method:        "POST",
			RouteTemplate: "/api/{project_id}/security/?sentry_key={project_key}",
			ContentType:   "application/csp-report",
			AuthMode:      "query_sentry_key",
			Builder:       "security_csp_basic",
			Expected:      PayloadExpected{StatusCode: 200, ProjectEventGoal: 1},
			ScenarioHints: []string{"compat", "security"},
		},
	)

	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return PayloadManifest{
		SourceFiles: []string{
			"eval/fixtures/store/",
			"eval/fixtures/envelopes/",
			"eval/fixtures/negative/",
			"eval/compat/sdk_harness.go",
			"internal/compat/compat_test.go",
			"internal/compat/otlp_conformance_test.go",
			"internal/compat/security_report_conformance_test.go",
		},
		Cases: cases,
	}, nil
}
