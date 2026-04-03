package normalize

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzNormalize(f *testing.F) {
	// Seed with valid events from fixtures
	storeDir := filepath.Join(fixtureDir(), "store")

	seeds := []string{
		"basic_error.json",
		"dotnet_error.json",
		"epoch_timestamp.json",
		"go_error.json",
		"java_error.json",
		"js_browser_error.json",
		"js_node_error.json",
		"python_full_realistic.json",
		"ruby_error.json",
		"tags_array_format.json",
	}
	for _, name := range seeds {
		data, err := os.ReadFile(filepath.Join(storeDir, name))
		if err != nil {
			f.Logf("skip fixture %s: %v", name, err)
			continue
		}
		f.Add(data)
	}

	// Edge case seeds
	f.Add([]byte("{}"))
	f.Add([]byte(`{"event_id":"deadbeefdeadbeefdeadbeefdeadbeef"}`))
	f.Add([]byte(`{"event_id":"x","message":"hello"}`))

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic regardless of input
		_, _ = Normalize(data)
	})
}
