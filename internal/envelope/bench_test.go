package envelope

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func loadFixtureB(b *testing.B, name string) []byte {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("cannot determine test file location")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "envelopes")
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		b.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func BenchmarkParse(b *testing.B) {
	data := loadFixtureB(b, "single_error.envelope")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Parse(data)
	}
}

func BenchmarkParseMultiItem(b *testing.B) {
	data := loadFixtureB(b, "multi_item.envelope")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Parse(data)
	}
}
