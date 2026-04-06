package synthetic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func ModuleRoot() string {
	return filepath.Join(RepoRoot(), "apps", "urgentry")
}

func SyntheticRoot() string {
	return filepath.Join(RepoRoot(), ".synthetic")
}

func GeneratedRoot() string {
	return filepath.Join(SyntheticRoot(), "generated")
}

func ScenariosRoot() string {
	return filepath.Join(SyntheticRoot(), "scenarios")
}

func relRepoPath(path string) string {
	rel, err := filepath.Rel(RepoRoot(), path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func readJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	return data, nil
}

func checkJSONFile(path string, value any) error {
	expected, err := marshalJSON(value)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("%s is stale", filepath.ToSlash(path))
	}
	return nil
}
