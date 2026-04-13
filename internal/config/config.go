package config

import (
	"os"
	"strconv"
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
	s3Endpoint := firstNonEmpty(os.Getenv("URGENTRY_S3_ENDPOINT"), os.Getenv("URGENTRY_MINIO_URL"))
	s3AccessKey := firstNonEmpty(os.Getenv("URGENTRY_S3_ACCESS_KEY"), os.Getenv("URGENTRY_S3_ACCESS_KEY_ID"))
	s3SecretKey := firstNonEmpty(os.Getenv("URGENTRY_S3_SECRET_KEY"), os.Getenv("URGENTRY_S3_SECRET_ACCESS_KEY"))
	trustedRelaySecret := os.Getenv("URGENTRY_TRUSTED_RELAY_SECRET")
	if trustedRelaySecret == "" {
		if path := os.Getenv("URGENTRY_TRUSTED_RELAY_SECRET_FILE"); path != "" {
			if data, err := os.ReadFile(path); err == nil {
				trustedRelaySecret = string(data)
			}
		}
	}
	samlCertPEM := os.Getenv("URGENTRY_SAML_CERT_PEM")
	if samlCertPEM == "" {
		if path := os.Getenv("URGENTRY_SAML_CERT_PEM_FILE"); path != "" {
			if data, err := os.ReadFile(path); err == nil {
				samlCertPEM = string(data)
			}
		}
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
		S3UseTLS:            envBool("URGENTRY_S3_USE_TLS", false),
		NATSURL:             firstNonEmpty(os.Getenv("URGENTRY_NATS_URL"), os.Getenv("NATS_URL")),
		ValkeyURL:           firstNonEmpty(os.Getenv("URGENTRY_VALKEY_URL"), os.Getenv("REDIS_URL")),
		MetricsToken:        os.Getenv("URGENTRY_METRICS_TOKEN"),
		ProfilingEnabled:    envBool("URGENTRY_PROFILING_ENABLED", false),
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
		IngestRateLimit:     envInt("URGENTRY_INGEST_RATE_LIMIT", 60),
		AppendOnlyIngest:    envBool("URGENTRY_APPEND_ONLY_INGEST", true),
		PipelineQueueSize:   envInt("URGENTRY_PIPELINE_QUEUE_SIZE", 10000),
		PipelineWorkers:     envInt("URGENTRY_PIPELINE_WORKERS", 0),
		ReadHeaderTimeout:   envDuration("URGENTRY_HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:         envDuration("URGENTRY_HTTP_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:        envDuration("URGENTRY_HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:         envDuration("URGENTRY_HTTP_IDLE_TIMEOUT", 60*time.Second),
		SMTPHost:            os.Getenv("URGENTRY_SMTP_HOST"),
		SMTPPort:            envOr("URGENTRY_SMTP_PORT", "587"),
		SMTPFrom:            os.Getenv("URGENTRY_SMTP_FROM"),
		SMTPUser:            os.Getenv("URGENTRY_SMTP_USER"),
		SMTPPass:            os.Getenv("URGENTRY_SMTP_PASS"),
	}
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

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
