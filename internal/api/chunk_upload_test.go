package api

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/store"
)

func TestChunkUpload_Capabilities(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	// GET is not registered; POST with no body returns capabilities.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/organizations/test-org/chunk-upload/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.ContentLength = 0

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST chunk-upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var caps chunkUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if caps.ChunkSize != 8388608 {
		t.Fatalf("chunkSize = %d, want 8388608", caps.ChunkSize)
	}
	if caps.HashAlgorithm != "sha1" {
		t.Fatalf("hashAlgorithm = %q, want sha1", caps.HashAlgorithm)
	}
	if len(caps.Chunks) != 0 {
		t.Fatalf("chunks len = %d, want 0", len(caps.Chunks))
	}
	if len(caps.Accept) == 0 {
		t.Fatal("expected non-empty accept list")
	}
}

func TestChunkUpload_StoresChunks(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	chunk1 := []byte("chunk-data-one")
	chunk2 := []byte("chunk-data-two")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	p1, err := writer.CreateFormFile("file", "chunk0")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := p1.Write(chunk1); err != nil {
		t.Fatalf("write chunk1: %v", err)
	}
	p2, err := writer.CreateFormFile("file", "chunk1")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := p2.Write(chunk2); err != nil {
		t.Fatalf("write chunk2: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/organizations/test-org/chunk-upload/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST chunk-upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result chunkUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Chunks) != 2 {
		t.Fatalf("chunks len = %d, want 2", len(result.Chunks))
	}

	// Verify hashes match SHA1 of chunk data.
	hash1 := sha1.Sum(chunk1)
	hash2 := sha1.Sum(chunk2)
	want1 := hex.EncodeToString(hash1[:])
	want2 := hex.EncodeToString(hash2[:])

	if result.Chunks[0].Hash != want1 {
		t.Fatalf("chunk[0].hash = %q, want %q", result.Chunks[0].Hash, want1)
	}
	if result.Chunks[0].Size != len(chunk1) {
		t.Fatalf("chunk[0].size = %d, want %d", result.Chunks[0].Size, len(chunk1))
	}
	if result.Chunks[0].Offset != 0 {
		t.Fatalf("chunk[0].offset = %d, want 0", result.Chunks[0].Offset)
	}
	if result.Chunks[1].Hash != want2 {
		t.Fatalf("chunk[1].hash = %q, want %q", result.Chunks[1].Hash, want2)
	}
	if result.Chunks[1].Size != len(chunk2) {
		t.Fatalf("chunk[1].size = %d, want %d", result.Chunks[1].Size, len(chunk2))
	}
	if result.Chunks[1].Offset != len(chunk1) {
		t.Fatalf("chunk[1].offset = %d, want %d", result.Chunks[1].Offset, len(chunk1))
	}

	// Verify chunks are in the blob store.
	stored1, err := blobs.Get(t.Context(), "chunks/"+want1)
	if err != nil {
		t.Fatalf("Get chunk1: %v", err)
	}
	if !bytes.Equal(stored1, chunk1) {
		t.Fatalf("stored chunk1 mismatch")
	}
	stored2, err := blobs.Get(t.Context(), "chunks/"+want2)
	if err != nil {
		t.Fatalf("Get chunk2: %v", err)
	}
	if !bytes.Equal(stored2, chunk2) {
		t.Fatalf("stored chunk2 mismatch")
	}
}

func TestChunkUpload_RequiresAuth(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	blobs := store.NewMemoryBlobStore()

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:        db,
		BlobStore: blobs,
	})))
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/organizations/test-org/chunk-upload/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// No Authorization header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST chunk-upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected auth failure, got 200")
	}
}
