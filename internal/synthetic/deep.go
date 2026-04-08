package synthetic

import (
	"fmt"

	nativefixtures "urgentry/internal/testfixtures/nativecrash"
	profilefixtures "urgentry/internal/testfixtures/profiles"
	replayfixtures "urgentry/internal/testfixtures/replays"
)

type DeepManifest struct {
	SourceFiles []string   `json:"source_files"`
	Cases       []DeepCase `json:"cases"`
}

type DeepCase struct {
	ID                 string   `json:"id"`
	Family             string   `json:"family"`
	Name               string   `json:"name"`
	Release            string   `json:"release,omitempty"`
	Platform           string   `json:"platform,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
	ScenarioHints      []string `json:"scenario_hints,omitempty"`
	ExpectedHighlights []string `json:"expected_highlights,omitempty"`
}

func generateDeepManifest(repoRoot string) (DeepManifest, error) {
	_ = repoRoot
	cases := make([]DeepCase, 0, 16)

	for _, fixture := range replayfixtures.Corpus() {
		spec := fixture.Spec()
		cases = append(cases, DeepCase{
			ID:            "replay/" + fixture.Name,
			Family:        "replay",
			Name:          fixture.Name,
			Release:       spec.Release,
			Platform:      spec.Platform,
			TraceID:       spec.TraceID,
			ScenarioHints: []string{"replays", "query", "privacy"},
			ExpectedHighlights: []string{
				fmt.Sprintf("console_count=%d", fixture.Expected.ConsoleCount),
				fmt.Sprintf("network_count=%d", fixture.Expected.NetworkCount),
				fmt.Sprintf("error_count=%d", fixture.Expected.ErrorCount),
			},
		})
	}

	for _, fixture := range profilefixtures.Corpus() {
		spec := fixture.Spec()
		cases = append(cases, DeepCase{
			ID:            "profile/" + fixture.Name,
			Family:        "profile",
			Name:          fixture.Name,
			Release:       spec.Release,
			Platform:      spec.Platform,
			TraceID:       spec.TraceID,
			ScenarioHints: []string{"profiles", "query"},
			ExpectedHighlights: []string{
				fmt.Sprintf("samples=%d", fixture.Expected.SampleCount),
				fmt.Sprintf("frames=%d", fixture.Expected.FrameCount),
				fmt.Sprintf("functions=%d", fixture.Expected.FunctionCount),
			},
		})
	}

	nativeCorpus, err := nativefixtures.CorpusForLibrary()
	if err != nil {
		return DeepManifest{}, err
	}
	for _, fixture := range nativeCorpus {
		cases = append(cases, DeepCase{
			ID:            "native/" + fixture.Name,
			Family:        "native",
			Name:          fixture.Name,
			Release:       fixture.Release,
			Platform:      fixture.Platform,
			ScenarioHints: []string{"native", "debug-files", "reprocess"},
			ExpectedHighlights: []string{
				fmt.Sprintf("resolved=%d", fixture.WantResolvedFrames),
				fmt.Sprintf("unresolved=%d", fixture.WantUnresolved),
			},
		})
	}

	return DeepManifest{
		SourceFiles: []string{
			"internal/testfixtures/replays/fixtures.go",
			"internal/testfixtures/profiles/fixtures.go",
			"internal/testfixtures/nativecrash/fixtures.go",
			"internal/testfixtures/minidump/minidump.go",
			"internal/testfixtures/nativesym/elf.go",
		},
		Cases: cases,
	}, nil
}
