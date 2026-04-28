package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(`{"body":"`+strings.Repeat("x", maxAPIBodySize)+`"}`))
	var body map[string]string

	err := decodeJSON(req, &body)
	if !errors.Is(err, errRequestBodyTooLarge) {
		t.Fatalf("decodeJSON error = %v, want errRequestBodyTooLarge", err)
	}
}

func TestDecodeJSONRejectsTrailingBytes(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true} trailing`))
	var body map[string]bool

	err := decodeJSON(req, &body)
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("decodeJSON error = %v, want trailing data error", err)
	}
}

func TestDecodeJSONRejectsMultipleValues(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(`{"one":1}{"two":2}`))
	var body map[string]int

	err := decodeJSON(req, &body)
	if err == nil {
		t.Fatal("decodeJSON accepted multiple JSON values")
	}
}
