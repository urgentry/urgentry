package envelope

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func FuzzParse(f *testing.F) {
	// Seed with valid envelopes from fixtures
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		f.Fatal("cannot determine test file location")
	}
	fixtureDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "eval", "fixtures", "envelopes")

	seeds := []string{
		"single_error.envelope",
		"error_with_attachment.envelope",
		"user_feedback.envelope",
		"multi_item.envelope",
		"with_client_report.envelope",
		"go_sdk_error.envelope",
	}
	for _, name := range seeds {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			f.Logf("skip fixture %s: %v", name, err)
			continue
		}
		f.Add(data)
	}

	// Edge case seeds
	f.Add([]byte{})
	f.Add([]byte("\n"))
	f.Add([]byte("{}"))
	f.Add([]byte("{}\n"))
	f.Add([]byte("{}\n{}\n"))

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic regardless of input
		_, _ = Parse(data)
	})
}
