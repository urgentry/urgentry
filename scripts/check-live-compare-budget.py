#!/usr/bin/env python3
"""Enforce recovered large-box comparison budgets on a live_compare artifact."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import pathlib
import sys


BUDGETS = {
    "Tiny": {
        "stable_rps_min": 400,
        "ingest_p95_ms_max": 11.0,
        "query_p95_ms_max": 85.0,
        "peak_memory_mb_max": 55.0,
        "quality_min": 1.0,
    },
    "Self-hosted": {
        "stable_rps_min": 2200,
        "ingest_p95_ms_max": 1.0,
        "query_p95_ms_max": 50.0,
        "peak_memory_mb_max": 425.0,
        "quality_min": 1.0,
    },
}


def fail(message: str) -> None:
    print(message, file=sys.stderr)
    raise SystemExit(1)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Enforce recovered large-box comparison budgets on one or more live_compare artifacts."
    )
    parser.add_argument("artifacts", nargs="+", help="Path(s) to live_compare latest.json artifacts")
    parser.add_argument("--expect-commit", help="Require report source.git_commit to match this commit")
    parser.add_argument("--max-age-hours", type=float, help="Require generated_at to be newer than this age")
    return parser.parse_args()


def parse_generated_at(value: str) -> dt.datetime:
    try:
        return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
    except ValueError as exc:
        fail(f"invalid generated_at timestamp: {value!r}: {exc}")
    raise AssertionError("unreachable")


def report_commit(obj: dict[str, object]) -> str:
    source = obj.get("source")
    if isinstance(source, dict):
        commit = source.get("git_commit")
        if isinstance(commit, str):
            return commit
    for key in ("git_commit", "git_sha", "source_commit", "bench_commit"):
        value = obj.get(key)
        if isinstance(value, str):
            return value
    return ""


def main() -> int:
    args = parse_args()
    by_name: dict[str, object] = {}
    newest_generated_at: dt.datetime | None = None
    for raw in args.artifacts:
        path = pathlib.Path(raw)
        if not path.is_file():
            fail(f"artifact not found: {path}")
        obj = json.loads(path.read_text())
        generated_at = obj.get("generated_at")
        if not isinstance(generated_at, str):
            fail(f"missing generated_at in artifact: {path}")
        parsed_generated_at = parse_generated_at(generated_at)
        if newest_generated_at is None or parsed_generated_at > newest_generated_at:
            newest_generated_at = parsed_generated_at
        if args.expect_commit:
            commit = report_commit(obj)
            if commit != args.expect_commit:
                fail(f"{path} commit {commit or '<missing>'} != expected {args.expect_commit}")
        for target in obj.get("targets", []):
            by_name[target["name"]] = target

    if args.max_age_hours is not None:
        if newest_generated_at is None:
            fail("no generated_at timestamps found in artifacts")
        age = dt.datetime.now(dt.timezone.utc) - newest_generated_at
        if age.total_seconds() > args.max_age_hours * 3600:
            fail(
                f"latest live-compare artifact age {age.total_seconds() / 3600:.1f}h > "
                f"budget {args.max_age_hours:.1f}h"
            )

    for name, budget in BUDGETS.items():
        target = by_name.get(name)
        if target is None:
            fail(f"missing target in artifact: {name}")
        stable_rps = int(target["stable_rps"])
        ingest_p95 = float(target["ingest"]["latency_p95_ms"])
        query_p95 = float(target["query"]["p95_ms"])
        peak_memory = float(target["resource_usage"]["peak_memory_mb"])
        quality = float(target["quality"]["score"])

        if stable_rps < budget["stable_rps_min"]:
            fail(f"{name} stable_rps {stable_rps} < budget {budget['stable_rps_min']}")
        if ingest_p95 > budget["ingest_p95_ms_max"]:
            fail(f"{name} ingest_p95 {ingest_p95:.3f} ms > budget {budget['ingest_p95_ms_max']:.3f} ms")
        if query_p95 > budget["query_p95_ms_max"]:
            fail(f"{name} query_p95 {query_p95:.3f} ms > budget {budget['query_p95_ms_max']:.3f} ms")
        if peak_memory > budget["peak_memory_mb_max"]:
            fail(f"{name} peak_memory {peak_memory:.3f} MB > budget {budget['peak_memory_mb_max']:.3f} MB")
        if quality + 1e-9 < budget["quality_min"]:
            fail(f"{name} quality {quality:.3f} < budget {budget['quality_min']:.3f}")

        print(
            f"ok: {name} stable={stable_rps} ingest_p95={ingest_p95:.2f} "
            f"query_p95={query_p95:.2f} peak_mem={peak_memory:.1f} quality={quality:.0%}"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
