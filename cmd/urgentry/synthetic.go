package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"urgentry/internal/synthetic"
	"urgentry/internal/synthetic/payloads"
)

func runSynthetic(args []string) {
	if len(args) == 0 {
		syntheticUsage()
	}
	switch args[0] {
	case "generate":
		runSyntheticGenerate(args[1:])
	case "audit":
		runSyntheticAudit(args[1:])
	case "run":
		runSyntheticRun(args[1:])
	default:
		syntheticUsage()
	}
}

func runSyntheticGenerate(args []string) {
	fs := flag.NewFlagSet("synthetic generate", flag.ExitOnError)
	surface := fs.String("surface", "all", "payload surface: all|store|envelope|otlp_traces|otlp_logs|security_report|negative")
	outDir := fs.String("out-dir", filepath.Join(defaultRepoRoot(), ".synthetic", "generated", "structured"), "directory for generated payload corpus manifests")
	manifestDir := fs.String("manifest-dir", filepath.Join(defaultRepoRoot(), ".synthetic", "generated"), "directory for generated payload/artifact/deep manifest outputs")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse synthetic generate flags")
	}

	var (
		items []payloads.Payload
		err   error
	)
	if strings.EqualFold(*surface, "all") {
		items, err = payloads.All()
	} else {
		items, err = payloads.BySurface(payloads.Surface(*surface))
	}
	if err != nil {
		log.Fatal().Err(err).Msg("generate synthetic payload corpus")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal().Err(err).Msg("mkdir synthetic output dir")
	}

	manifests := make([]payloads.Manifest, 0, len(items))
	for _, item := range items {
		manifests = append(manifests, item.Manifest)
		bodyPath := filepath.Join(*outDir, item.Manifest.Name+bodyExtension(item.Manifest.ContentType, item.Manifest.Compression))
		if err := os.WriteFile(bodyPath, item.Body, 0o644); err != nil {
			log.Fatal().Err(err).Str("path", bodyPath).Msg("write synthetic payload body")
		}
	}
	outputs, err := synthetic.GenerateOutputs(defaultRepoRoot())
	if err != nil {
		log.Fatal().Err(err).Msg("generate synthetic manifests")
	}
	if err := synthetic.WriteOutputs(*manifestDir, outputs); err != nil {
		log.Fatal().Err(err).Msg("write synthetic manifests")
	}
	if err := synthetic.WriteArtifactCorpus(filepath.Join(*manifestDir, "artifacts")); err != nil {
		log.Fatal().Err(err).Msg("write synthetic artifact corpus")
	}
	if err := synthetic.WriteDeepCorpus(filepath.Join(*manifestDir, "deep")); err != nil {
		log.Fatal().Err(err).Msg("write synthetic deep corpus")
	}

	writeJSON(map[string]any{
		"surface":     *surface,
		"count":       len(manifests),
		"outputDir":   *outDir,
		"manifestDir": *manifestDir,
		"manifests":   manifests,
	})
}

func runSyntheticRun(args []string) {
	fs := flag.NewFlagSet("synthetic run", flag.ExitOnError)
	scenarioPath := fs.String("scenario", "", "path to a YAML synthetic scenario file")
	packName := fs.String("pack", "", "name or path of a JSON synthetic suite pack")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse synthetic run flags")
	}
	switch {
	case strings.TrimSpace(*scenarioPath) != "" && strings.TrimSpace(*packName) != "":
		log.Fatal().Msg("synthetic run accepts either --scenario or --pack, not both")
	case strings.TrimSpace(*scenarioPath) != "":
		result, err := synthetic.RunScenarioFile(context.Background(), *scenarioPath)
		if err != nil {
			log.Fatal().Err(err).Str("scenario", *scenarioPath).Msg("run synthetic scenario")
		}
		writeJSON(result)
	case strings.TrimSpace(*packName) != "":
		var (
			result synthetic.PackRunResult
			err    error
		)
		if filepath.Ext(*packName) == ".json" || strings.ContainsRune(*packName, os.PathSeparator) {
			result, err = synthetic.RunPackFile(context.Background(), *packName)
		} else {
			result, err = synthetic.RunPackByName(context.Background(), *packName)
		}
		if err != nil {
			log.Fatal().Err(err).Str("pack", *packName).Msg("run synthetic suite pack")
		}
		writeJSON(result)
	default:
		log.Fatal().Msg("synthetic run requires --scenario or --pack")
	}
}

func runSyntheticAudit(args []string) {
	fs := flag.NewFlagSet("synthetic audit", flag.ExitOnError)
	repoRoot := fs.String("repo-root", defaultRepoRoot(), "repository root containing .synthetic registries and scenario packs")
	reportPath := fs.String("report", "", "optional path to write the audit JSON report")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse synthetic audit flags")
	}
	report, err := synthetic.Audit(*repoRoot)
	if err != nil {
		log.Fatal().Err(err).Msg("audit synthetic coverage")
	}
	if strings.TrimSpace(*reportPath) != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatal().Err(err).Msg("marshal synthetic audit report")
		}
		data = append(data, '\n')
		if err := os.WriteFile(*reportPath, data, 0o644); err != nil {
			log.Fatal().Err(err).Str("path", *reportPath).Msg("write synthetic audit report")
		}
	}
	writeJSON(report)
}

func syntheticUsage() {
	fmt.Fprintf(os.Stderr, "usage: urgentry synthetic <command> [args]\n\n")
	fmt.Fprintf(os.Stderr, "commands:\n")
	fmt.Fprintf(os.Stderr, "  generate   Generate synthetic payload corpora for a surface\n")
	fmt.Fprintf(os.Stderr, "  audit      Summarize synthetic coverage against the checked-in registry\n")
	fmt.Fprintf(os.Stderr, "  run        Execute a YAML synthetic scenario or JSON synthetic suite pack\n")
	os.Exit(2)
}

func defaultRepoRoot() string {
	return synthetic.RepoRoot()
}

func bodyExtension(contentType string, compression payloads.Compression) string {
	switch {
	case compression == payloads.CompressionGzip:
		return ".json.gz"
	case compression == payloads.CompressionDeflate:
		return ".json.deflate"
	case strings.Contains(contentType, "envelope"):
		return ".envelope"
	case strings.Contains(contentType, "json"):
		return ".json"
	default:
		return ".bin"
	}
}
