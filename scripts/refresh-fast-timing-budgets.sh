#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

results_dir="${RESULTS_DIR:-test-results}"
budget_file="./testdata/test_timing_budgets.json"

FAST_TEST_SKIP_BUDGETS=1 bash ./scripts/test-fast-with-timings.sh

go run ./tools/testtimings \
  --input "$results_dir/fast-suite.jsonl" \
  --budget-file "$budget_file" \
  --write-budget-file "$budget_file" \
  --budget-headroom 1.5 \
  --budget-min-seconds 30 \
  --budget-round-seconds 5 >/dev/null

bash ./scripts/test-fast-with-timings.sh
