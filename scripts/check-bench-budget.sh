#!/usr/bin/env bash
# check-bench-budget.sh — Enforce performance budgets on bench-pr output.
#
# Usage:
#   make bench-pr > current.txt
#   bash scripts/check-bench-budget.sh current.txt
#
# Budgets are absolute upper bounds. A benchmark exceeding any of its
# budgets causes a non-zero exit. Update budgets when intentional changes
# land (document the reason in the commit message).
set -euo pipefail

BENCH_FILE="${1:?usage: check-bench-budget.sh <bench-output-file>}"

if [ ! -f "$BENCH_FILE" ]; then
  echo "error: bench output file not found: $BENCH_FILE" >&2
  exit 1
fi

# Budget format: benchmark_name ns_budget bytes_budget allocs_budget
# Values are ~2x current to catch regressions without false positives.
# A budget of 0 means "not checked" for that dimension.
declare -A NS_BUDGET BYTES_BUDGET ALLOCS_BUDGET

# envelope/grouping/normalize/domain (original PR lane)
NS_BUDGET["BenchmarkParse"]=4000;            BYTES_BUDGET["BenchmarkParse"]=4000;            ALLOCS_BUDGET["BenchmarkParse"]=80
NS_BUDGET["BenchmarkParseMultiItem"]=8000;   BYTES_BUDGET["BenchmarkParseMultiItem"]=8000;   ALLOCS_BUDGET["BenchmarkParseMultiItem"]=160
NS_BUDGET["BenchmarkComputeGrouping"]=2000;  BYTES_BUDGET["BenchmarkComputeGrouping"]=2000;  ALLOCS_BUDGET["BenchmarkComputeGrouping"]=40
NS_BUDGET["BenchmarkComputeGroupingPythonFull"]=1200; BYTES_BUDGET["BenchmarkComputeGroupingPythonFull"]=2000; ALLOCS_BUDGET["BenchmarkComputeGroupingPythonFull"]=40
NS_BUDGET["BenchmarkComputeGroupingFingerprint"]=400; BYTES_BUDGET["BenchmarkComputeGroupingFingerprint"]=500; ALLOCS_BUDGET["BenchmarkComputeGroupingFingerprint"]=20
NS_BUDGET["BenchmarkNormalizePythonFull"]=260000;     BYTES_BUDGET["BenchmarkNormalizePythonFull"]=65000;      ALLOCS_BUDGET["BenchmarkNormalizePythonFull"]=1200
NS_BUDGET["BenchmarkNormalizeArrayTags"]=60000;       BYTES_BUDGET["BenchmarkNormalizeArrayTags"]=16000;       ALLOCS_BUDGET["BenchmarkNormalizeArrayTags"]=320

# http hot paths
NS_BUDGET["BenchmarkProjectIssuesEndpoint"]=8000000;  BYTES_BUDGET["BenchmarkProjectIssuesEndpoint"]=800000;  ALLOCS_BUDGET["BenchmarkProjectIssuesEndpoint"]=8000

# telemetryquery bridge
NS_BUDGET["BenchmarkBridgeService/SearchLogs"]=3000000;                       BYTES_BUDGET["BenchmarkBridgeService/SearchLogs"]=200000;                      ALLOCS_BUDGET["BenchmarkBridgeService/SearchLogs"]=6000
NS_BUDGET["BenchmarkBridgeService/ExecuteTransactionsTable"]=2000000;         BYTES_BUDGET["BenchmarkBridgeService/ExecuteTransactionsTable"]=240000;        ALLOCS_BUDGET["BenchmarkBridgeService/ExecuteTransactionsTable"]=6000

FAILED=0

while IFS= read -r line; do
  # Match: BenchmarkName-N  runs  ns/op [B/op] [allocs/op]
  if [[ "$line" =~ ^(Benchmark[A-Za-z0-9_/]+)-[0-9]+[[:space:]]+[0-9]+[[:space:]]+([0-9.]+)[[:space:]]ns/op ]]; then
    name="${BASH_REMATCH[1]}"
    ns="${BASH_REMATCH[2]}"
    ns_int="${ns%.*}"

    # Extract bytes/op and allocs/op if present.
    bytes_int=0
    allocs_int=0
    if [[ "$line" =~ ([0-9]+)[[:space:]]B/op ]]; then
      bytes_int="${BASH_REMATCH[1]}"
    fi
    if [[ "$line" =~ ([0-9]+)[[:space:]]allocs/op ]]; then
      allocs_int="${BASH_REMATCH[1]}"
    fi

    checked=false

    if [[ -n "${NS_BUDGET[$name]+x}" ]] && [[ "${NS_BUDGET[$name]}" -gt 0 ]]; then
      checked=true
      if (( ns_int > NS_BUDGET[$name] )); then
        echo "BUDGET EXCEEDED: $name = ${ns_int} ns/op > budget ${NS_BUDGET[$name]} ns/op" >&2
        FAILED=1
      fi
    fi
    if [[ -n "${BYTES_BUDGET[$name]+x}" ]] && [[ "${BYTES_BUDGET[$name]}" -gt 0 ]]; then
      checked=true
      if (( bytes_int > BYTES_BUDGET[$name] )); then
        echo "BUDGET EXCEEDED: $name = ${bytes_int} B/op > budget ${BYTES_BUDGET[$name]} B/op" >&2
        FAILED=1
      fi
    fi
    if [[ -n "${ALLOCS_BUDGET[$name]+x}" ]] && [[ "${ALLOCS_BUDGET[$name]}" -gt 0 ]]; then
      checked=true
      if (( allocs_int > ALLOCS_BUDGET[$name] )); then
        echo "BUDGET EXCEEDED: $name = ${allocs_int} allocs/op > budget ${ALLOCS_BUDGET[$name]} allocs/op" >&2
        FAILED=1
      fi
    fi

    if $checked; then
      if [ "$FAILED" -eq 0 ] 2>/dev/null || true; then
        echo "  ok: $name = ${ns_int} ns/op, ${bytes_int} B/op, ${allocs_int} allocs/op"
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
