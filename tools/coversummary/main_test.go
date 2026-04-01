package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSummary(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(strings.Join([]string{
		"mode: set",
		"internal/foo.go:1.1,2.2 2 1",
		"internal/bar.go:3.1,4.2 3 0",
	}, "\n"))

	got, err := parseSummary(input)
	if err != nil {
		t.Fatalf("parseSummary: %v", err)
	}
	if got.mode != "set" {
		t.Fatalf("mode = %q, want set", got.mode)
	}
	if got.statements != 5 {
		t.Fatalf("statements = %d, want 5", got.statements)
	}
	if got.covered != 2 {
		t.Fatalf("covered = %d, want 2", got.covered)
	}
}

func TestParseSummaryLongLine(t *testing.T) {
	t.Parallel()

	longPath := strings.Repeat("pkg/", 20000) + "coverage.go"
	input := strings.NewReader("mode: atomic\n" + longPath + ":1.1,2.2 7 1")

	got, err := parseSummary(input)
	if err != nil {
		t.Fatalf("parseSummary: %v", err)
	}
	if got.mode != "atomic" {
		t.Fatalf("mode = %q, want atomic", got.mode)
	}
	if got.statements != 7 || got.covered != 7 {
		t.Fatalf("summary = %+v, want 7 covered statements", got)
	}
}

func TestParseSummaryLargeProfile(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("mode: count\n")
	var wantCovered int64
	var wantStatements int64
	for i := range 20000 {
		statements := (i % 7) + 1
		count := i % 3
		fmt.Fprintf(&b, "pkg/file_%d.go:1.1,2.2 %d %d\n", i, statements, count)
		wantStatements += int64(statements)
		if count > 0 {
			wantCovered += int64(statements)
		}
	}

	got, err := parseSummary(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("parseSummary: %v", err)
	}
	if got.mode != "count" {
		t.Fatalf("mode = %q, want count", got.mode)
	}
	if got.statements != wantStatements || got.covered != wantCovered {
		t.Fatalf("summary = %+v, want covered=%d statements=%d", got, wantCovered, wantStatements)
	}
}

func TestParseSummaryRejectsMalformedLine(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("mode: set\ninternal/foo.go:1.1,2.2 not-a-number 1\n")
	if _, err := parseSummary(input); err == nil {
		t.Fatal("parseSummary succeeded on malformed input")
	}
}

func TestParseSummaryRejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("not-mode: set\ninternal/foo.go:1.1,2.2 1 1\n")
	if _, err := parseSummary(input); err == nil {
		t.Fatal("parseSummary succeeded on malformed header")
	}
}

func TestCheckThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		actual  float64
		minPct  float64
		wantErr bool
	}{
		{"disabled when min is zero", 50.0, 0, false},
		{"passes above threshold", 60.0, 58.0, false},
		{"passes at exact threshold", 58.0, 58.0, false},
		{"fails below threshold", 57.9, 58.0, true},
		{"fails at zero coverage", 0.0, 58.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkThreshold(tt.actual, tt.minPct)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkThreshold(%.1f, %.1f) error = %v, wantErr = %v", tt.actual, tt.minPct, err, tt.wantErr)
			}
		})
	}
}
