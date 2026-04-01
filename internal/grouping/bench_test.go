package grouping

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"urgentry/internal/normalize"
)

func loadAndNormalizeFixture(b *testing.B, subdir, name string) *normalize.Event {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("cannot determine test file location")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", subdir)
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		b.Fatalf("load fixture %s/%s: %v", subdir, name, err)
	}
	evt, err := normalize.Normalize(data)
	if err != nil {
		b.Fatalf("normalize %s/%s: %v", subdir, name, err)
	}
	return evt
}

func benchmarkComputeGroupingFixture(b *testing.B, subdir, name string) {
	b.Helper()
	evt := loadAndNormalizeFixture(b, subdir, name)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeGrouping(evt)
	}
}

func BenchmarkComputeGrouping(b *testing.B) {
	benchmarkComputeGroupingFixture(b, "store", "basic_error.json")
}

func BenchmarkComputeGroupingPythonFull(b *testing.B) {
	benchmarkComputeGroupingFixture(b, "store", "python_full_realistic.json")
}

func BenchmarkComputeGroupingFingerprint(b *testing.B) {
	benchmarkComputeGroupingFixture(b, "grouping", "error_fingerprint.json")
}

func BenchmarkComputeGroupingMessageFallback(b *testing.B) {
	benchmarkComputeGroupingFixture(b, "grouping", "message_a.json")
}
