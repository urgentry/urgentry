package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

type startAutofixRequest struct {
	EventID       string `json:"event_id"`
	Instruction   string `json:"instruction"`
	PRCommentURL  string `json:"pr_to_comment_on_url"`
	StoppingPoint string `json:"stopping_point"`
}

type startAutofixResponse struct {
	RunID int64 `json:"run_id"`
}

type autofixIssueContext struct {
	IssueID          string
	Title            string
	Culprit          string
	LastEventID      string
	ProjectID        string
	ProjectSlug      string
	OrganizationID   string
	OrganizationSlug string
}

func handleGetIssueAutofix(store *sqlite.AutofixStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		run, err := store.GetLatestRun(r.Context(), PathParam(r, "issue_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue autofix.")
			return
		}
		var payload any
		if run != nil {
			payload = run.Payload
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"autofix": payload})
	}
}

func handleStartIssueAutofix(db *sql.DB, store *sqlite.AutofixStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}

		req, err := parseStartAutofixRequest(r)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Detail: err.Error(),
			})
			return
		}

		issueCtx, err := loadAutofixIssueContext(r.Context(), db, PathParam(r, "issue_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue.")
			return
		}
		if issueCtx == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}

		eventID := strings.TrimSpace(req.EventID)
		if eventID == "" {
			eventID = issueCtx.LastEventID
		}
		if eventID != "" {
			ok, err := autofixEventBelongsToIssue(r.Context(), db, issueCtx.IssueID, eventID)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to load issue.")
				return
			}
			if !ok {
				httputil.WriteAPIError(w, httputil.APIError{
					Status: http.StatusBadRequest,
					Detail: "event_id must belong to the target issue.",
				})
				return
			}
		}

		now := time.Now().UTC()
		payload := buildAutofixPayload(*issueCtx, req, eventID, now)
		runID, err := store.CreateRun(r.Context(), &sqlite.AutofixRun{
			OrganizationID: issueCtx.OrganizationID,
			ProjectID:      issueCtx.ProjectID,
			IssueID:        issueCtx.IssueID,
			Status:         "COMPLETED",
			EventID:        eventID,
			StoppingPoint:  req.StoppingPoint,
			Payload:        payload,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to start issue autofix.")
			return
		}

		httputil.WriteJSON(w, http.StatusAccepted, startAutofixResponse{RunID: runID})
	}
}

func parseStartAutofixRequest(r *http.Request) (startAutofixRequest, error) {
	req := startAutofixRequest{StoppingPoint: "root_cause"}
	if r.Body == nil {
		return req, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		return startAutofixRequest{}, autofixBadRequest("Invalid JSON body.")
	}
	req.EventID = strings.TrimSpace(req.EventID)
	req.Instruction = strings.TrimSpace(req.Instruction)
	req.PRCommentURL = strings.TrimSpace(req.PRCommentURL)
	req.StoppingPoint = normalizeAutofixStoppingPoint(req.StoppingPoint)
	if req.PRCommentURL != "" {
		parsed, err := url.Parse(req.PRCommentURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return startAutofixRequest{}, autofixBadRequest("pr_to_comment_on_url must be a valid URL.")
		}
	}
	switch req.StoppingPoint {
	case "root_cause", "solution", "code_changes", "open_pr":
		return req, nil
	default:
		return startAutofixRequest{}, autofixBadRequest("stopping_point must be one of root_cause, solution, code_changes, or open_pr.")
	}
}

func loadAutofixIssueContext(ctx context.Context, db *sql.DB, issueID string) (*autofixIssueContext, error) {
	var issue autofixIssueContext
	err := db.QueryRowContext(ctx, `
		SELECT
			g.id,
			COALESCE(g.title, ''),
			COALESCE(g.culprit, ''),
			COALESCE(g.last_event_id, ''),
			p.id,
			p.slug,
			o.id,
			o.slug
		FROM groups g
		JOIN projects p ON p.id = g.project_id
		JOIN organizations o ON o.id = p.organization_id
		WHERE g.id = ?`,
		issueID,
	).Scan(
		&issue.IssueID,
		&issue.Title,
		&issue.Culprit,
		&issue.LastEventID,
		&issue.ProjectID,
		&issue.ProjectSlug,
		&issue.OrganizationID,
		&issue.OrganizationSlug,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func autofixEventBelongsToIssue(ctx context.Context, db *sql.DB, issueID, eventID string) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM events WHERE group_id = ? AND event_id = ?`,
		issueID, eventID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func buildAutofixPayload(issue autofixIssueContext, req startAutofixRequest, eventID string, now time.Time) map[string]any {
	timestamp := now.Format(time.RFC3339Nano)
	rootCauseSummary := "The issue clusters around " + issue.Title + "."
	if strings.TrimSpace(issue.Culprit) != "" {
		rootCauseSummary = "The issue clusters around " + issue.Title + " in " + issue.Culprit + "."
	}
	solutionSummary := "Review the failing path and add guards around the condition that produces " + issue.Title + "."
	if strings.TrimSpace(req.Instruction) != "" {
		solutionSummary = solutionSummary + " Operator instruction: " + req.Instruction
	}

	steps := []map[string]any{
		{
			"id":      "root_cause_analysis",
			"key":     "root_cause_analysis",
			"title":   "Root Cause Analysis",
			"type":    "root_cause_analysis",
			"status":  "COMPLETED",
			"summary": rootCauseSummary,
		},
	}

	payload := map[string]any{
		"status": "COMPLETED",
		"request": map[string]any{
			"organization_id":      issue.OrganizationID,
			"project_id":           issue.ProjectID,
			"issue_id":             issue.IssueID,
			"event_id":             emptyStringAsNull(eventID),
			"instruction":          emptyStringAsNull(req.Instruction),
			"pr_to_comment_on_url": emptyStringAsNull(req.PRCommentURL),
			"stopping_point":       req.StoppingPoint,
			"repos":                []any{},
		},
		"issue": map[string]any{
			"id":                issue.IssueID,
			"title":             issue.Title,
			"culprit":           issue.Culprit,
			"last_event_id":     emptyStringAsNull(eventID),
			"organization_slug": issue.OrganizationSlug,
			"project_slug":      issue.ProjectSlug,
		},
		"steps":             steps,
		"repositories":      []any{},
		"codebases":         map[string]any{},
		"root_cause":        map[string]any{"summary": rootCauseSummary},
		"solution":          nil,
		"code_changes":      nil,
		"last_triggered_at": timestamp,
		"updated_at":        timestamp,
		"completed_at":      timestamp,
	}

	if req.StoppingPoint == "solution" || req.StoppingPoint == "code_changes" || req.StoppingPoint == "open_pr" {
		steps = append(steps, map[string]any{
			"id":      "solution",
			"key":     "solution",
			"title":   "Proposed Solution",
			"type":    "solution",
			"status":  "COMPLETED",
			"summary": solutionSummary,
		})
		payload["steps"] = steps
		payload["solution"] = map[string]any{
			"summary": solutionSummary,
		}
	}

	if req.StoppingPoint == "code_changes" || req.StoppingPoint == "open_pr" {
		changeSummary := "Repository write access is not configured, so Urgentry recorded a proposed change set without editing source files."
		steps = append(steps, map[string]any{
			"id":      "code_changes",
			"key":     "code_changes",
			"title":   "Generated Code Changes",
			"type":    "code_changes",
			"status":  "COMPLETED",
			"summary": changeSummary,
		})
		payload["steps"] = steps
		payload["code_changes"] = []map[string]any{
			{
				"description": changeSummary,
				"patch":       "",
			},
		}
	}

	if req.StoppingPoint == "open_pr" {
		prSummary := "No linked repository integration is available, so the run stopped before opening a pull request."
		steps = append(steps, map[string]any{
			"id":      "open_pr",
			"key":     "open_pr",
			"title":   "Open Pull Request",
			"type":    "open_pr",
			"status":  "SKIPPED",
			"summary": prSummary,
		})
		payload["steps"] = steps
		payload["pull_request"] = map[string]any{
			"status":  "SKIPPED",
			"summary": prSummary,
			"url":     emptyStringAsNull(req.PRCommentURL),
		}
	}

	return payload
}

func normalizeAutofixStoppingPoint(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "root_cause"
	}
	return value
}

func emptyStringAsNull(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func autofixBadRequest(detail string) error {
	return autofixRequestError(detail)
}

type autofixRequestError string

func (e autofixRequestError) Error() string { return string(e) }
