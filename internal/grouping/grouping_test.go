package grouping

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"urgentry/internal/normalize"
)

func fixtureDir() string {
	return filepath.Join("..", "..", "..", "..", "eval", "fixtures")
}

func loadEvent(t *testing.T, name string) *normalize.Event {
	t.Helper()
	path := filepath.Join(fixtureDir(), "grouping", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	evt, err := normalize.Normalize(data)
	if err != nil {
		t.Fatalf("normalize %s: %v", name, err)
	}
	return evt
}

type testCase struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Events      []string `yaml:"events"`
	Expect      string   `yaml:"expect"`
}

type testManifest struct {
	Tests []testCase `yaml:"tests"`
}

func loadManifest(t *testing.T) []testCase {
	t.Helper()
	path := filepath.Join(fixtureDir(), "grouping", "GROUPING_TESTS.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	var m testManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m.Tests
}

func TestAllGroupingCases(t *testing.T) {
	cases := loadManifest(t)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.Events) < 2 && tc.Expect != "uses_fingerprint_as_group_key" {
				t.Skipf("test case %s has <2 events and non-fingerprint expect", tc.Name)
			}

			events := make([]*normalize.Event, len(tc.Events))
			results := make([]Result, len(tc.Events))
			for i, name := range tc.Events {
				events[i] = loadEvent(t, name)
				results[i] = ComputeGrouping(events[i])
			}

			switch tc.Expect {
			case "same_group":
				for i := 1; i < len(results); i++ {
					if results[i].GroupingKey != results[0].GroupingKey {
						t.Errorf("expected same group for %s and %s\n  got: %s\n  want: %s\n  components[0]: %v\n  components[%d]: %v",
							tc.Events[0], tc.Events[i],
							results[i].GroupingKey, results[0].GroupingKey,
							results[0].Components, i, results[i].Components)
					}
				}

			case "different_group":
				for i := 1; i < len(results); i++ {
					if results[i].GroupingKey == results[0].GroupingKey {
						t.Errorf("expected different group for %s and %s, but both got %s\n  components[0]: %v\n  components[%d]: %v",
							tc.Events[0], tc.Events[i], results[0].GroupingKey,
							results[0].Components, i, results[i].Components)
					}
				}

			case "uses_fingerprint_as_group_key":
				if len(events[0].Fingerprint) == 0 {
					t.Error("expected event to have a fingerprint")
				}
				r := results[0]
				if r.GroupingKey == "" {
					t.Error("grouping key is empty")
				}

			default:
				t.Skipf("unknown expect: %q", tc.Expect)
			}
		})
	}
}

func TestGroupingDeterministic(t *testing.T) {
	evt := loadEvent(t, "error_a_v1.json")
	r1 := ComputeGrouping(evt)
	r2 := ComputeGrouping(evt)
	if r1.GroupingKey != r2.GroupingKey {
		t.Errorf("grouping not deterministic: %s vs %s", r1.GroupingKey, r2.GroupingKey)
	}
}

func TestGroupingVersion(t *testing.T) {
	evt := loadEvent(t, "error_a_v1.json")
	r := ComputeGrouping(evt)
	if r.Version != Version {
		t.Errorf("version: got %q want %q", r.Version, Version)
	}
}
