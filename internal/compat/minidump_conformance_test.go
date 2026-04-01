//go:build integration

package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeMDMP is the minimal MDMP header signature (4 bytes).
var fakeMDMP = []byte{0x4D, 0x44, 0x4D, 0x50}

func minidumpURL(srv *compatServer) string {
	return srv.server.URL + "/api/default-project/minidump/?sentry_key=" + srv.projectKey
}

func buildMinidumpMultipart(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("upload_file_minidump", "crash.dmp")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(fakeMDMP); err != nil {
		t.Fatalf("write minidump data: %v", err)
	}

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func TestMinidumpBasicUpload(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	body, ct := buildMinidumpMultipart(t, nil)
	resp := doRequest(t, http.MethodPost, minidumpURL(srv), body, map[string]string{
		"Content-Type": ct,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing event id")
	}
}

func TestMinidumpAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	body, ct := buildMinidumpMultipart(t, nil)
	// POST without sentry_key query param
	resp := doRequest(t, http.MethodPost, srv.server.URL+"/api/default-project/minidump/", body, map[string]string{
		"Content-Type": ct,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMinidumpMissingFile(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Build multipart without upload_file_minidump field
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("event_id", strings.Repeat("a", 32)); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	resp := doRequest(t, http.MethodPost, minidumpURL(srv), &body, map[string]string{
		"Content-Type": writer.FormDataContentType(),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, respBody)
	}
}

func TestMinidumpWithExtra(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	fields := map[string]string{
		"release":     "1.0.0",
		"environment": "production",
	}
	body, ct := buildMinidumpMultipart(t, fields)
	resp := doRequest(t, http.MethodPost, minidumpURL(srv), body, map[string]string{
		"Content-Type": ct,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] == "" {
		t.Fatal("response missing event id")
	}
}

func TestMinidumpContentType(t *testing.T) {
	srv := newCompatServer(t, compatOptions{})
	defer srv.close()

	// Send application/json instead of multipart/form-data
	payload := []byte(`{"event_id":"test"}`)
	resp := doRequest(t, http.MethodPost, minidumpURL(srv), bytes.NewReader(payload), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMinidumpConcurrent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{queueSize: 200})
	defer srv.close()

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, ct := buildMinidumpMultipart(t, nil)
			resp := doRequest(t, http.MethodPost, minidumpURL(srv), body, map[string]string{
				"Content-Type": ct,
			})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(resp.Body)
				errs <- &minidumpError{status: resp.StatusCode, body: string(respBody)}
				return
			}
			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				errs <- err
				return
			}
			if result["id"] == "" {
				errs <- &minidumpError{status: 200, body: "missing event id"}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent upload failed: %v", err)
	}
}

type minidumpError struct {
	status int
	body   string
}

func (e *minidumpError) Error() string {
	return "status " + http.StatusText(e.status) + ": " + e.body
}
