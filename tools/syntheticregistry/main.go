package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	repoRoot := flag.String("repo-root", defaultRepoRoot(), "repository root containing docs/, eval/, and .synthetic/")
	outDir := flag.String("out-dir", "", "directory to write synthetic registry artifacts into (defaults to <repo-root>/.synthetic)")
	writeOutputs := flag.Bool("write", true, "write registry artifacts to disk")
	checkOutputs := flag.Bool("check", false, "fail if checked-in registry artifacts do not match generated output")
	flag.Parse()

	if *outDir == "" {
		*outDir = filepath.Join(*repoRoot, ".synthetic")
	}

	bundle, err := GenerateBundle(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "synthetic registry: %v\n", err)
		os.Exit(1)
	}

	if *checkOutputs {
		if err := CheckOutputs(*outDir, bundle); err != nil {
			fmt.Fprintf(os.Stderr, "synthetic registry check: %v\n", err)
			os.Exit(1)
		}
	}

	if *writeOutputs {
		if err := WriteOutputs(*outDir, bundle); err != nil {
			fmt.Fprintf(os.Stderr, "synthetic registry write: %v\n", err)
			os.Exit(1)
		}
		for _, name := range bundle.FileNames() {
			fmt.Println(filepath.ToSlash(filepath.Join(*outDir, name)))
		}
	}
}
