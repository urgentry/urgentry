package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type ValkeyQueryGuardStore struct {
	db     *sql.DB
	client *redis.Client
	now    func() time.Time
}

var _ QueryGuard = (*ValkeyQueryGuardStore)(nil)

func NewValkeyQueryGuardStore(db *sql.DB, url string) (*ValkeyQueryGuardStore, error) {
	if db == nil {
		return nil, fmt.Errorf("query guard database is required")
	}
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("valkey url is required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse valkey url: %w", err)
	}
	opts.DisableIdentity = true
	return &ValkeyQueryGuardStore{
		db:     db,
		client: redis.NewClient(opts),
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *ValkeyQueryGuardStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *ValkeyQueryGuardStore) CheckAndRecord(ctx context.Context, req QueryGuardRequest) (QueryGuardDecision, error) {
	if s == nil || s.client == nil {
		return QueryGuardDecision{Allowed: true, StatusCode: http.StatusOK}, nil
	}
	if req.Principal == nil || req.OrganizationID == "" {
		return QueryGuardDecision{Allowed: false, StatusCode: http.StatusForbidden, Reason: "Query guard requires an authenticated organization-scoped principal."}, nil
	}

	policy, err := NewQueryGuardStore(s.db).loadPolicy(ctx, req.OrganizationID, req.Estimate.Workload)
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
		_ = NewQueryGuardStore(s.db).insertAudit(ctx, req, false, decision)
		return decision, nil
	}
	if cost > policy.MaxCostPerRequest {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "Query cost exceeds the configured per-request limit.",
			Cost:       cost,
		}
		_ = NewQueryGuardStore(s.db).insertAudit(ctx, req, false, decision)
		return decision, nil
	}

	window := time.Duration(policy.WindowSeconds) * time.Second
	now := s.now()
	windowStart := now.Truncate(window)
	retryAfter := window - now.Sub(windowStart)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	if policy.MaxRequestsPerWindow < 1 || cost > policy.MaxCostPerWindow {
		decision := QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "Query quota exhausted for the current window.",
			RetryAfter: retryAfter,
			Cost:       cost,
		}
		_ = NewQueryGuardStore(s.db).insertAudit(ctx, req, false, decision)
		return decision, nil
	}

	key := fmt.Sprintf("urgentry:quota:org:%s:%s:%s:%d", req.OrganizationID, req.Estimate.Workload, queryGuardActorKey(req.Principal), windowStart.Unix())
	const script = `
local count = tonumber(redis.call("HGET", KEYS[1], "count") or "0")
local cost = tonumber(redis.call("HGET", KEYS[1], "cost") or "0")
local nextCount = count + 1
local nextCost = cost + tonumber(ARGV[1])
if nextCount > tonumber(ARGV[2]) or nextCost > tonumber(ARGV[3]) then
  return {0, count, cost}
end
redis.call("HSET", KEYS[1], "count", nextCount, "cost", nextCost)
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[4]))
return {1, nextCount, nextCost}
`
	valuesRaw, err := s.client.Eval(ctx, script, []string{key}, cost, policy.MaxRequestsPerWindow, policy.MaxCostPerWindow, int(window.Seconds())+1).Result()
	if err != nil {
		// Degrade gracefully: allow the request through when Valkey is
		// unavailable rather than turning a cache outage into org-wide 500s.
		log.Warn().Err(err).
			Str("organization_id", req.OrganizationID).
			Str("workload", string(req.Estimate.Workload)).
			Msg("query guard degraded: valkey unavailable, allowing request")
		decision := QueryGuardDecision{
			Allowed:    true,
			StatusCode: http.StatusOK,
			Cost:       cost,
			Degraded:   true,
		}
		_ = NewQueryGuardStore(s.db).insertAudit(ctx, req, true, decision)
		return decision, nil
	}
	values, ok := valuesRaw.([]any)
	allowed := ok && len(values) > 0 && values[0] == int64(1)
	decision := QueryGuardDecision{
		Allowed:    allowed,
		StatusCode: http.StatusOK,
		Cost:       cost,
	}
	if !allowed {
		decision.StatusCode = http.StatusTooManyRequests
		decision.Reason = "Query quota exhausted for the current window."
		decision.RetryAfter = retryAfter
	}
	_ = NewQueryGuardStore(s.db).insertAudit(ctx, req, allowed, decision)
	return decision, nil
}
