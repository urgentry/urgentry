package payloads

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAllDeterministic(t *testing.T) {
	first, err := All()
	if err != nil {
		t.Fatalf("All first: %v", err)
	}
	second, err := All()
	if err != nil {
		t.Fatalf("All second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("len(first)=%d len(second)=%d", len(first), len(second))
	}
	for i := range first {
		leftManifest, err := json.Marshal(first[i].Manifest)
		if err != nil {
			t.Fatalf("marshal first manifest: %v", err)
		}
		rightManifest, err := json.Marshal(second[i].Manifest)
		if err != nil {
			t.Fatalf("marshal second manifest: %v", err)
		}
		if !bytes.Equal(leftManifest, rightManifest) {
			t.Fatalf("manifest mismatch at %d: %s vs %s", i, leftManifest, rightManifest)
		}
		if !bytes.Equal(first[i].Body, second[i].Body) {
			t.Fatalf("body mismatch at %d (%s)", i, first[i].Manifest.Name)
		}
	}
}

func TestAllUniqueNames(t *testing.T) {
	items, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Manifest.Name == "" {
			t.Fatal("empty manifest name")
		}
		if _, ok := seen[item.Manifest.Name]; ok {
			t.Fatalf("duplicate manifest name %q", item.Manifest.Name)
		}
		seen[item.Manifest.Name] = struct{}{}
	}
}

func TestGeneratedJSONPayloadsAreValidJSON(t *testing.T) {
	for _, surface := range []Surface{SurfaceStore, SurfaceOTLPLogs, SurfaceOTLPTraces, SurfaceSecurityReport} {
		items, err := BySurface(surface)
		if err != nil {
			t.Fatalf("BySurface(%s): %v", surface, err)
		}
		for _, item := range items {
			if item.Manifest.Compression != CompressionNone {
				continue
			}
			if item.Manifest.ContentType == "application/x-sentry-envelope" {
				continue
			}
			var payload any
			if err := json.Unmarshal(item.Body, &payload); err != nil {
				t.Fatalf("%s JSON invalid: %v", item.Manifest.Name, err)
			}
		}
	}
}

func TestNegativeCorpusUses400Expectations(t *testing.T) {
	items, err := BySurface(SurfaceNegative)
	if err != nil {
		t.Fatalf("BySurface(negative): %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected negative corpus")
	}
	for _, item := range items {
		if item.Manifest.ExpectStatusCode != httpStatusBadRequest {
			t.Fatalf("%s expect status=%d want %d", item.Manifest.Name, item.Manifest.ExpectStatusCode, httpStatusBadRequest)
		}
	}
}
