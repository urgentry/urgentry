package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func FuzzDecodeJSON(f *testing.F) {
	for _, seed := range []string{`{"ok":true}`, `{"ok":true} extra`, `[1,2,3]`, `{`, ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		req, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
		var body any
		err := decodeJSON(req, &body)
		if err == nil && len(raw) > maxAPIBodySize {
			t.Fatal("decodeJSON accepted an oversized body")
		}
		if errors.Is(err, errRequestBodyTooLarge) && len(raw) <= maxAPIBodySize {
			t.Fatalf("decodeJSON reported oversized body for %d bytes", len(raw))
		}
	})
}
