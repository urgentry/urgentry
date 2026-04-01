package normalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenDir returns the path to the normalization golden fixtures.
func goldenDir() string {
	return filepath.Join(fixtureDir(), "normalization")
}

func TestGoldenSnapshots(t *testing.T) {
	fixtures := []string{
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

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			raw := loadFixture(t, "store", name)
			evt, err := Normalize(raw)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}

			// Marshal with stable JSON ordering
			got, err := json.MarshalIndent(evt, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			goldenName := strings.TrimSuffix(name, ".json") + ".expected.json"
			goldenPath := filepath.Join(goldenDir(), goldenName)

			// If golden file doesn't exist, create it (update mode)
			if _, err := os.Stat(goldenPath); os.IsNotExist(err) {
				if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				t.Logf("created golden file: %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			// Re-marshal both sides to normalize JSON key ordering
			var gotObj, wantObj any
			if err := json.Unmarshal(got, &gotObj); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal(want, &wantObj); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}

			gotStable, _ := json.Marshal(gotObj)
			wantStable, _ := json.Marshal(wantObj)

			if string(gotStable) != string(wantStable) {
				t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s",
					name, string(got), string(want))
			}
		})
	}
}
