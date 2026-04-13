#!/bin/sh
set -eu

find_urgentry_app_dir() {
  dir="$1"
  while [ "$dir" != "/" ]; do
    if [ -f "$dir/go.mod" ] && [ -d "$dir/cmd/urgentry" ] && [ -d "$dir/internal" ]; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  return 1
}

resolve_urgentry_paths() {
  source_file="$1"
  start_dir="$(cd "$(dirname "$source_file")" && pwd)"
  APP_DIR="$(find_urgentry_app_dir "$start_dir")"
  REPO_ROOT="$(git -C "$APP_DIR" rev-parse --show-toplevel 2>/dev/null || printf '%s\n' "$APP_DIR")"
  export APP_DIR
  export REPO_ROOT
}
