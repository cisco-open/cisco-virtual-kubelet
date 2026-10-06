#!/usr/bin/env python3
"""Validate durable roadmap evidence; never execute collector commands.

Default mode validates a checkpoint, including explicitly blocked gates.
--require-complete additionally rejects every open gate and stale candidate.
This is evidence accounting, not a substitute for review of test assertions.
"""
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

import argparse
from datetime import datetime
import hashlib
import json
from pathlib import Path
import re
import sys


# Deliberately independent of the report: deleting a row cannot remove a gate.
GATE_COUNTS = (8, 5, 3, 6, 4, 3, 5, 4, 4, 4, 4, 4, 4)
REQUIRED = {f"E{area:02}-{chr(65 + gate)}"
            for area, count in enumerate(GATE_COUNTS) for gate in range(count)}
REQUIRED |= {f"F{gate:02}" for gate in range(1, 14)}
CACHE_CONDITIONAL = {"E09-B", "E09-C", "E09-D", "F09"}
PHYSICAL_CASES = {f"c9k-{node}/{direction}" for node in (100, 101, 103)
                  for direction in ("upgrade", "downgrade")}
SHA = re.compile(r"[0-9a-f]{40}")
DIGEST = re.compile(r"[0-9a-f]{64}")


class InvalidEvidence(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise InvalidEvidence(message)


def fields(value, required, optional=()):
    require(isinstance(value, dict), "expected an object")
    require(set(required) <= value.keys(), f"missing fields: {set(required) - value.keys()}")
    require(value.keys() <= set(required) | set(optional), "unknown fields")


def nonempty(value):
    return isinstance(value, str) and bool(value.strip())


def timestamp(value):
    require(isinstance(value, str), "timestamp must be a string")
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise InvalidEvidence("invalid timestamp") from error
    require(result.utcoffset() is not None, "timestamp needs an explicit timezone")
    return result


def artifact(record, root):
    fields(record, {"path", "sha256"})
    path, digest = record["path"], record["sha256"]
    require(isinstance(path, str) and isinstance(digest, str) and DIGEST.fullmatch(digest),
            "artifact needs a relative path and SHA-256")
    relative = Path(path)
    require(not relative.is_absolute() and ".." not in relative.parts and
            relative.parts[:2] == ("docs", "evidence"),
            "artifact must be under repository docs/evidence, not a private /tmp log")
    resolved = (root / relative).resolve()
    require(resolved.is_relative_to((root / "docs/evidence").resolve()),
            "artifact symlink escapes evidence directory")
    require(resolved.is_file(), f"missing artifact: {path}")
    require(resolved.stat().st_size <= 16 * 1024 * 1024, "publish a bounded sanitized artifact")
    require(hashlib.sha256(resolved.read_bytes()).hexdigest() == digest,
            f"artifact checksum mismatch: {path}")


def validate(report, root, candidate=None, require_complete=False):
    fields(report, {"schema_version", "candidate", "recorded_at", "gates"},
           {"cache_decision"})
    require(type(report["schema_version"]) is int and report["schema_version"] == 1,
            "unsupported schema version")
    require(isinstance(report["candidate"], str) and SHA.fullmatch(report["candidate"]),
            "candidate must be a full Git SHA")
    recorded = timestamp(report["recorded_at"])
    if candidate is not None:
        require(SHA.fullmatch(candidate) is not None, "expected candidate must be a full Git SHA")
        require(report["candidate"] == candidate, "report is for a different candidate")
    require(not require_complete or candidate is not None,
            "completion requires an independently supplied --candidate SHA")
    gates = report["gates"]
    require(isinstance(gates, list), "gates must be a list")
    results = {}
    for gate in gates:
        fields(gate, {"id", "status", "summary", "next_action", "evidence", "runs"})
        name, status = gate["id"], gate["status"]
        require(isinstance(name, str) and name in REQUIRED, f"unknown gate: {name}")
        require(name not in results, f"duplicate gate: {name}")
        require(status in ("PASS", "FAIL", "BLOCKED", "NOT_APPLICABLE"),
                f"{name}: invalid status; skips are not results")
        results[name] = status
        require(nonempty(gate["summary"]), f"{name}: missing result or blocker")
        require(isinstance(gate["next_action"], str), f"{name}: next_action must be text")
        require(status not in ("FAIL", "BLOCKED") or nonempty(gate["next_action"]),
                f"{name}: open result needs an executable next action")
        evidence, runs = gate["evidence"], gate["runs"]
        require(isinstance(evidence, list) and isinstance(runs, list),
                f"{name}: evidence and runs must be lists")
        for entry in evidence:
            artifact(entry, root)
        if status == "PASS":
            require(evidence and runs, f"{name}: PASS requires durable evidence and execution results")
            if name == "F13":
                cases = [run.get("case") for run in runs if isinstance(run, dict)]
                require(len(cases) == 6 and all(isinstance(case, str) for case in cases) and
                        set(cases) == PHYSICAL_CASES,
                        "F13 requires six distinct device/direction execution records")
        if status == "NOT_APPLICABLE":
            require(name in CACHE_CONDITIONAL, f"{name}: unconditional gate cannot be waived")
        for run in runs:
            fields(run, {"candidate", "command", "started_at", "finished_at", "exit_code",
                         "collector_complete", "skipped", "observed"}, {"case"})
            require(run["candidate"] == report["candidate"], f"{name}: stale test candidate")
            require(nonempty(run["command"]) and nonempty(run["observed"]),
                    f"{name}: command and observed assertion required")
            start, end = timestamp(run["started_at"]), timestamp(run["finished_at"])
            require(start <= end <= recorded, f"{name}: invalid execution interval")
            require(type(run["exit_code"]) is int and type(run["skipped"]) is int and
                    run["skipped"] >= 0 and type(run["collector_complete"]) is bool,
                    f"{name}: explicit exit, skip count and collection result required")
            if status == "PASS":
                require(run["exit_code"] == 0 and run["collector_complete"] and run["skipped"] == 0,
                        f"{name}: failed, skipped or interrupted collector cannot pass")
    require(set(results) == REQUIRED, f"missing gates: {sorted(REQUIRED - results.keys())}")
    decision = report.get("cache_decision")
    if decision is not None:
        fields(decision, {"selected", "reason", "evidence"})
        require(type(decision["selected"]) is bool and nonempty(decision["reason"]),
                "cache decision requires an explicit selection and reason")
        require(isinstance(decision["evidence"], list) and decision["evidence"],
                "cache decision requires measured evidence")
        for entry in decision["evidence"]:
            artifact(entry, root)
    if "NOT_APPLICABLE" in results.values():
        require(decision is not None and decision["selected"] is False and results["E09-A"] == "PASS",
                "cache waiver requires a measured E09-A PASS and explicit no-cache decision")
    pending = sorted(name for name, status in results.items() if status in ("BLOCKED", "FAIL"))
    require(not require_complete or not pending, f"acceptance incomplete: {', '.join(pending)}")
    return results


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, f"duplicate JSON key: {key}")
        result[key] = value
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report", type=Path)
    parser.add_argument("--candidate", help="independently obtained full Git SHA")
    parser.add_argument("--require-complete", action="store_true")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    try:
        report = json.loads(args.report.read_text(), object_pairs_hook=unique_object)
        results = validate(report, root, args.candidate, args.require_complete)
    except (OSError, ValueError, TypeError) as error:
        print(f"INVALID / NOT COMPLETE: {error}", file=sys.stderr)
        return 1
    counts = {status: list(results.values()).count(status)
              for status in ("PASS", "FAIL", "BLOCKED", "NOT_APPLICABLE")}
    print(f"{'COMPLETE evidence index' if args.require_complete else 'VALID checkpoint (not merge approval)'}: {counts}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
