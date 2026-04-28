package api

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadMultipartFileRejectsOversizedPart(t *testing.T) {
	body, contentType := multipartBody(t, "file", "oversize.bin", strings.Repeat("x", 12))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	_, _, err := readMultipartFile(rec, req, "file", 8)
	if !errors.Is(err, errRequestBodyTooLarge) {
		t.Fatalf("readMultipartFile error = %v, want errRequestBodyTooLarge", err)
	}
}

func TestReadMultipartFileAcceptsMaxSizedPart(t *testing.T) {
	body, contentType := multipartBody(t, "file", "max.bin", strings.Repeat("x", 8))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	data, header, err := readMultipartFile(rec, req, "file", 8)
	if err != nil {
		t.Fatalf("readMultipartFile: %v", err)
	}
	if string(data) != strings.Repeat("x", 8) {
		t.Fatalf("data length = %d", len(data))
	}
	if header == nil || header.Filename != "max.bin" {
		t.Fatalf("header = %#v", header)
	}
}

func multipartBody(t *testing.T, field, filename, payload string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(payload)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), writer.FormDataContentType()
}
