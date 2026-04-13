package http

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/config"
	"urgentry/internal/httputil"
	"urgentry/internal/ingest"
	"urgentry/internal/metrics"
	"urgentry/internal/middleware"
	"urgentry/internal/pipeline"
	"urgentry/internal/profiling"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/web"
)

/*
HTTP server composition

	app/internal/app
	  -> builds Deps bundles
	  -> NewServer(role, cfg, deps)
	     -> auth / metrics / profiling middleware
	     -> mount ingest routes
	     -> mount API routes
	     -> mount web routes
	     -> expose health / operator endpoints
*/

type healthResponse struct {
	Status          string    `json:"status"`
	Role            string    `json:"role"`
	Env             string    `json:"env"`
	AsyncBackend    string    `json:"async_backend,omitempty"`
	CacheBackend    string    `json:"cache_backend,omitempty"`
	Now             time.Time `json:"now"`
	Uptime          string    `json:"uptime,omitempty"`
	EventsProcessed int64     `json:"events_processed,omitempty"`
	GroupsCreated   int64     `json:"groups_created,omitempty"`
	QueueDepth      int       `json:"queue_depth,omitempty"`
	MemoryMB        int       `json:"memory_mb,omitempty"`
	Version         string    `json:"version,omitempty"`
}

type readyCheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type readyResponse struct {
	Status string             `json:"status"`
	Role   string             `json:"role"`
	Env    string             `json:"env"`
	Now    time.Time          `json:"now"`
	Checks []readyCheckResult `json:"checks"`
}

// Deps holds all dependencies for the HTTP server.
type Deps struct {
	KeyStore    auth.KeyStore
	AuthStore   auth.Store
	RelayStore  auth.TrustedRelayStore
	SAMLConfigs auth.SAMLConfigStore
	SAMLUsers   auth.SAMLUserProvisioner
	RateLimiter auth.RateLimiter
	Pipeline    *pipeline.Pipeline // nil = no async processing (events accepted but not stored)
	DB          *sql.DB            // required for all runtime roles
	QueueDB     *sql.DB
	Lifecycle   store.LifecycleStore
	Ingest      ingest.IngestDeps
	API         api.Dependencies
	Web         web.Dependencies
	Metrics     *metrics.Metrics // nil = metrics endpoints return empty data
	Version     string           // build version for healthz
}

type relayAuditWriter struct {
	audits *sqlite.AuditStore
}

func (w relayAuditWriter) RecordTrustedRelayDecision(ctx context.Context, record auth.TrustedRelayAuditRecord) error {
	if w.audits == nil {
		return nil
	}
	return w.audits.Record(ctx, sqlite.AuditRecord{
		CredentialType: trustedRelayCredentialType(),
		CredentialID:   record.RelayID,
		OrganizationID: record.OrganizationID,
		Action:         record.Action,
		RequestPath:    record.RequestPath,
		RequestMethod:  record.RequestMethod,
		IPAddress:      record.IPAddress,
		UserAgent:      record.UserAgent,
	})
}

func trustedRelayCredentialType() string {
	return "relay"
}

// ValidateDeps checks the role-aware runtime dependencies needed to build the
// HTTP server. NewServer still panics on invalid deps for direct misuse in
// tests, but app/runtime code should validate first and return the error.
func ValidateDeps(role string, cfg config.Config, deps Deps) error {
	if deps.DB == nil {
		return errors.New("requires a SQLite database")
	}
	if mountsIngest(role) {
		if deps.KeyStore == nil {
			return errors.New("requires a key store for ingest roles")
		}
		if deps.Ingest.NativeCrashes == nil {
			return errors.New("requires a native crash store for ingest roles")
		}
	}
	if !mountsAPI(role) {
		return nil
	}

	authz, tokenManager, err := apiRuntimeDeps(cfg, deps.AuthStore)
	if err != nil {
		return err
	}

	apiDeps := deps.API
	apiDeps.DB = deps.DB
	apiDeps.Auth = authz
	apiDeps.TokenManager = tokenManager
	if err := api.ValidateDependencies(apiDeps); err != nil {
		return fmt.Errorf("requires valid API dependencies: %w", err)
	}

	webDeps := deps.Web
	webDeps.DB = deps.DB
	webDeps.Auth = authz
	if err := web.ValidateDependencies(webDeps); err != nil {
		return fmt.Errorf("requires valid web dependencies: %w", err)
	}

	return nil
}

func apiRuntimeDeps(cfg config.Config, authStore auth.Store) (*auth.Authorizer, auth.TokenManager, error) {
	if authStore == nil {
		return nil, nil, errors.New("requires an auth store for API roles")
	}
	authz := auth.NewAuthorizer(authStore, cfg.SessionCookieName, cfg.CSRFCookieName, 30*24*time.Hour)
	tokenManager, ok := authStore.(auth.TokenManager)
	if !ok {
		return nil, nil, errors.New("requires an auth store with token manager support for API roles")
	}
	return authz, tokenManager, nil
}

func BuildServer(role string, cfg config.Config, deps Deps) (http.Handler, error) {
	if err := ValidateDeps(role, cfg, deps); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	var authz *auth.Authorizer
	var tokenManager auth.TokenManager
	ingestLimiter := deps.RateLimiter
	if ingestLimiter == nil {
		ingestLimiter = auth.NewFixedWindowRateLimiter(time.Minute)
	}
	if mountsAPI(role) {
		var err error
		authz, tokenManager, err = apiRuntimeDeps(cfg, deps.AuthStore)
		if err != nil {
			return nil, err
		}
	}
	ingestDeps := deps.Ingest
	if ingestDeps.Pipeline == nil {
		ingestDeps.Pipeline = deps.Pipeline
	}
	if ingestDeps.Metrics == nil {
		ingestDeps.Metrics = deps.Metrics
	}

	// Health endpoints (enriched with telemetry when metrics are available)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		resp := healthResponse{
			Status:       "ok",
			Role:         role,
			Env:          cfg.Env,
			AsyncBackend: cfg.AsyncBackend,
			CacheBackend: cfg.CacheBackend,
			Now:          time.Now().UTC(),
			Version:      deps.Version,
		}
		if deps.Metrics != nil {
			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)
			resp.Uptime = time.Since(deps.Metrics.StartedAt).Round(time.Second).String()
			resp.EventsProcessed = deps.Metrics.EventsProcessed.Load()
			resp.GroupsCreated = deps.Metrics.GroupsCreated.Load()
			resp.MemoryMB = int(memStats.HeapAlloc / 1024 / 1024)
		}
		if deps.Pipeline != nil {
			resp.QueueDepth = deps.Pipeline.Len()
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		var checks []readyCheckResult
		healthy := true

		// Database — required for all roles.
		if deps.DB != nil {
			if err := deps.DB.PingContext(ctx); err != nil {
				checks = append(checks, readyCheckResult{Name: "database", Status: "error", Detail: err.Error()})
				healthy = false
			} else {
				checks = append(checks, readyCheckResult{Name: "database", Status: "ok"})
			}
		}

		// Queue (NATS/JetStream) — critical for ingest, worker, and all roles.
		if needsQueue(role) && strings.EqualFold(strings.TrimSpace(cfg.AsyncBackend), "jetstream") {
			if strings.TrimSpace(cfg.NATSURL) == "" {
				checks = append(checks, readyCheckResult{Name: "queue", Status: "error", Detail: "missing nats url"})
				healthy = false
			} else {
				nc, err := nats.Connect(cfg.NATSURL, nats.Name("urgentry-readyz"), nats.MaxReconnects(0), nats.Timeout(2*time.Second))
				if err != nil {
					checks = append(checks, readyCheckResult{Name: "queue", Status: "error", Detail: err.Error()})
					healthy = false
				} else {
					nc.Close()
					checks = append(checks, readyCheckResult{Name: "queue", Status: "ok"})
				}
			}
			if deps.QueueDB != nil {
				if err := deps.QueueDB.PingContext(ctx); err != nil {
					checks = append(checks, readyCheckResult{Name: "queue_database", Status: "error", Detail: err.Error()})
					healthy = false
				} else {
					checks = append(checks, readyCheckResult{Name: "queue_database", Status: "ok"})
				}
			}
		}

		// Cache (Valkey) — critical for api and all roles when configured.
		if needsCache(role) && strings.EqualFold(strings.TrimSpace(cfg.CacheBackend), "valkey") {
			if strings.TrimSpace(cfg.ValkeyURL) == "" {
				checks = append(checks, readyCheckResult{Name: "cache", Status: "error", Detail: "missing valkey url"})
				healthy = false
			} else {
				opts, err := redis.ParseURL(cfg.ValkeyURL)
				if err != nil {
					checks = append(checks, readyCheckResult{Name: "cache", Status: "error", Detail: err.Error()})
					healthy = false
				} else {
					opts.DisableIdentity = true
					client := redis.NewClient(opts)
					if err := client.Ping(ctx).Err(); err != nil {
						checks = append(checks, readyCheckResult{Name: "cache", Status: "error", Detail: err.Error()})
						healthy = false
					} else {
						checks = append(checks, readyCheckResult{Name: "cache", Status: "ok"})
					}
					client.Close()
				}
			}
		}

		// Backlog pressure — if the pipeline queue is deeply backed up,
		// signal unavailability so orchestrators stop routing new traffic.
		if deps.Pipeline != nil && mountsIngest(role) {
			depth := deps.Pipeline.Len()
			const backlogThreshold = 10000
			if depth > backlogThreshold {
				checks = append(checks, readyCheckResult{Name: "backlog", Status: "error", Detail: fmt.Sprintf("queue depth %d exceeds threshold %d", depth, backlogThreshold)})
				healthy = false
			} else {
				checks = append(checks, readyCheckResult{Name: "backlog", Status: "ok", Detail: fmt.Sprintf("depth=%d", depth)})
			}
		}

		status := "ready"
		code := http.StatusOK
		if !healthy {
			status = "unavailable"
			code = http.StatusServiceUnavailable
		}
		httputil.WriteJSON(w, code, readyResponse{
			Status: status,
			Role:   role,
			Env:    cfg.Env,
			Now:    time.Now().UTC(),
			Checks: checks,
		})
	})

	// Metrics endpoint
	if deps.Metrics != nil {
		mux.HandleFunc("/metrics", deps.Metrics.Handler())
	} else {
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
			httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "no metrics configured"})
		})
	}
	if cfg.ProfilingEnabled {
		profiling.Register(mux, cfg.ProfilingToken)
	}

	if mountsIngest(role) {
		// Ingest endpoints with auth + compression + CORS
		ingestMiddleware := []func(http.Handler) http.Handler{
			middleware.IngestCORS,
		}
		if deps.KeyStore != nil {
			authMW := auth.MiddlewareWithMetrics(deps.KeyStore, ingestLimiter, cfg.IngestRateLimit, deps.Metrics)
			if deps.RelayStore != nil {
				ingestMiddleware = append(ingestMiddleware,
					middleware.Decompress,
					auth.RelayMiddleware(deps.RelayStore, relayAuditWriter{audits: deps.API.Audits}),
					authMW,
				)
			} else {
				// Keep the lean pre-relay order when trusted relay is disabled.
				ingestMiddleware = append(ingestMiddleware, authMW, middleware.Decompress)
			}
		} else {
			if deps.RelayStore != nil {
				ingestMiddleware = append(ingestMiddleware,
					auth.RelayMiddleware(deps.RelayStore, relayAuditWriter{audits: deps.API.Audits}),
				)
			}
			ingestMiddleware = append(ingestMiddleware, middleware.Decompress)
		}
		ingestChain := middleware.Chain(ingestMiddleware...)

		mux.Handle("POST /api/{project_id}/store/", ingestChain(ingest.StoreHandlerWithMetrics(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/envelope/", ingestChain(ingest.EnvelopeHandlerWithDeps(ingestDeps)))
		mux.Handle("POST /api/{project_id}/minidump/", ingestChain(ingest.MinidumpHandlerWithDeps(ingestDeps)))
		mux.Handle("POST /api/{project_id}/security/", ingestChain(ingest.SecurityReportHandler(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/csp-report/", ingestChain(ingest.SecurityReportHandler(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/nel/", ingestChain(ingest.SecurityReportHandler(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/unreal/{sentry_key}/", ingestChain(ingest.MinidumpHandlerWithDeps(ingestDeps)))
		mux.Handle("POST /api/{project_id}/otlp/v1/traces/", ingestChain(ingest.OTLPTracesHandler(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/otlp/v1/logs/", ingestChain(ingest.OTLPLogsHandler(ingestDeps.Pipeline, ingestDeps.Metrics)))
		mux.Handle("POST /api/{project_id}/otlp/v1/metrics/", ingestChain(ingest.OTLPMetricsHandler(ingestDeps.MetricBuckets, ingestDeps.Metrics)))
		mux.Handle("OPTIONS /api/{project_id}/store/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/envelope/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/minidump/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/security/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/csp-report/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/otlp/v1/traces/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/otlp/v1/logs/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
		mux.Handle("OPTIONS /api/{project_id}/otlp/v1/metrics/", middleware.IngestCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	}

	if mountsAPI(role) {
		apiDeps := deps.API
		apiDeps.DB = deps.DB
		apiDeps.Auth = authz
		apiDeps.TokenManager = tokenManager
		if err := api.RegisterRoutesInto(mux, apiDeps); err != nil {
			return nil, err
		}
		if deps.SAMLConfigs != nil && deps.SAMLUsers != nil && deps.AuthStore != nil {
			samlProvider := auth.NewSAMLProvider(deps.SAMLConfigs, deps.SAMLUsers, deps.AuthStore, 30*24*time.Hour)
			auth.RegisterOrgSAMLRoutes(mux, deps.AuthStore, samlProvider, cfg.SessionCookieName, cfg.CSRFCookieName)
		}

		webDeps := deps.Web
		webDeps.DB = deps.DB
		webDeps.Auth = authz
		webHandler, err := web.NewHandler(webDeps)
		if err != nil {
			return nil, err
		}
		webHandler.RegisterRoutes(mux)
	}

	handler := http.Handler(mux)
	if deps.Lifecycle != nil {
		handler = middleware.Maintenance(deps.Lifecycle)(handler)
	}

	// Wrap the entire mux with request logging (outermost middleware).
	return middleware.RequestLogging(handler), nil
}

func NewServer(role string, cfg config.Config, deps Deps) http.Handler {
	handler, err := BuildServer(role, cfg, deps)
	if err != nil {
		panic("http.NewServer " + err.Error())
	}
	return handler
}

func mountsAPI(role string) bool {
	return role == "all" || role == "api"
}

func mountsIngest(role string) bool {
	return role == "all" || role == "ingest"
}

// needsQueue returns true for roles that depend on the async queue.
func needsQueue(role string) bool {
	return role == "all" || role == "ingest" || role == "worker"
}

// needsCache returns true for roles that depend on the cache backend.
func needsCache(role string) bool {
	return role == "all" || role == "api"
}

func OperatorServiceChecks(db, queueDB *sql.DB, cfg config.Config) []sqlite.OperatorCheck {
	checks := []sqlite.OperatorCheck{
		{
			Name: "sqlite",
			Check: func(ctx context.Context) (store.OperatorServiceStatus, error) {
				if err := db.PingContext(ctx); err != nil {
					return store.OperatorServiceStatus{Name: "sqlite", Status: "error", Detail: err.Error()}, nil
				}
				return store.OperatorServiceStatus{Name: "sqlite", Status: "ok", Detail: "reachable"}, nil
			},
		},
		{
			Name: "control-plane",
			Check: func(ctx context.Context) (store.OperatorServiceStatus, error) {
				return operatorPostgresStatus(ctx, "control-plane", cfg.ControlDSN)
			},
		},
		{
			Name: "telemetry-bridge",
			Check: func(ctx context.Context) (store.OperatorServiceStatus, error) {
				return operatorPostgresStatus(ctx, "telemetry-bridge", cfg.TelemetryDSN)
			},
		},
		{
			Name: "jetstream",
			Check: func(_ context.Context) (store.OperatorServiceStatus, error) {
				if !strings.EqualFold(strings.TrimSpace(cfg.AsyncBackend), "jetstream") {
					return store.OperatorServiceStatus{Name: "jetstream", Status: "skipped", Detail: "sqlite backend"}, nil
				}
				if strings.TrimSpace(cfg.NATSURL) == "" {
					return store.OperatorServiceStatus{Name: "jetstream", Status: "error", Detail: "missing nats url"}, nil
				}
				nc, err := nats.Connect(cfg.NATSURL, nats.Name("urgentry-ops-health"))
				if err != nil {
					return store.OperatorServiceStatus{Name: "jetstream", Status: "error", Detail: err.Error()}, nil
				}
				defer nc.Close()
				if err := nc.FlushTimeout(2 * time.Second); err != nil {
					return store.OperatorServiceStatus{Name: "jetstream", Status: "error", Detail: err.Error()}, nil
				}
				return store.OperatorServiceStatus{Name: "jetstream", Status: "ok", Detail: "reachable"}, nil
			},
		},
		{
			Name: "valkey",
			Check: func(ctx context.Context) (store.OperatorServiceStatus, error) {
				if !strings.EqualFold(strings.TrimSpace(cfg.CacheBackend), "valkey") {
					return store.OperatorServiceStatus{Name: "valkey", Status: "skipped", Detail: "sqlite backend"}, nil
				}
				if strings.TrimSpace(cfg.ValkeyURL) == "" {
					return store.OperatorServiceStatus{Name: "valkey", Status: "error", Detail: "missing valkey url"}, nil
				}
				opts, err := redis.ParseURL(cfg.ValkeyURL)
				if err != nil {
					return store.OperatorServiceStatus{Name: "valkey", Status: "error", Detail: err.Error()}, nil
				}
				opts.DisableIdentity = true
				client := redis.NewClient(opts)
				defer client.Close()
				if err := client.Ping(ctx).Err(); err != nil {
					return store.OperatorServiceStatus{Name: "valkey", Status: "error", Detail: err.Error()}, nil
				}
				return store.OperatorServiceStatus{Name: "valkey", Status: "ok", Detail: "reachable"}, nil
			},
		},
	}
	if queueDB != nil {
		checks = append(checks, sqlite.OperatorCheck{
			Name: "queue-sqlite",
			Check: func(ctx context.Context) (store.OperatorServiceStatus, error) {
				if err := queueDB.PingContext(ctx); err != nil {
					return store.OperatorServiceStatus{Name: "queue-sqlite", Status: "error", Detail: err.Error()}, nil
				}
				return store.OperatorServiceStatus{Name: "queue-sqlite", Status: "ok", Detail: "reachable"}, nil
			},
		})
	}
	return checks
}

func operatorPostgresStatus(ctx context.Context, name, dsn string) (store.OperatorServiceStatus, error) {
	if strings.TrimSpace(dsn) == "" {
		return store.OperatorServiceStatus{Name: name, Status: "skipped", Detail: "not configured"}, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return store.OperatorServiceStatus{Name: name, Status: "error", Detail: err.Error()}, nil
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return store.OperatorServiceStatus{Name: name, Status: "error", Detail: "timeout"}, nil
		}
		return store.OperatorServiceStatus{Name: name, Status: "error", Detail: err.Error()}, nil
	}
	return store.OperatorServiceStatus{Name: name, Status: "ok", Detail: "reachable"}, nil
}
