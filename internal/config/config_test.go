package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadIngestRateLimit(t *testing.T) {
	t.Setenv("URGENTRY_INGEST_RATE_LIMIT", "17")

	cfg := Load()
	if cfg.IngestRateLimit != 17 {
		t.Fatalf("IngestRateLimit = %d, want 17", cfg.IngestRateLimit)
	}
}

func TestLoadIngestRateLimitFallback(t *testing.T) {
	t.Setenv("URGENTRY_INGEST_RATE_LIMIT", "not-an-int")

	cfg := Load()
	if cfg.IngestRateLimit != 60 {
		t.Fatalf("IngestRateLimit = %d, want 60", cfg.IngestRateLimit)
	}
}

func TestLoadPipelineAndTimeoutConfig(t *testing.T) {
	t.Setenv("URGENTRY_DATA_DIR", "/tmp/urgentry-test")
	t.Setenv("URGENTRY_QUEUE_DATA_DIR", "/tmp/urgentry-queue")
	t.Setenv("URGENTRY_PIPELINE_QUEUE_SIZE", "77")
	t.Setenv("URGENTRY_PIPELINE_WORKERS", "3")
	t.Setenv("URGENTRY_APPEND_ONLY_INGEST", "true")
	t.Setenv("URGENTRY_HTTP_READ_HEADER_TIMEOUT", "7s")
	t.Setenv("URGENTRY_HTTP_READ_TIMEOUT", "11s")
	t.Setenv("URGENTRY_HTTP_WRITE_TIMEOUT", "13s")
	t.Setenv("URGENTRY_HTTP_IDLE_TIMEOUT", "17s")

	cfg := Load()
	if cfg.DataDir != "/tmp/urgentry-test" {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "/tmp/urgentry-test")
	}
	if cfg.QueueDataDir != "/tmp/urgentry-queue" {
		t.Fatalf("QueueDataDir = %q, want %q", cfg.QueueDataDir, "/tmp/urgentry-queue")
	}
	if cfg.PipelineQueueSize != 77 {
		t.Fatalf("PipelineQueueSize = %d, want 77", cfg.PipelineQueueSize)
	}
	if cfg.PipelineWorkers != 3 {
		t.Fatalf("PipelineWorkers = %d, want 3", cfg.PipelineWorkers)
	}
	if !cfg.AppendOnlyIngest {
		t.Fatal("AppendOnlyIngest = false, want true")
	}
	if cfg.ReadHeaderTimeout != 7*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 7s", cfg.ReadHeaderTimeout)
	}
	if cfg.ReadTimeout != 11*time.Second {
		t.Fatalf("ReadTimeout = %s, want 11s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 13*time.Second {
		t.Fatalf("WriteTimeout = %s, want 13s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 17*time.Second {
		t.Fatalf("IdleTimeout = %s, want 17s", cfg.IdleTimeout)
	}
}

func TestLoadAppendOnlyIngestDefaultsTrue(t *testing.T) {
	cfg := Load()
	if !cfg.AppendOnlyIngest {
		t.Fatal("AppendOnlyIngest = false, want true by default")
	}
}

func TestLoadAppendOnlyIngestCanBeDisabled(t *testing.T) {
	t.Setenv("URGENTRY_APPEND_ONLY_INGEST", "false")

	cfg := Load()
	if cfg.AppendOnlyIngest {
		t.Fatal("AppendOnlyIngest = true, want false when explicitly disabled")
	}
}

func TestLoadPipelineQueueSizeFallback(t *testing.T) {
	t.Setenv("URGENTRY_PIPELINE_QUEUE_SIZE", "not-an-int")

	cfg := Load()
	if cfg.PipelineQueueSize != 10000 {
		t.Fatalf("PipelineQueueSize = %d, want 10000", cfg.PipelineQueueSize)
	}
}

func TestLoadDurationFallback(t *testing.T) {
	t.Setenv("URGENTRY_HTTP_READ_TIMEOUT", "not-a-duration")

	cfg := Load()
	if cfg.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %s, want 30s", cfg.ReadTimeout)
	}
}

func TestLoadBlobConfig(t *testing.T) {
	t.Setenv("URGENTRY_BLOB_BACKEND", "s3")
	t.Setenv("URGENTRY_REGION", "us-west-2")
	t.Setenv("URGENTRY_S3_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("URGENTRY_S3_BUCKET", "urgentry")
	t.Setenv("URGENTRY_S3_ACCESS_KEY", "minio")
	t.Setenv("URGENTRY_S3_SECRET_KEY", "minio123")
	t.Setenv("URGENTRY_S3_REGION", "us-west-2")
	t.Setenv("URGENTRY_S3_PREFIX", "artifacts")
	t.Setenv("URGENTRY_S3_USE_TLS", "true")

	cfg := Load()
	if cfg.BlobBackend != "s3" {
		t.Fatalf("BlobBackend = %q, want s3", cfg.BlobBackend)
	}
	if cfg.Region != "us-west-2" {
		t.Fatalf("Region = %q, want us-west-2", cfg.Region)
	}
	if cfg.S3Endpoint != "127.0.0.1:9000" {
		t.Fatalf("S3Endpoint = %q", cfg.S3Endpoint)
	}
	if cfg.S3Bucket != "urgentry" {
		t.Fatalf("S3Bucket = %q", cfg.S3Bucket)
	}
	if cfg.S3AccessKey != "minio" || cfg.S3SecretKey != "minio123" {
		t.Fatalf("unexpected S3 credentials: %q %q", cfg.S3AccessKey, cfg.S3SecretKey)
	}
	if cfg.S3Region != "us-west-2" {
		t.Fatalf("S3Region = %q", cfg.S3Region)
	}
	if cfg.S3Prefix != "artifacts" {
		t.Fatalf("S3Prefix = %q", cfg.S3Prefix)
	}
	if !cfg.S3UseTLS {
		t.Fatal("S3UseTLS = false, want true")
	}
}

func TestLoadStrictFailsForUnreadableSecretFile(t *testing.T) {
	t.Setenv("URGENTRY_TRUSTED_RELAY_SECRET_FILE", filepath.Join(t.TempDir(), "missing-secret"))

	_, err := LoadStrict()
	if err == nil || !strings.Contains(err.Error(), "URGENTRY_TRUSTED_RELAY_SECRET_FILE") {
		t.Fatalf("LoadStrict error = %v, want trusted relay secret file error", err)
	}
}

func TestLoadStrictFailsForUnreadableSAMLCertFile(t *testing.T) {
	t.Setenv("URGENTRY_SAML_CERT_PEM_FILE", filepath.Join(t.TempDir(), "missing-cert"))

	_, err := LoadStrict()
	if err == nil || !strings.Contains(err.Error(), "URGENTRY_SAML_CERT_PEM_FILE") {
		t.Fatalf("LoadStrict error = %v, want SAML cert file error", err)
	}
}

func TestLoadStrictTrimsSecretFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("super-secret\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	t.Setenv("URGENTRY_TRUSTED_RELAY_SECRET_FILE", path)

	cfg, err := LoadStrict()
	if err != nil {
		t.Fatalf("LoadStrict: %v", err)
	}
	if cfg.TrustedRelaySecret != "super-secret" {
		t.Fatalf("TrustedRelaySecret = %q, want trimmed secret", cfg.TrustedRelaySecret)
	}
}

func TestLoadStrictFailsForInvalidScalars(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "int", key: "URGENTRY_PIPELINE_QUEUE_SIZE", val: "not-an-int"},
		{name: "duration", key: "URGENTRY_HTTP_READ_TIMEOUT", val: "not-a-duration"},
		{name: "bool", key: "URGENTRY_APPEND_ONLY_INGEST", val: "definitely"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.val)
			_, err := LoadStrict()
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("LoadStrict error = %v, want error mentioning %s", err, tt.key)
			}
		})
	}
}

func TestLoadTrustedProxyCIDRs(t *testing.T) {
	t.Setenv("URGENTRY_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")

	cfg, err := LoadStrict()
	if err != nil {
		t.Fatalf("LoadStrict: %v", err)
	}
	if cfg.TrustedProxyCIDRs != "10.0.0.0/8" {
		t.Fatalf("TrustedProxyCIDRs = %q", cfg.TrustedProxyCIDRs)
	}
}
