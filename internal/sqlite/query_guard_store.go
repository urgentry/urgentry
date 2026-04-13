package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/auth"
)

type QueryWorkload string

const (
	QueryWorkloadOrgIssues    QueryWorkload = "org_issues"
	QueryWorkloadDiscover     QueryWorkload = "discover"
	QueryWorkloadLogs         QueryWorkload = "logs"
	QueryWorkloadTransactions QueryWorkload = "transactions"
	QueryWorkloadReplays      QueryWorkload = "replays"
	QueryWorkloadProfiles     QueryWorkload = "profiles"
)

type QueryEstimate struct {
	Workload QueryWorkload
	Limit    int
	Query    string
	Scope    string
	Detail   bool
}

type QueryGuardPolicy struct {
	MaxCostPerRequest    int
	MaxRequestsPerWindow int
	MaxCostPerWindow     int
	WindowSeconds        int
}

type QueryGuardRequest struct {
	Principal      *auth.Principal
	OrganizationID string
	ProjectID      string
	RequestPath    string
	RequestMethod  string
	IPAddress      string
	UserAgent      string
	Estimate       QueryEstimate
}

type QueryGuardDecision struct {
	Allowed    bool
	StatusCode int
	Reason     string
	RetryAfter time.Duration
	Cost       int
	Degraded   bool // true when Valkey is unavailable and protection was bypassed
}

type QueryGuard interface {
	CheckAndRecord(ctx context.Context, req QueryGuardRequest) (QueryGuardDecision, error)
}

type QueryGuardStore struct {
	db  *sql.DB
	now func() time.Time
}

var _ QueryGuard = (*QueryGuardStore)(nil)

func NewQueryGuardStore(db *sql.DB) *QueryGuardStore {
	return &QueryGuardStore{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *QueryGuardStore) CheckAndRecord(ctx context.Context, req QueryGuardRequest) (QueryGuardDecision, error) {
	if s == nil || s.db == nil {
		return QueryGuardDecision{Allowed: true, StatusCode: http.StatusOK}, nil
	}
	if req.Principal == nil || req.OrganizationID == "" {
		return QueryGuardDecision{Allowed: false, StatusCode: http.StatusForbidden, Reason: "Query guard requires an authenticated organization-scoped principal."}, nil
	}

	policy, err := s.loadPolicy(ctx, req.OrganizationID, req.Estimate.Workload)
	if err != nil {
		return QueryGuardDecision{}, err
	}
	cost, err := estimateQueryCost(req.Estimate)
	if err != nil {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusBadRequest,
			Reason:     err.Error(),
		}
		_ = s.insertAudit(ctx, req, false, decision)
		return decision, nil
	}
	if cost > policy.MaxCostPerRequest {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "Query cost exceeds the configured per-request limit.",
			Cost:       cost,
		}
		_ = s.insertAudit(ctx, req, false, decision)
		return decision, nil
	}

	window := time.Duration(policy.WindowSeconds) * time.Second
	now := s.now()
	windowStart := now.Truncate(window)
	if policy.MaxRequestsPerWindow < 1 || cost > policy.MaxCostPerWindow {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "Query quota exhausted for the current window.",
			RetryAfter: window - now.Sub(windowStart),
			Cost:       cost,
		}
		_ = s.insertAudit(ctx, req, false, decision)
		return decision, nil
	}

	actorKey := queryGuardActorKey(req.Principal)
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO query_guard_usage
			(organization_id, workload, actor_key, window_start, request_count, cost_units, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?)
		 ON CONFLICT(organization_id, workload, actor_key, window_start)
		 DO UPDATE SET
			request_count = query_guard_usage.request_count + 1,
			cost_units = query_guard_usage.cost_units + excluded.cost_units,
			updated_at = excluded.updated_at
		 WHERE query_guard_usage.request_count + 1 <= ? AND query_guard_usage.cost_units + excluded.cost_units <= ?`,
		req.OrganizationID,
		string(req.Estimate.Workload),
		actorKey,
		windowStart.Format(time.RFC3339),
		cost,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		policy.MaxRequestsPerWindow,
		policy.MaxCostPerWindow,
	)
	if err != nil {
		return QueryGuardDecision{}, fmt.Errorf("record query guard usage: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return QueryGuardDecision{}, fmt.Errorf("load query guard usage result: %w", err)
	}
	if rowsAffected == 0 {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "Query quota exhausted for the current window.",
			RetryAfter: window - now.Sub(windowStart),
			Cost:       cost,
		}
		_ = s.insertAudit(ctx, req, false, decision)
		return decision, nil
	}

	decision := QueryGuardDecision{
		Allowed:    true,
		StatusCode: http.StatusOK,
		Cost:       cost,
	}
	_ = s.insertAudit(ctx, req, true, decision)
	return decision, nil
}

func (s *QueryGuardStore) loadPolicy(ctx context.Context, organizationID string, workload QueryWorkload) (QueryGuardPolicy, error) {
	policy := defaultQueryGuardPolicy(workload)
	err := s.db.QueryRowContext(ctx,
		`SELECT max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds
		 FROM query_guard_policies
		 WHERE organization_id = ? AND workload = ?`,
		organizationID, string(workload),
	).Scan(&policy.MaxCostPerRequest, &policy.MaxRequestsPerWindow, &policy.MaxCostPerWindow, &policy.WindowSeconds)
	if err != nil {
		if err == sql.ErrNoRows {
			return policy, nil
		}
		return QueryGuardPolicy{}, fmt.Errorf("load query guard policy: %w", err)
	}
	return policy, nil
}

func (s *QueryGuardStore) insertAudit(ctx context.Context, req QueryGuardRequest, allowed bool, _ QueryGuardDecision) error {
	action := "query." + string(req.Estimate.Workload) + ".allowed"
	if !allowed {
		action = "query." + string(req.Estimate.Workload) + ".denied"
	}
	var userID string
	if req.Principal != nil && req.Principal.User != nil {
		userID = req.Principal.User.ID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_audit_logs
			(id, credential_type, credential_id, user_id, project_id, organization_id, action, request_path, request_method, ip_address, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		generateID(),
		nullIfEmpty(string(req.Principal.Kind)),
		nullIfEmpty(req.Principal.CredentialID),
		nullIfEmpty(userID),
		nullIfEmpty(req.ProjectID),
		req.OrganizationID,
		action,
		nullIfEmpty(req.RequestPath),
		nullIfEmpty(req.RequestMethod),
		nullIfEmpty(req.IPAddress),
		nullIfEmpty(req.UserAgent),
		s.now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert query audit log: %w", err)
	}
	return nil
}

func defaultQueryGuardPolicy(workload QueryWorkload) QueryGuardPolicy {
	switch workload {
	case QueryWorkloadDiscover, QueryWorkloadLogs, QueryWorkloadTransactions, QueryWorkloadOrgIssues:
		return QueryGuardPolicy{
			MaxCostPerRequest:    350,
			MaxRequestsPerWindow: 60,
			MaxCostPerWindow:     2400,
			WindowSeconds:        300,
		}
	case QueryWorkloadReplays, QueryWorkloadProfiles:
		return QueryGuardPolicy{
			MaxCostPerRequest:    220,
			MaxRequestsPerWindow: 90,
			MaxCostPerWindow:     1800,
			WindowSeconds:        300,
		}
	default:
		return QueryGuardPolicy{
			MaxCostPerRequest:    250,
			MaxRequestsPerWindow: 60,
			MaxCostPerWindow:     1800,
			WindowSeconds:        300,
		}
	}
}

func estimateQueryCost(estimate QueryEstimate) (int, error) {
	query := strings.TrimSpace(estimate.Query)
	if len(query) > 512 {
		return 0, fmt.Errorf("query exceeds the 512-character guardrail")
	}
	tokens := strings.Fields(query)
	limit := estimate.Limit
	if limit <= 0 {
		limit = 25
	}
	cost := limit
	switch estimate.Workload {
	case QueryWorkloadDiscover:
		cost = limit * 3
		if strings.TrimSpace(estimate.Scope) == "" || strings.EqualFold(strings.TrimSpace(estimate.Scope), "all") {
			cost += limit * 2
		}
	case QueryWorkloadLogs, QueryWorkloadTransactions, QueryWorkloadOrgIssues:
		cost = limit * 2
	case QueryWorkloadReplays, QueryWorkloadProfiles:
		if estimate.Detail {
			cost = 80
		} else {
			cost = limit * 2
		}
	}
	if len(tokens) > 0 {
		cost += len(tokens) * 20
	}
	parsed := ParseIssueSearch(query)
	filterCount := 0
	for _, value := range []string{parsed.Status, parsed.Release, parsed.Environment, parsed.Level, parsed.EventType} {
		if strings.TrimSpace(value) != "" {
			filterCount++
		}
	}
	if filterCount > 0 {
		cost += filterCount * 15
	}
	if query != "" {
		cost += 10 + len(query)/16
	}
	if estimate.Detail {
		cost += 10
	}
	return cost, nil
}

func queryGuardActorKey(principal *auth.Principal) string {
	if principal == nil {
		return "anonymous"
	}
	if principal.User != nil && principal.User.ID != "" {
		return "user:" + principal.User.ID
	}
	if principal.CredentialID != "" {
		return string(principal.Kind) + ":" + principal.CredentialID
	}
	if principal.ProjectID != "" {
		return "project:" + principal.ProjectID
	}
	return string(principal.Kind)
}
