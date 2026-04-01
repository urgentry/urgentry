package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3BlobConfig struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	Prefix    string
	UseTLS    bool
}

type S3BlobStore struct {
	client *minio.Client
	bucket string
	prefix string
}

func NewS3BlobStore(cfg S3BlobConfig) (*S3BlobStore, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	endpoint, useTLS, err := parseS3Endpoint(cfg.Endpoint, cfg.UseTLS)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       useTLS,
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}
	store := &S3BlobStore{
		client: client,
		bucket: strings.TrimSpace(cfg.Bucket),
		prefix: normalizeBlobPrefix(cfg.Prefix),
	}
	if err := store.ensureBucket(context.Background(), cfg.Region); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *S3BlobStore) Put(ctx context.Context, key string, data []byte) error {
	key, err := s.objectKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: http.DetectContentType(data),
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

func (s *S3BlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	key, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %q: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read object %q: %w", key, err)
	}
	return data, nil
}

func (s *S3BlobStore) Delete(ctx context.Context, key string) error {
	key, err := s.objectKey(key)
	if err != nil {
		return err
	}
	if _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("stat object %q: %w", key, err)
	}
	opts := minio.RemoveObjectOptions{}
	if err := s.client.RemoveObject(ctx, s.bucket, key, opts); err != nil {
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
}

func (s *S3BlobStore) ensureBucket(ctx context.Context, region string) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return fmt.Errorf("create bucket %q: %w", s.bucket, err)
	}
	return nil
}

func (s *S3BlobStore) objectKey(key string) (string, error) {
	key = path.Clean(strings.TrimSpace(key))
	if key == "." || key == "" || strings.HasPrefix(key, "/") || strings.HasPrefix(key, "..") {
		return "", fmt.Errorf("invalid blob key %q", key)
	}
	return s.prefix + key, nil
}

func normalizeBlobPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

func parseS3Endpoint(raw string, useTLS bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("s3 endpoint is required")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", false, fmt.Errorf("parse s3 endpoint: %w", err)
		}
		if parsed.Host == "" {
			return "", false, fmt.Errorf("s3 endpoint host is required")
		}
		switch parsed.Scheme {
		case "http":
			return parsed.Host, false, nil
		case "https":
			return parsed.Host, true, nil
		default:
			return "", false, fmt.Errorf("unsupported s3 endpoint scheme %q", parsed.Scheme)
		}
	}
	return raw, useTLS, nil
}
