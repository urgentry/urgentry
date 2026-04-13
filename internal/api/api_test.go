package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urgentry/internal/httputil"
)

func TestPaginate(t *testing.T) {
	items := make([]int, 250)
	for i := range items {
		items[i] = i
	}

	t.Run("first page", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		page := Paginate(w, r, items)
		if len(page) != defaultPageSize {
			t.Fatalf("expected %d items, got %d", defaultPageSize, len(page))
		}
		link := w.Header().Get("Link")
		if link == "" {
			t.Fatal("expected Link header")
		}
		if !containsStr(link, `rel="next"`) {
			t.Fatalf("expected next link in %q", link)
		}
		if !containsStr(link, `results="true"`) {
			t.Fatalf("expected results=true in next link: %q", link)
		}
	})

	t.Run("with cursor", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/test?cursor=200", nil)
		w := httptest.NewRecorder()
		page := Paginate(w, r, items)
		if len(page) != 50 {
			t.Fatalf("expected 50 items, got %d", len(page))
		}
		link := w.Header().Get("Link")
		if !containsStr(link, `rel="previous"; results="true"`) {
			t.Fatalf("expected previous results in: %q", link)
		}
		if !containsStr(link, `rel="next"; results="false"`) {
			t.Fatalf("expected next results to be exhausted in: %q", link)
		}
	})

	t.Run("cursor past end", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/test?cursor=999", nil)
		w := httptest.NewRecorder()
		page := Paginate(w, r, items)
		if len(page) != 0 {
			t.Fatalf("expected 0 items, got %d", len(page))
		}
	})
}

func TestSetPaginationHeadersPreservesQueryParams(t *testing.T) {
	items := make([]int, 11)
	for i := range items {
		items[i] = i
	}

	r := httptest.NewRequest(http.MethodGet, "/test?per_page=10&query=is%3Aunresolved", nil)
	w := httptest.NewRecorder()

	page := SetPaginationHeaders(w, r, items, PaginationOpts{Offset: 20, Limit: 10})
	if len(page) != 10 {
		t.Fatalf("expected 10 items, got %d", len(page))
	}

	link := w.Header().Get("Link")
	if link == "" {
		t.Fatal("expected Link header")
	}
	if !containsStr(link, `rel="previous"; results="true"; cursor="0:10:1"`) {
		t.Fatalf("expected previous cursor in %q", link)
	}
	if !containsStr(link, `rel="next"; results="true"; cursor="0:30:0"`) {
		t.Fatalf("expected next cursor in %q", link)
	}
	if !containsStr(link, `per_page=10`) || !containsStr(link, `query=is%3Aunresolved`) {
		t.Fatalf("expected query params in %q", link)
	}
}

func TestNewRouterRequiresAuthorizer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when authorizer is missing")
		}
	}()
	NewRouter(Dependencies{DB: openTestSQLite(t)})
}

func TestBuildRouterRequiresAuthorizer(t *testing.T) {
	_, err := BuildRouter(Dependencies{DB: openTestSQLite(t)})
	if err == nil || !strings.Contains(err.Error(), "authorizer") {
		t.Fatalf("BuildRouter error = %v, want authorizer requirement", err)
	}
}

func TestValidateDependenciesRequiresAuthorizer(t *testing.T) {
	err := ValidateDependencies(Dependencies{DB: openTestSQLite(t)})
	if err == nil || !strings.Contains(err.Error(), "authorizer") {
		t.Fatalf("ValidateDependencies error = %v, want authorizer requirement", err)
	}
}

func TestPathParam(t *testing.T) {
	// PathParam delegates to r.PathValue which is set by the ServeMux.
	// We can test it via the router in integration tests.
	// Here just ensure it doesn't panic on an empty request.
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	val := PathParam(r, "nonexistent")
	if val != "" {
		t.Fatalf("expected empty string, got %q", val)
	}
}

func decodeAPIError(t *testing.T, resp *http.Response) httputil.APIErrorBody {
	t.Helper()
	defer resp.Body.Close()

	var body httputil.APIErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode api error: %v", err)
	}
	return body
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
