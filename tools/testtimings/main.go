package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type packageTiming struct {
	Package string  `json:"package"`
	Status  string  `json:"status"`
	Elapsed float64 `json:"elapsed_seconds"`
}

type timingBudget struct {
	Package    string  `json:"package"`
	MaxSeconds float64 `json:"max_seconds"`
}

type timingBudgets struct {
	Suite   string         `json:"suite,omitempty"`
	Budgets []timingBudget `json:"budgets"`
}

type budgetViolation struct {
	Package    string  `json:"package"`
	Elapsed    float64 `json:"elapsed_seconds"`
	MaxSeconds float64 `json:"max_seconds"`
}

type timingSummary struct {
	Packages   []packageTiming   `json:"packages"`
	Slowest    []packageTiming   `json:"slowest"`
	Budgets    []timingBudget    `json:"budgets,omitempty"`
	Violations []budgetViolation `json:"violations,omitempty"`
}

func main() {
	inputPath := flag.String("input", "", "go test -json output file (defaults to stdin)")
	emitOutput := flag.Bool("emit-output", false, "write test output events to stdout while summarizing")
	budgetPath := flag.String("budget-file", "", "JSON file with package timing budgets")
	summaryPath := flag.String("summary-output", "", "write a JSON timing summary to this path")
	topN := flag.Int("top", 10, "number of slowest packages to print")
	flag.Parse()

	timings, err := readPackageTimings(*inputPath, *emitOutput, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test timings: %v\n", err)
		os.Exit(1)
	}
	if len(timings) == 0 {
		fmt.Fprintln(os.Stderr, "test timings: no package timing events found")
		os.Exit(1)
	}

	sortPackageTimings(timings)

	budgets, err := readBudgets(*budgetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test timings: %v\n", err)
		os.Exit(1)
	}
	violations := checkBudgets(timings, budgets)

	limit := *topN
	if limit <= 0 || limit > len(timings) {
		limit = len(timings)
	}
	slowest := append([]packageTiming(nil), timings[:limit]...)

	if *summaryPath != "" {
		summary := timingSummary{
			Packages:   timings,
			Slowest:    slowest,
			Budgets:    budgets,
			Violations: violations,
		}
		if err := writeSummary(*summaryPath, summary); err != nil {
			fmt.Fprintf(os.Stderr, "test timings: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("slowest packages:")
	for _, timing := range slowest {
		fmt.Printf("  %6.2fs  %s\n", timing.Elapsed, timing.Package)
	}
	if len(violations) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "timing budgets exceeded:")
	for _, violation := range violations {
		fmt.Fprintf(os.Stderr, "  %6.2fs > %6.2fs  %s\n", violation.Elapsed, violation.MaxSeconds, violation.Package)
	}
	os.Exit(1)
}

func readPackageTimings(path string, emitOutput bool, output io.Writer) ([]packageTiming, error) {
	reader := io.Reader(os.Stdin)
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader = file
	}
	return readPackageTimingsFromReader(reader, emitOutput, output)
}

func readPackageTimingsFromReader(reader io.Reader, emitOutput bool, output io.Writer) ([]packageTiming, error) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	timings := make(map[string]packageTiming)
	bufferedOutput := make(map[string][]string)
	for scanner.Scan() {
		var evt event
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			return nil, fmt.Errorf("decode go test event: %w", err)
		}
		if emitOutput && evt.Action == "output" && evt.Package != "" {
			if evt.Test == "" {
				if _, err := fmt.Fprint(output, evt.Output); err != nil {
					return nil, err
				}
			} else {
				bufferedOutput[evt.Package] = append(bufferedOutput[evt.Package], evt.Output)
			}
		}
		if evt.Package == "" || evt.Test != "" || evt.Elapsed <= 0 {
			continue
		}
		switch evt.Action {
		case "pass", "fail", "skip":
			if emitOutput && evt.Action == "fail" {
				for _, line := range bufferedOutput[evt.Package] {
					if _, err := fmt.Fprint(output, line); err != nil {
						return nil, err
					}
				}
			}
			delete(bufferedOutput, evt.Package)
			timings[evt.Package] = packageTiming{
				Package: evt.Package,
				Status:  evt.Action,
				Elapsed: evt.Elapsed,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	result := make([]packageTiming, 0, len(timings))
	for _, timing := range timings {
		result = append(result, timing)
	}
	return result, nil
}

func sortPackageTimings(timings []packageTiming) {
	sort.Slice(timings, func(i, j int) bool {
		if timings[i].Elapsed == timings[j].Elapsed {
			return timings[i].Package < timings[j].Package
		}
		return timings[i].Elapsed > timings[j].Elapsed
	})
}

func readBudgets(path string) ([]timingBudget, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var budgets timingBudgets
	if err := json.Unmarshal(data, &budgets); err != nil {
		return nil, fmt.Errorf("decode budget file: %w", err)
	}
	for _, budget := range budgets.Budgets {
		if budget.Package == "" {
			return nil, fmt.Errorf("decode budget file: budget package must not be empty")
		}
		if budget.MaxSeconds <= 0 {
			return nil, fmt.Errorf("decode budget file: budget for %q must be positive", budget.Package)
		}
	}
	sort.Slice(budgets.Budgets, func(i, j int) bool {
		return budgets.Budgets[i].Package < budgets.Budgets[j].Package
	})
	return budgets.Budgets, nil
}

func checkBudgets(timings []packageTiming, budgets []timingBudget) []budgetViolation {
	if len(budgets) == 0 {
		return nil
	}
	timingByPackage := make(map[string]packageTiming, len(timings))
	for _, timing := range timings {
		timingByPackage[timing.Package] = timing
	}
	violations := make([]budgetViolation, 0)
	for _, budget := range budgets {
		timing, ok := timingByPackage[budget.Package]
		if !ok || timing.Elapsed <= budget.MaxSeconds {
			continue
		}
		violations = append(violations, budgetViolation{
			Package:    budget.Package,
			Elapsed:    timing.Elapsed,
			MaxSeconds: budget.MaxSeconds,
		})
	}
	return violations
}

func writeSummary(path string, summary timingSummary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
