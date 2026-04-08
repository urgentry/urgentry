package synthetic

import (
	"fmt"
	"os"
	"path/filepath"
)

type GeneratedOutputs struct {
	Payloads  PayloadManifest  `json:"payloads"`
	Artifacts ArtifactManifest `json:"artifacts"`
	Deep      DeepManifest     `json:"deep"`
}

func GenerateOutputs(repoRoot string) (GeneratedOutputs, error) {
	payloads, err := generatePayloadManifest(repoRoot)
	if err != nil {
		return GeneratedOutputs{}, err
	}
	artifacts, err := generateArtifactManifest(repoRoot)
	if err != nil {
		return GeneratedOutputs{}, err
	}
	deep, err := generateDeepManifest(repoRoot)
	if err != nil {
		return GeneratedOutputs{}, err
	}
	return GeneratedOutputs{
		Payloads:  payloads,
		Artifacts: artifacts,
		Deep:      deep,
	}, nil
}

func WriteOutputs(outDir string, outputs GeneratedOutputs) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for name, value := range map[string]any{
		"payloads.json":  outputs.Payloads,
		"artifacts.json": outputs.Artifacts,
		"deep.json":      outputs.Deep,
	} {
		if err := writeJSONFile(filepath.Join(outDir, name), value); err != nil {
			return err
		}
	}
	return nil
}

func CheckOutputs(outDir string, outputs GeneratedOutputs) error {
	for name, value := range map[string]any{
		"payloads.json":  outputs.Payloads,
		"artifacts.json": outputs.Artifacts,
		"deep.json":      outputs.Deep,
	} {
		if err := checkJSONFile(filepath.Join(outDir, name), value); err != nil {
			return fmt.Errorf("%w; rerun `urgentry synthetic generate --write` or `make synthetic-generate`", err)
		}
	}
	return nil
}
