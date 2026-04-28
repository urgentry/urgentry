package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env                 string
	HTTPAddr            string
	BaseURL             string
	DataDir             string
	QueueDataDir        string
	BlobBackend         string
	AsyncBackend        string
	CacheBackend        string
	Region              string
	ControlDSN          string
	TelemetryDSN        string
	TelemetryBackend    string
	ColumnarDSN         string
	ColumnarBackend     string
	S3Endpoint          string
	S3Bucket            string
	S3AccessKey         string
	S3SecretKey         string
	S3Region            string
	S3Prefix            string
	S3UseTLS            bool
	NATSURL             string
	ValkeyURL           string
	MetricsToken        string // bearer token for remote /metrics access
	ProfilingEnabled    bool
	ProfilingToken      string // bearer token for remote /debug/pprof and /debug/fgprof access
	SessionCookieName   string
	CSRFCookieName      string
	BootstrapEmail      string
	BootstrapPassword   string
	BootstrapPAT        string
	TrustedRelayOrgSlug string
	TrustedRelayID      string
	TrustedRelaySecret  string
	SAMLOrgSlug         string
	SAMLIdPEntityID     string
	SAMLSSOURL          string
	SAMLCertificatePEM  string
	SAMLSPEntityID      string
	SAMLACSURL          string
	TrustedProxyCIDRs   string
	IngestRateLimit     int
	AppendOnlyIngest    bool
	PipelineQueueSize   int
	PipelineWorkers     int
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	SMTPHost            string
	SMTPPort            string
	SMTPFrom            string
	SMTPUser            string
	SMTPPass            string
}

func Load() Config {
	cfg, _ := load(false)
	return cfg
}

func LoadStrict() (Config, error) {
	return load(true)
}

func load(strict bool) (Config, error) {
	s3Endpoint := firstNonEmpty(os.Getenv("URGENTRY_S3_ENDPOINT"), os.Getenv("URGENTRY_MINIO_URL"))
	s3AccessKey := firstNonEmpty(os.Getenv("URGENTRY_S3_ACCESS_KEY"), os.Getenv("URGENTRY_S3_ACCESS_KEY_ID"))
	s3SecretKey := firstNonEmpty(os.Getenv("URGENTRY_S3_SECRET_KEY"), os.Getenv("URGENTRY_S3_SECRET_ACCESS_KEY"))
	trustedRelaySecret := os.Getenv("URGENTRY_TRUSTED_RELAY_SECRET")
	if trustedRelaySecret == "" {
		secret, err := envFile("URGENTRY_TRUSTED_RELAY_SECRET_FILE", strict)
		if err != nil {
			return Config{}, err
		}
		trustedRelaySecret = secret
	}
	samlCertPEM := os.Getenv("URGENTRY_SAML_CERT_PEM")
	if samlCertPEM == "" {
		cert, err := envFile("URGENTRY_SAML_CERT_PEM_FILE", strict)
		if err != nil {
			return Config{}, err
		}
		samlCertPEM = cert
	}

	s3UseTLS, err := envBoolValue("URGENTRY_S3_USE_TLS", false, strict)
	if err != nil {
		return Config{}, err
	}
	profilingEnabled, err := envBoolValue("URGENTRY_PROFILING_ENABLED", false, strict)
	if err != nil {
		return Config{}, err
	}
	ingestRateLimit, err := envIntValue("URGENTRY_INGEST_RATE_LIMIT", 60, strict)
	if err != nil {
		return Config{}, err
	}
	appendOnlyIngest, err := envBoolValue("URGENTRY_APPEND_ONLY_INGEST", true, strict)
	if err != nil {
		return Config{}, err
	}
	pipelineQueueSize, err := envIntValue("URGENTRY_PIPELINE_QUEUE_SIZE", 10000, strict)
	if err != nil {
		return Config{}, err
	}
	pipelineWorkers, err := envIntValue("URGENTRY_PIPELINE_WORKERS", 0, strict)
	if err != nil {
		return Config{}, err
	}
	readHeaderTimeout, err := envDurationValue("URGENTRY_HTTP_READ_HEADER_TIMEOUT", 5*time.Second, strict)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := envDurationValue("URGENTRY_HTTP_READ_TIMEOUT", 30*time.Second, strict)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := envDurationValue("URGENTRY_HTTP_WRITE_TIMEOUT", 30*time.Second, strict)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := envDurationValue("URGENTRY_HTTP_IDLE_TIMEOUT", 60*time.Second, strict)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Env:                 envOr("URGENTRY_ENV", "development"),
		HTTPAddr:            envOr("URGENTRY_HTTP_ADDR", ":8080"),
		BaseURL:             envOr("URGENTRY_BASE_URL", "http://localhost:8080"),
		DataDir:             os.Getenv("URGENTRY_DATA_DIR"),
		QueueDataDir:        os.Getenv("URGENTRY_QUEUE_DATA_DIR"),
		BlobBackend:         envOr("URGENTRY_BLOB_BACKEND", "file"),
		AsyncBackend:        envOr("URGENTRY_ASYNC_BACKEND", "sqlite"),
		CacheBackend:        envOr("URGENTRY_CACHE_BACKEND", "sqlite"),
		Region:              os.Getenv("URGENTRY_REGION"),
		ControlDSN:          firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")),
		TelemetryDSN:        firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")),
		TelemetryBackend:    envOr("URGENTRY_TELEMETRY_BACKEND", "postgres"),
		ColumnarDSN:         os.Getenv("URGENTRY_COLUMNAR_DATABASE_URL"),
		ColumnarBackend:     os.Getenv("URGENTRY_COLUMNAR_BACKEND"),
		S3Endpoint:          s3Endpoint,
		S3Bucket:            os.Getenv("URGENTRY_S3_BUCKET"),
		S3AccessKey:         s3AccessKey,
		S3SecretKey:         s3SecretKey,
		S3Region:            envOr("URGENTRY_S3_REGION", "us-east-1"),
		S3Prefix:            os.Getenv("URGENTRY_S3_PREFIX"),
		S3UseTLS:            s3UseTLS,
		NATSURL:             firstNonEmpty(os.Getenv("URGENTRY_NATS_URL"), os.Getenv("NATS_URL")),
		ValkeyURL:           firstNonEmpty(os.Getenv("URGENTRY_VALKEY_URL"), os.Getenv("REDIS_URL")),
		MetricsToken:        os.Getenv("URGENTRY_METRICS_TOKEN"),
		ProfilingEnabled:    profilingEnabled,
		ProfilingToken:      os.Getenv("URGENTRY_PROFILING_TOKEN"),
		SessionCookieName:   envOr("URGENTRY_SESSION_COOKIE", "urgentry_session"),
		CSRFCookieName:      envOr("URGENTRY_CSRF_COOKIE", "urgentry_csrf"),
		BootstrapEmail:      os.Getenv("URGENTRY_BOOTSTRAP_EMAIL"),
		BootstrapPassword:   os.Getenv("URGENTRY_BOOTSTRAP_PASSWORD"),
		BootstrapPAT:        os.Getenv("URGENTRY_BOOTSTRAP_PAT"),
		TrustedRelayOrgSlug: os.Getenv("URGENTRY_TRUSTED_RELAY_ORG_SLUG"),
		TrustedRelayID:      os.Getenv("URGENTRY_TRUSTED_RELAY_ID"),
		TrustedRelaySecret:  trustedRelaySecret,
		SAMLOrgSlug:         os.Getenv("URGENTRY_SAML_ORG_SLUG"),
		SAMLIdPEntityID:     os.Getenv("URGENTRY_SAML_IDP_ENTITY_ID"),
		SAMLSSOURL:          os.Getenv("URGENTRY_SAML_SSO_URL"),
		SAMLCertificatePEM:  samlCertPEM,
		SAMLSPEntityID:      os.Getenv("URGENTRY_SAML_SP_ENTITY_ID"),
		SAMLACSURL:          os.Getenv("URGENTRY_SAML_ACS_URL"),
		TrustedProxyCIDRs:   firstNonEmpty(os.Getenv("URGENTRY_TRUSTED_PROXY_CIDRS"), os.Getenv("URGENTRY_TRUSTED_PROXIES")),
		IngestRateLimit:     ingestRateLimit,
		AppendOnlyIngest:    appendOnlyIngest,
		PipelineQueueSize:   pipelineQueueSize,
		PipelineWorkers:     pipelineWorkers,
		ReadHeaderTimeout:   readHeaderTimeout,
		ReadTimeout:         readTimeout,
		WriteTimeout:        writeTimeout,
		IdleTimeout:         idleTimeout,
		SMTPHost:            os.Getenv("URGENTRY_SMTP_HOST"),
		SMTPPort:            envOr("URGENTRY_SMTP_PORT", "587"),
		SMTPFrom:            os.Getenv("URGENTRY_SMTP_FROM"),
		SMTPUser:            os.Getenv("URGENTRY_SMTP_USER"),
		SMTPPass:            os.Getenv("URGENTRY_SMTP_PASS"),
	}, nil
}

func envFile(key string, strict bool) (string, error) {
	path := strings.TrimSpace(os.Getenv(key))
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !strict {
			return "", nil
		}
		return "", fmt.Errorf("read %s %q: %w", key, path, err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIntValue(key string, fallback int, strict bool) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		if !strict {
			return fallback, nil
		}
		return 0, fmt.Errorf("parse %s=%q as int: %w", key, value, err)
	}
	return parsed, nil
}

func envDurationValue(key string, fallback time.Duration, strict bool) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		if !strict {
			return fallback, nil
		}
		return 0, fmt.Errorf("parse %s=%q as duration: %w", key, value, err)
	}
	return parsed, nil
}

func envBoolValue(key string, fallback bool, strict bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		if !strict {
			return fallback, nil
		}
		return false, fmt.Errorf("parse %s=%q as bool: %w", key, value, err)
	}
	return parsed, nil
}
