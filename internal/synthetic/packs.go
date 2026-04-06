package synthetic

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type GoTestPack struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	AliasOf     string           `json:"alias_of,omitempty"`
	Suites      []GoTestSuite    `json:"suites"`
	Covers      ScenarioCoverage `json:"covers,omitempty"`
}

type GoTestSuite struct {
	Name    string            `json:"name"`
	Package string            `json:"package"`
	Tags    []string          `json:"tags,omitempty"`
	Run     string            `json:"run,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type SuiteRunResult struct {
	Name     string   `json:"name"`
	Package  string   `json:"package"`
	Command  []string `json:"command"`
	Passed   bool     `json:"passed"`
	Output   string   `json:"output,omitempty"`
	Duration string   `json:"duration"`
}

type PackRunResult struct {
	Pack    string           `json:"pack"`
	Passed  bool             `json:"passed"`
	Results []SuiteRunResult `json:"results"`
}

type ScenarioCoverage struct {
	RouteSections     []string `json:"route_sections,omitempty"`
	RoutePrefixes     []string `json:"route_prefixes,omitempty"`
	TelemetrySurfaces []string `json:"telemetry_surfaces,omitempty"`
	ArtifactFamilies  []string `json:"artifact_families,omitempty"`
	Datasets          []string `json:"datasets,omitempty"`
	EntityFamilies    []string `json:"entity_families,omitempty"`
}

type AuditReport struct {
	PayloadCaseCount  int           `json:"payload_case_count"`
	ArtifactCaseCount int           `json:"artifact_case_count"`
	DeepCaseCount     int           `json:"deep_case_count"`
	ScenarioPackCount int           `json:"scenario_pack_count"`
	Coverage          AuditCoverage `json:"coverage"`
}

type AuditCoverage struct {
	RouteSections     CoverageSet `json:"route_sections"`
	TelemetrySurfaces CoverageSet `json:"telemetry_surfaces"`
	ArtifactFamilies  CoverageSet `json:"artifact_families"`
	Datasets          CoverageSet `json:"datasets"`
	EntityFamilies    CoverageSet `json:"entity_families"`
}

type CoverageSet struct {
	Covered   []string `json:"covered"`
	Uncovered []string `json:"uncovered"`
}

func RunPackByName(ctx context.Context, name string) (PackRunResult, error) {
	return RunPackFile(ctx, filepath.Join(ScenariosRoot(), name+".json"))
}

func RunPackFile(ctx context.Context, path string) (PackRunResult, error) {
	var pack GoTestPack
	if err := readJSONFile(path, &pack); err != nil {
		return PackRunResult{}, err
	}
	results := make([]SuiteRunResult, 0, len(pack.Suites))
	for _, suite := range pack.Suites {
		args := []string{"test", suite.Package, "-count=1"}
		if len(suite.Tags) > 0 {
			args = append(args, "-tags="+strings.Join(suite.Tags, ","))
		}
		if suite.Run != "" {
			args = append(args, "-run", suite.Run)
		}
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = ModuleRoot()
		cmd.Env = os.Environ()
		for key, value := range suite.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		started := time.Now()
		output, err := cmd.CombinedOutput()
		result := SuiteRunResult{
			Name:     suite.Name,
			Package:  suite.Package,
			Command:  append([]string{"go"}, args...),
			Passed:   err == nil,
			Output:   string(output),
			Duration: time.Since(started).String(),
		}
		results = append(results, result)
		if err != nil {
			return PackRunResult{Pack: pack.Name, Passed: false, Results: results}, fmt.Errorf("%s failed: %w", suite.Name, err)
		}
	}
	return PackRunResult{Pack: pack.Name, Passed: true, Results: results}, nil
}

func Audit(repoRoot string) (AuditReport, error) {
	outputs, err := GenerateOutputs(repoRoot)
	if err != nil {
		return AuditReport{}, err
	}
	entries, err := os.ReadDir(ScenariosRoot())
	if err != nil {
		return AuditReport{}, err
	}
	scenarioPackCount := 0
	coveredSections := makeSet()
	coveredTelemetry := makeSet()
	coveredArtifacts := makeSet()
	coveredDatasets := makeSet()
	coveredEntityFamilies := makeSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var pack GoTestPack
		if err := readJSONFile(filepath.Join(ScenariosRoot(), entry.Name()), &pack); err != nil {
			return AuditReport{}, err
		}
		if strings.TrimSpace(pack.AliasOf) != "" {
			continue
		}
		scenarioPackCount++
		for _, item := range pack.Covers.RouteSections {
			coveredSections[item] = struct{}{}
		}
		for _, item := range pack.Covers.TelemetrySurfaces {
			coveredTelemetry[item] = struct{}{}
		}
		for _, item := range pack.Covers.ArtifactFamilies {
			coveredArtifacts[item] = struct{}{}
		}
		for _, item := range pack.Covers.Datasets {
			coveredDatasets[item] = struct{}{}
		}
		for _, item := range pack.Covers.EntityFamilies {
			coveredEntityFamilies[item] = struct{}{}
		}
	}

	var surfaces struct {
		RouteMatrix struct {
			SectionCounts []struct {
				Name string `json:"name"`
			} `json:"section_counts"`
		} `json:"route_matrix"`
		TelemetrySurfaces []string `json:"telemetry_surfaces"`
		ArtifactFamilies  []struct {
			Name string `json:"name"`
		} `json:"artifact_families"`
	}
	if err := readJSONFile(filepath.Join(repoRoot, ".synthetic", "surfaces.json"), &surfaces); err != nil {
		return AuditReport{}, err
	}
	var query struct {
		Datasets []struct {
			Name string `json:"name"`
		} `json:"datasets"`
	}
	if err := readJSONFile(filepath.Join(repoRoot, ".synthetic", "query-fields.json"), &query); err != nil {
		return AuditReport{}, err
	}
	var entities struct {
		Families []struct {
			Name string `json:"name"`
		} `json:"families"`
	}
	if err := readJSONFile(filepath.Join(repoRoot, ".synthetic", "entities.json"), &entities); err != nil {
		return AuditReport{}, err
	}

	routeSections := make([]string, 0, len(surfaces.RouteMatrix.SectionCounts))
	for _, item := range surfaces.RouteMatrix.SectionCounts {
		routeSections = append(routeSections, item.Name)
	}
	artifactFamilies := make([]string, 0, len(surfaces.ArtifactFamilies))
	for _, item := range surfaces.ArtifactFamilies {
		artifactFamilies = append(artifactFamilies, item.Name)
	}
	datasets := make([]string, 0, len(query.Datasets))
	for _, item := range query.Datasets {
		datasets = append(datasets, item.Name)
	}
	entityFamilies := make([]string, 0, len(entities.Families))
	for _, item := range entities.Families {
		entityFamilies = append(entityFamilies, item.Name)
	}

	return AuditReport{
		PayloadCaseCount:  len(outputs.Payloads.Cases),
		ArtifactCaseCount: len(outputs.Artifacts.Cases),
		DeepCaseCount:     len(outputs.Deep.Cases),
		ScenarioPackCount: scenarioPackCount,
		Coverage: AuditCoverage{
			RouteSections:     computeCoverage(routeSections, coveredSections),
			TelemetrySurfaces: computeCoverage(surfaces.TelemetrySurfaces, coveredTelemetry),
			ArtifactFamilies:  computeCoverage(artifactFamilies, coveredArtifacts),
			Datasets:          computeCoverage(datasets, coveredDatasets),
			EntityFamilies:    computeCoverage(entityFamilies, coveredEntityFamilies),
		},
	}, nil
}

func computeCoverage(all []string, covered map[string]struct{}) CoverageSet {
	items := append([]string(nil), all...)
	sort.Strings(items)
	result := CoverageSet{}
	for _, item := range items {
		if _, ok := covered[item]; ok {
			result.Covered = append(result.Covered, item)
		} else {
			result.Uncovered = append(result.Uncovered, item)
		}
	}
	return result
}

func makeSet() map[string]struct{} {
	return make(map[string]struct{})
}
