#!/usr/bin/env bash

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

append_random_port_overrides() {
  local env_file="$1"
  {
    echo "URGENTRY_API_PORT=$(free_port)"
    echo "URGENTRY_INGEST_PORT=$(free_port)"
    echo "URGENTRY_WORKER_PORT=$(free_port)"
    echo "URGENTRY_SCHEDULER_PORT=$(free_port)"
    echo "POSTGRES_PORT=$(free_port)"
    echo "MINIO_API_PORT=$(free_port)"
    echo "MINIO_CONSOLE_PORT=$(free_port)"
    echo "VALKEY_PORT=$(free_port)"
    echo "NATS_PORT=$(free_port)"
    echo "NATS_MONITOR_PORT=$(free_port)"
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
