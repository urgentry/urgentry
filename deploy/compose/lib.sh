#!/usr/bin/env bash

append_random_port_overrides() {
  local env_file="$1"
  # Use port 0 to let Docker assign random host ports, eliminating TOCTOU
  # races from pre-allocated free_port() calls. After compose up, use
  # docker_host_port() to discover the actual bound ports.
  {
    echo "URGENTRY_API_PORT=0"
    echo "URGENTRY_INGEST_PORT=0"
    echo "URGENTRY_WORKER_PORT=0"
    echo "URGENTRY_SCHEDULER_PORT=0"
    echo "POSTGRES_PORT=0"
    echo "MINIO_API_PORT=0"
    echo "MINIO_CONSOLE_PORT=0"
    echo "VALKEY_PORT=0"
    echo "NATS_PORT=0"
    echo "NATS_MONITOR_PORT=0"
  } >>"$env_file"
}

extract_host_port() {
  python3 -c '
import sys

line = sys.stdin.read().strip().splitlines()
if not line:
    raise SystemExit(1)
value = line[-1].rsplit(":", 1)[-1].strip()
if not value:
    raise SystemExit(1)
print(value)
'
}

docker_host_port() {
  local container="$1"
  local container_port="$2"
  docker port "$container" "${container_port}/tcp" | extract_host_port
}

command_hit_port_conflict() {
  local output_file="$1"
  grep -Eq 'ports are not available|address already in use|port is already allocated' "$output_file"
}
