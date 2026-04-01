#!/usr/bin/env bash
# check-bench-budget.sh — Enforce performance budgets on bench-pr output.
#
# Usage:
#   make bench-pr > current.txt
#   bash scripts/check-bench-budget.sh current.txt
#
# Budgets are absolute upper bounds (ns/op). A benchmark exceeding its
# budget causes a non-zero exit. Update budgets when intentional changes
# land (document the reason in the commit message).
set -euo pipefail

BENCH_FILE="${1:?usage: check-bench-budget.sh <bench-output-file>}"

if [ ! -f "$BENCH_FILE" ]; then
  echo "error: bench output file not found: $BENCH_FILE" >&2
  exit 1
fi

# Budget format: benchmark_name max_ns_per_op max_bytes_per_op max_allocs_per_op
# These are ~2x the current values to catch regressions without false positives.
declare -A NS_BUDGET=(
  ["BenchmarkParse"]=4000
  ["BenchmarkParseMultiItem"]=8000
  ["BenchmarkComputeGrouping"]=2000
  ["BenchmarkComputeGroupingPythonFull"]=1200
  ["BenchmarkComputeGroupingFingerprint"]=400
  ["BenchmarkNormalizePythonFull"]=250000
)

FAILED=0

while IFS= read -r line; do
  # Match benchmark result lines: BenchmarkName-N  runs  ns/op  B/op  allocs/op
  if [[ "$line" =~ ^(Benchmark[A-Za-z0-9_]+)-[0-9]+[[:space:]]+[0-9]+[[:space:]]+([0-9.]+)[[:space:]]ns/op ]]; then
    name="${BASH_REMATCH[1]}"
    ns="${BASH_REMATCH[2]}"
    ns_int="${ns%.*}"

    if [[ -n "${NS_BUDGET[$name]+x}" ]]; then
      budget="${NS_BUDGET[$name]}"
      if (( ns_int > budget )); then
        echo "BUDGET EXCEEDED: $name = ${ns_int} ns/op > budget ${budget} ns/op" >&2
        FAILED=1
      else
        echo "  ok: $name = ${ns_int} ns/op (budget: ${budget})"
      fi
    fi
  fi
done < "$BENCH_FILE"

if [ "$FAILED" -ne 0 ]; then
  echo "" >&2
  echo "Performance budget check FAILED. Update budgets in scripts/check-bench-budget.sh if the regression is intentional." >&2
  exit 1
fi

echo "All performance budgets passed."
