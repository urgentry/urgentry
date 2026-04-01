package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/proguard"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

func newProGuardTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	router := NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{
		DB:            db,
		ProGuardStore: sqliteProGuardStoreForTest(t, db),
	}))
	return httptest.NewServer(router)
}

func sqliteProGuardStoreForTest(t *testing.T, db *sql.DB) proguard.Store {
	t.Helper()
	return sqlite.NewProGuardStore(db, store.NewMemoryBlobStore())
}

func TestProGuardMappingsUploadListLookup(t *testing.T) {
	ts := newProGuardTestServer(t)
	defer ts.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "proguard.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("proguard mapping content")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.WriteField("uuid", "660f839b-8bfd-580d-9a7c-ea339a6c9867"); err != nil {
		t.Fatalf("WriteField uuid: %v", err)
	}
	if err := writer.WriteField("code_id", "code-123"); err != nil {
		t.Fatalf("WriteField code_id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/0/projects/test-org/test-project/releases/1.2.3/proguard/", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}

	var uploaded proguard.Mapping
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded.UUID == "" || uploaded.ReleaseID != "1.2.3" {
		t.Fatalf("uploaded mapping = %+v", uploaded)
	}

	req, err = http.NewRequest(http.MethodGet, ts.URL+"/api/0/projects/test-org/test-project/releases/1.2.3/proguard/", nil)
	if err != nil {
		t.Fatalf("new list request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var listed []proguard.Mapping
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("list len = %d, want 1", len(listed))
	}

	req, err = http.NewRequest(http.MethodGet, ts.URL+"/api/0/projects/test-org/test-project/releases/1.2.3/proguard/"+uploaded.UUID+"/", nil)
	if err != nil {
		t.Fatalf("new lookup request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET lookup: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup status = %d, want 200", resp.StatusCode)
	}
	var lookedUp proguard.Mapping
	if err := json.NewDecoder(resp.Body).Decode(&lookedUp); err != nil {
		t.Fatalf("decode lookup response: %v", err)
	}
	if lookedUp.UUID != uploaded.UUID || lookedUp.ReleaseID != "1.2.3" {
		t.Fatalf("looked up mapping = %+v, want UUID %q release 1.2.3", lookedUp, uploaded.UUID)
	}
}
