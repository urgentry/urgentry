package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type packageCoverage struct {
	statements int64
	covered    int64
}

type summary struct {
	mode       string
	statements int64
	covered    int64
	packages   map[string]*packageCoverage
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "coverage profile to summarize")
	percentOnly := flag.Bool("percent-only", false, "print only the total percentage")
	minCoverage := flag.Float64("min", 0, "minimum coverage percentage; exit 1 if below (0 = disabled)")
	flag.Parse()

	result, err := readSummary(*profilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage summary: %v\n", err)
		os.Exit(1)
	}

	totalPct := 0.0
	if result.statements > 0 {
		totalPct = float64(result.covered) * 100 / float64(result.statements)
	}

	if *percentOnly {
		fmt.Printf("%.1f\n", totalPct)
	} else {
		fmt.Printf("mode: %s\n", result.mode)
		fmt.Printf("total: %.1f%% (%d/%d statements)\n", totalPct, result.covered, result.statements)
		printPackageGroups(result)
	}

	if err := checkThreshold(totalPct, *minCoverage); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// checkThreshold returns a non-nil error when minPct > 0 and actual is below it.
func checkThreshold(actual, minPct float64) error {
	if minPct > 0 && actual < minPct {
		return fmt.Errorf("FAIL: coverage %.1f%% is below minimum %.1f%%", actual, minPct)
	}
	return nil
}

func readSummary(path string) (summary, error) {
	file, err := os.Open(path)
	if err != nil {
		return summary{}, err
	}
	defer file.Close()
	return parseSummary(file)
}

func parseSummary(r io.Reader) (summary, error) {
	reader := bufio.NewReader(r)
	header, err := readLine(reader)
	if err != nil {
		return summary{}, err
	}
	if !strings.HasPrefix(header, "mode: ") {
		return summary{}, fmt.Errorf("invalid coverage header %q", header)
	}

	result := summary{mode: strings.TrimPrefix(header, "mode: "), packages: make(map[string]*packageCoverage)}
	lineNo := 1
	for {
		line, err := readLine(reader)
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return summary{}, err
		}
		lineNo++
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		stmts, count, err := parseCoverageFields(line)
		if err != nil {
			return summary{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		result.statements += int64(stmts)
		if count > 0 {
			result.covered += int64(stmts)
		}

		// Track per-package coverage.
		pkg := extractPackage(line)
		if pkg != "" {
			if result.packages[pkg] == nil {
				result.packages[pkg] = &packageCoverage{}
			}
			result.packages[pkg].statements += int64(stmts)
			if count > 0 {
				result.packages[pkg].covered += int64(stmts)
			}
		}
	}
}

// Package groups for per-surface coverage visibility.
var packageGroups = map[string][]string{
	"bridge":   {"telemetrybridge", "telemetryquery"},
	"ingest":   {"ingest", "pipeline", "envelope", "normalize"},
	"web":      {"web", "api"},
	"ops":      {"selfhostedops", "postgrescontrol", "controlplane"},
	"storage":  {"sqlite", "store", "blob"},
}

func printPackageGroups(result summary) {
	if len(result.packages) == 0 {
		return
	}
	fmt.Println("\npackage groups:")
	for group, prefixes := range packageGroups {
		var stmts, covered int64
		for _, prefix := range prefixes {
			full := "urgentry/internal/" + prefix
			if pkg, ok := result.packages[full]; ok {
				stmts += pkg.statements
				covered += pkg.covered
			}
		}
		if stmts == 0 {
			continue
		}
		pct := float64(covered) * 100 / float64(stmts)
		fmt.Printf("  %-10s %.1f%% (%d/%d)\n", group, pct, covered, stmts)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) && len(line) > 0 {
		return strings.TrimRight(line, "\r\n"), nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func extractPackage(line string) string {
	// Coverage line format: urgentry/internal/foo/bar.go:10.5,20.3 1 0
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return ""
	}
	path := line[:colon]
	lastSlash := strings.LastIndexByte(path, '/')
	if lastSlash < 0 {
		return ""
	}
	return path[:lastSlash]
}

func parseCoverageFields(line string) (int, int64, error) {
	lastSpace := strings.LastIndexByte(line, ' ')
	if lastSpace < 0 {
		return 0, 0, fmt.Errorf("missing execution count")
	}
	count, err := strconv.ParseInt(line[lastSpace+1:], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse execution count: %w", err)
	}

	prefix := line[:lastSpace]
	secondLastSpace := strings.LastIndexByte(prefix, ' ')
	if secondLastSpace < 0 {
		return 0, 0, fmt.Errorf("missing statement count")
	}
	statements, err := strconv.Atoi(prefix[secondLastSpace+1:])
	if err != nil {
		return 0, 0, fmt.Errorf("parse statement count: %w", err)
	}
	return statements, count, nil
}
