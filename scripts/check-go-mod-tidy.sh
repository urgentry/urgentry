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

primary_module_dir=""
if [[ -f "go.mod" ]]; then
  primary_module_dir="."
  check_module "$primary_module_dir"
else
  nested_go_mod="$(find apps -maxdepth 2 -name go.mod -path '*/urgentry/go.mod' -print -quit 2>/dev/null || true)"
  if [[ -n "$nested_go_mod" ]]; then
    primary_module_dir="$(dirname "$nested_go_mod")"
    check_module "$primary_module_dir"
  else
    echo "no urgentry go.mod found in this checkout" >&2
    exit 1
  fi
fi

while IFS= read -r extra_go_mod; do
  extra_module_dir="$(dirname "$extra_go_mod")"
  if [[ "$extra_module_dir" == "$primary_module_dir" ]]; then
    continue
  fi
  check_module "$extra_module_dir"
done < <(
  find . -mindepth 2 -name go.mod \
    -not -path './.git/*' \
    -not -path './vendor/*' \
    | sort
)
