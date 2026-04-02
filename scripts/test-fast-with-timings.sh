#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

mapfile -t packages < <(go list ./... | grep -v '^urgentry/internal/compat$')
package_parallelism="${FAST_TEST_PACKAGE_PARALLELISM:-4}"
results_dir="${RESULTS_DIR:-test-results}"
raw_output="$results_dir/fast-suite.jsonl"
summary_output="$results_dir/fast-suite-timings.json"
budget_file="./testdata/test_timing_budgets.json"

mkdir -p "$results_dir"

set +e
go test -json -p "$package_parallelism" "${packages[@]}" -count=1 | tee "$raw_output" | go run ./tools/testtimings --emit-output --budget-file "$budget_file" --summary-output "$summary_output"
statuses=("${PIPESTATUS[@]}")
set -e

go_status="${statuses[0]:-1}"
tee_status="${statuses[1]:-1}"
timing_status="${statuses[2]:-1}"

if [ "$tee_status" -ne 0 ]; then
  exit "$tee_status"
fi
if [ "$go_status" -ne 0 ]; then
  exit "$go_status"
fi
if [ "$timing_status" -ne 0 ]; then
  exit "$timing_status"
fi
