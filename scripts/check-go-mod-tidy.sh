#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

check_module() {
  local module_dir="$1"
  if [[ ! -f "$module_dir/go.mod" ]]; then
    echo "go.mod not found: $module_dir" >&2
    exit 1
  fi
  echo "checking go mod tidy -diff in $module_dir"
  (
    cd "$module_dir"
    go mod tidy -diff
  )
}

if [[ -f "go.mod" ]]; then
  check_module "."
else
  nested_go_mod="$(find apps -maxdepth 2 -name go.mod -path '*/urgentry/go.mod' -print -quit 2>/dev/null || true)"
  if [[ -n "$nested_go_mod" ]]; then
    check_module "$(dirname "$nested_go_mod")"
  else
    echo "no urgentry go.mod found in this checkout" >&2
    exit 1
  fi
fi

if [[ -f "bench/go.mod" ]]; then
  check_module "bench"
fi
