package sqlite

import (
	"strings"
	"testing"
)

func FuzzSanitizeKeySegment(f *testing.F) {
	for _, seed := range []string{"project", "../escape", `windows\path`, "", "release:v1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := sanitizeKeySegment(raw)
		if strings.TrimSpace(got) == "" {
			t.Fatalf("sanitizeKeySegment(%q) returned empty segment", raw)
		}
		if strings.ContainsAny(got, `/\:`) {
			t.Fatalf("sanitizeKeySegment(%q) returned unsafe segment %q", raw, got)
		}
	})
}
