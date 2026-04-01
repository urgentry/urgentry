package config

import (
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
	t.Setenv("URGENTRY_PIPELINE_QUEUE_SIZE", "77")
	t.Setenv("URGENTRY_PIPELINE_WORKERS", "3")
	t.Setenv("URGENTRY_HTTP_READ_HEADER_TIMEOUT", "7s")
	t.Setenv("URGENTRY_HTTP_READ_TIMEOUT", "11s")
	t.Setenv("URGENTRY_HTTP_WRITE_TIMEOUT", "13s")
	t.Setenv("URGENTRY_HTTP_IDLE_TIMEOUT", "17s")

	cfg := Load()
	if cfg.DataDir != "/tmp/urgentry-test" {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "/tmp/urgentry-test")
	}
	if cfg.PipelineQueueSize != 77 {
		t.Fatalf("PipelineQueueSize = %d, want 77", cfg.PipelineQueueSize)
	}
	if cfg.PipelineWorkers != 3 {
		t.Fatalf("PipelineWorkers = %d, want 3", cfg.PipelineWorkers)
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
