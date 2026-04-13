package store

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3BlobStorePutGetDelete(t *testing.T) {
	t.Parallel()

	store := newTestS3BlobStore(t, "replays")
	ctx := context.Background()
	if err := store.Put(ctx, "project-1/replay.json", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, err := store.Get(ctx, "project-1/replay.json")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("Get() = %q", got)
	}
	if err := store.Delete(ctx, "project-1/replay.json"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(ctx, "project-1/replay.json"); err != ErrNotFound {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestS3BlobStoreRejectsUnsafeKeys(t *testing.T) {
	t.Parallel()

	store := newTestS3BlobStore(t, "")
	if _, err := store.objectKey("../secret"); err == nil {
		t.Fatal("objectKey() expected error for traversal key")
	}
	if _, err := store.objectKey("/absolute"); err == nil {
		t.Fatal("objectKey() expected error for absolute key")
	}
}

func TestS3BlobStoreUsesPrefix(t *testing.T) {
	t.Parallel()

	store := newTestS3BlobStore(t, "urgentry/blobs")
	key, err := store.objectKey("events/a.json")
	if err != nil {
		t.Fatalf("objectKey() error = %v", err)
	}
	if key != "urgentry/blobs/events/a.json" {
		t.Fatalf("objectKey() = %q", key)
	}
}

func TestS3BlobStoreAcceptsURLEndpoint(t *testing.T) {
	t.Parallel()

	server := newFakeS3Server(t)
	t.Cleanup(server.Close)

	store, err := NewS3BlobStore(S3BlobConfig{
		Endpoint:  server.URL,
		Bucket:    "urgentry-test",
		AccessKey: "minio",
		SecretKey: "minio123",
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatalf("NewS3BlobStore() error = %v", err)
	}
	if err := store.Put(context.Background(), "events/a.json", []byte("ok")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
}

func newTestS3BlobStore(t *testing.T, prefix string) *S3BlobStore {
	t.Helper()

	server := newFakeS3Server(t)
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse(server.URL) error = %v", err)
	}

	store, err := NewS3BlobStore(S3BlobConfig{
		Endpoint:  serverURL.Host,
		Bucket:    "urgentry-test",
		AccessKey: "minio",
		SecretKey: "minio123",
		Region:    "us-east-1",
		Prefix:    prefix,
		UseTLS:    false,
	})
	if err != nil {
		t.Fatalf("NewS3BlobStore() error = %v", err)
	}
	return store
}

func newFakeS3Server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(&fakeS3Server{
		buckets: make(map[string]map[string]fakeS3Object),
	})
}

type fakeS3Server struct {
	mu      sync.Mutex
	buckets map[string]map[string]fakeS3Object
}

type fakeS3Object struct {
	body         []byte
	lastModified time.Time
}

func (s *fakeS3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := splitS3Path(r.URL.Path)
	if bucket == "" {
		http.NotFound(w, r)
		return
	}
	if key == "" {
		s.handleBucket(w, r, bucket)
		return
	}
	s.handleObject(w, r, bucket, key)
}

func splitS3Path(path string) (string, string) {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func (s *fakeS3Server) handleBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		if _, ok := s.buckets[bucket]; !ok {
			s.buckets[bucket] = make(map[string]fakeS3Object)
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodHead, http.MethodGet:
		if _, ok := s.buckets[bucket]; !ok {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchBucket")
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *fakeS3Server) handleObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	objects, ok := s.buckets[bucket]
	if !ok {
		writeFakeS3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}

	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "<Error><Code>InternalError</Code></Error>")
			return
		}
		body, err = decodeFakeS3Body(r, body)
		if err != nil {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<Error><Code>InvalidRequest</Code></Error>")
			return
		}
		objects[key] = fakeS3Object{
			body:         body,
			lastModified: time.Now().UTC(),
		}
		w.Header().Set("ETag", "\"fake-etag\"")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		object, ok := objects[key]
		if !ok {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		setFakeS3ObjectHeaders(w, object)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(object.body)
	case http.MethodHead:
		object, ok := objects[key]
		if !ok {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		setFakeS3ObjectHeaders(w, object)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if _, ok := objects[key]; !ok {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		delete(objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeFakeS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<Error><Code>"+code+"</Code></Error>")
}

func setFakeS3ObjectHeaders(w http.ResponseWriter, object fakeS3Object) {
	w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
	w.Header().Set("ETag", "\"fake-etag\"")
	w.Header().Set("Last-Modified", object.lastModified.Format(http.TimeFormat))
}

func decodeFakeS3Body(r *http.Request, body []byte) ([]byte, error) {
	if !strings.Contains(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") &&
		!strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		return body, nil
	}

	reader := bufio.NewReader(bytes.NewReader(body))
	var decoded bytes.Buffer
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		sizeHex := line
		if idx := strings.IndexByte(line, ';'); idx >= 0 {
			sizeHex = line[:idx]
		}
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return decoded.Bytes(), nil
		}
		chunk := make([]byte, size)
		if _, err := io.ReadFull(reader, chunk); err != nil {
			return nil, err
		}
		decoded.Write(chunk)
		if _, err := reader.Discard(2); err != nil {
			return nil, err
		}
	}
}
