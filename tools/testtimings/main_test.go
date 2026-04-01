package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadPackageTimings(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"urgentry/internal/foo","Output":"ok  \turgentry/internal/foo\t1.25s\n"}`,
		`{"Action":"run","Package":"urgentry/internal/foo","Test":"TestFoo"}`,
		`{"Action":"pass","Package":"urgentry/internal/foo","Elapsed":1.25}`,
		`{"Action":"pass","Package":"urgentry/internal/bar","Elapsed":2.5}`,
		`{"Action":"pass","Package":"urgentry/internal/foo","Test":"TestFoo","Elapsed":0.01}`,
	}, "\n"))

	got, err := readPackageTimingsFromReader(input, false, nil)
	if err != nil {
		t.Fatalf("readPackageTimingsFromReader: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Status == "" || got[1].Status == "" {
		t.Fatalf("expected statuses to be recorded: %#v", got)
	}
}

func TestReadPackageTimingsRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	if _, err := readPackageTimingsFromReader(strings.NewReader("{"), false, nil); err == nil {
		t.Fatal("readPackageTimingsFromReader succeeded on malformed input")
	}
}

func TestReadPackageTimingsEmitsPackageOutput(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"urgentry/internal/foo","Output":"ok  \turgentry/internal/foo\t1.25s\n"}`,
		`{"Action":"output","Package":"urgentry/internal/foo","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}`,
		`{"Action":"pass","Package":"urgentry/internal/foo","Elapsed":1.25}`,
	}, "\n"))

	var output strings.Builder
	if _, err := readPackageTimingsFromReader(input, true, &output); err != nil {
		t.Fatalf("readPackageTimingsFromReader: %v", err)
	}
	if got := output.String(); got != "ok  \turgentry/internal/foo\t1.25s\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestCheckBudgets(t *testing.T) {
	t.Parallel()

	violations := checkBudgets([]packageTiming{
		{Package: "urgentry/internal/foo", Elapsed: 3.5, Status: "pass"},
		{Package: "urgentry/internal/bar", Elapsed: 1.0, Status: "pass"},
	}, []timingBudget{
		{Package: "urgentry/internal/foo", MaxSeconds: 3.0},
		{Package: "urgentry/internal/bar", MaxSeconds: 2.0},
	})
	if len(violations) != 1 {
		t.Fatalf("len(violations) = %d, want 1", len(violations))
	}
	if got := violations[0].Package; got != "urgentry/internal/foo" {
		t.Fatalf("violation package = %q, want urgentry/internal/foo", got)
	}
}

func TestWriteSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timings.json")
	want := timingSummary{
		Packages: []packageTiming{{Package: "urgentry/internal/foo", Status: "pass", Elapsed: 1.25}},
		Slowest:  []packageTiming{{Package: "urgentry/internal/foo", Status: "pass", Elapsed: 1.25}},
		Budgets:  []timingBudget{{Package: "urgentry/internal/foo", MaxSeconds: 2}},
	}
	if err := writeSummary(path, want); err != nil {
		t.Fatalf("writeSummary: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got timingSummary
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(got.Packages) != 1 || got.Packages[0].Package != "urgentry/internal/foo" {
		t.Fatalf("got summary = %#v", got)
	}
}
