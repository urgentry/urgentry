#!/usr/bin/env bash
# check-binary-size.sh — Fail if the optimized Tiny binary exceeds the size budget.
#
# Usage: bash scripts/check-binary-size.sh [binary-path]
#
# Budget: 40MB. Current baseline: ~30MB. Update budget when intentional
# growth is documented.
set -euo pipefail

BINARY="${1:-urgentry}"
BUDGET_BYTES=41943040  # 40MB

if [ ! -f "$BINARY" ]; then
  echo "error: binary not found: $BINARY" >&2
  echo "Run 'make build' first." >&2
  exit 1
fi

SIZE=$(wc -c < "$BINARY" | tr -d ' ')
SIZE_MB=$(echo "scale=1; $SIZE / 1048576" | bc)
BUDGET_MB=$(echo "scale=0; $BUDGET_BYTES / 1048576" | bc)

if [ "$SIZE" -gt "$BUDGET_BYTES" ]; then
  echo "FAIL: binary size ${SIZE_MB}MB exceeds budget ${BUDGET_MB}MB" >&2
  exit 1
fi

echo "ok: binary size ${SIZE_MB}MB (budget: ${BUDGET_MB}MB)"
