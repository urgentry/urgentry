package normalize

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixtureB(b *testing.B, subdir, name string) []byte {
	b.Helper()
	path := filepath.Join(fixtureDir(), subdir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("load fixture %s/%s: %v", subdir, name, err)
	}
	return data
}

func benchmarkNormalizeFixture(b *testing.B, subdir, name string) {
	b.Helper()
	data := loadFixtureB(b, subdir, name)
	benchmarkNormalizeData(b, data)
}

func benchmarkNormalizeData(b *testing.B, data []byte) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Normalize(data)
	}
}

func BenchmarkNormalize(b *testing.B) {
	benchmarkNormalizeFixture(b, "store", "basic_error.json")
}

func BenchmarkNormalizePythonFull(b *testing.B) {
	benchmarkNormalizeFixture(b, "store", "python_full_realistic.json")
}

func BenchmarkNormalizeEpochTimestamp(b *testing.B) {
	benchmarkNormalizeFixture(b, "store", "epoch_timestamp.json")
}

func BenchmarkNormalizeArrayTags(b *testing.B) {
	benchmarkNormalizeFixture(b, "store", "tags_array_format.json")
}

func BenchmarkNormalizeObjectMessage(b *testing.B) {
	benchmarkNormalizeData(b, []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","message":{"formatted":"hello from object message"},"platform":"go","level":"error"}`))
}
