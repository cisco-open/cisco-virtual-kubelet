#!/usr/bin/env python3
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
"""Read-only wait for successful main CI on an exact frozen release commit."""
from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
import time
import urllib.parse
from dataclasses import dataclass
from typing import Any, Callable

UTC = dt.timezone.utc
MAX_PAGES = 5
MAX_RUNS = 500
ACTIVE = frozenset({"queued", "in_progress", "pending", "requested", "waiting"})
TERMINAL = frozenset(
    {
        "success",
        "failure",
        "cancelled",
        "timed_out",
        "action_required",
        "neutral",
        "skipped",
        "stale",
        "startup_failure",
    }
)


class ReleaseCIError(ValueError):
    """Metadata or readiness cannot safely authorize release tagging."""


@dataclass(frozen=True)
class Readiness:
    state: str
    run: dict[str, Any] | None
    reason: str


def validate_identity(repository: str, commit: str) -> None:
    if re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository) is None or any(
        part in {".", ".."} for part in repository.split("/")
    ):
        raise ReleaseCIError("repository must be OWNER/REPOSITORY")
    if re.fullmatch(r"[0-9a-f]{40}", commit) is None:
        raise ReleaseCIError("commit must be a full lowercase 40-character SHA")


def positive_integer(value: Any, name: str) -> int:
    if type(value) is not int or value <= 0:
        raise ReleaseCIError(f"invalid {name}")
    return value


def timestamp(value: Any) -> dt.datetime:
    if (
        not isinstance(value, str)
        or re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", value) is None
    ):
        raise ReleaseCIError("updated_at must be a canonical UTC timestamp")
    try:
        return dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=UTC)
    except ValueError as error:
        raise ReleaseCIError("invalid updated_at timestamp") from error


def evaluate_runs(
    repository: str,
    commit: str,
    runs: list[dict[str, Any]],
    *,
    completed_before: dt.datetime,
) -> Readiness:
    """Validate exact candidate metadata, then classify without network or waits."""
    validate_identity(repository, commit)
    if (
        not isinstance(completed_before, dt.datetime)
        or completed_before.tzinfo is None
        or completed_before.utcoffset() != dt.timedelta(0)
    ):
        raise ReleaseCIError("completion cutoff must be UTC-aware")
    if not isinstance(runs, list) or len(runs) > MAX_RUNS:
        raise ReleaseCIError("invalid or oversized workflow-run list")
    successful: list[dict[str, Any]] = []
    active = False
    seen: set[int] = set()
    for run in runs:
        if not isinstance(run, dict):
            raise ReleaseCIError("workflow run must be an object")
        run_id = positive_integer(run.get("id"), "run ID")
        positive_integer(run.get("run_attempt"), "run attempt")
        if run_id in seen:
            raise ReleaseCIError("duplicate workflow run")
        seen.add(run_id)
        repo = run.get("repository")
        path = run.get("path")
        if (
            not isinstance(repo, dict)
            or repo.get("full_name") != repository
            or not isinstance(path, str)
            or path.split("@", 1)[0] != ".github/workflows/smoke.yml"
            or run.get("event") != "push"
            or run.get("head_branch") != "main"
            or run.get("head_sha") != commit
        ):
            raise ReleaseCIError(
                "workflow run does not match the frozen main candidate"
            )
        completed_at = timestamp(run.get("updated_at"))
        if completed_at > completed_before:
            raise ReleaseCIError("workflow update is later than the completion cutoff")
        status, conclusion = run.get("status"), run.get("conclusion")
        if not isinstance(status, str) or (
            conclusion is not None and not isinstance(conclusion, str)
        ):
            raise ReleaseCIError("invalid workflow status or conclusion")
        if status in ACTIVE:
            if conclusion not in (None, ""):
                raise ReleaseCIError("active workflow has a terminal conclusion")
            active = True
        elif status == "completed":
            if conclusion not in TERMINAL:
                raise ReleaseCIError("completed workflow has an invalid conclusion")
            if conclusion == "success":
                successful.append(run)
        else:
            raise ReleaseCIError("workflow has an unknown status")
    if len(successful) > 1:
        raise ReleaseCIError(
            "multiple successful main CI runs are ambiguous; do not tag"
        )
    if active:
        return Readiness(
            "waiting", None, "main CI is still active for the frozen commit"
        )
    if successful:
        return Readiness("ready", successful[0], "one successful main CI run qualifies")
    if runs:
        return Readiness(
            "failed", None, "main CI finished without success for the frozen commit"
        )
    return Readiness("waiting", None, "main CI has not appeared for the frozen commit")


def github_api(path: str) -> Any:
    """GET only; gh supplies existing credentials without exposing token material."""
    try:
        result = subprocess.run(
            [
                "gh",
                "api",
                "--hostname",
                "github.com",
                "-H",
                "X-GitHub-Api-Version: 2022-11-28",
                path,
            ],
            check=True,
            capture_output=True,
            text=True,
            timeout=30,
        )
        return json.loads(result.stdout)
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        raise ReleaseCIError(
            "GitHub metadata GET failed; no release action was taken"
        ) from error


def load_runs(
    repository: str, commit: str, *, api_call: Callable[[str], Any] = github_api
) -> list[dict[str, Any]]:
    """Bound search results and refetch each exact attempt before trusting it."""
    validate_identity(repository, commit)
    query = urllib.parse.urlencode(
        {"branch": "main", "event": "push", "head_sha": commit, "per_page": 100}
    )
    summaries: list[dict[str, Any]] = []
    total: int | None = None
    for page in range(1, MAX_PAGES + 1):
        response = api_call(
            f"/repos/{repository}/actions/workflows/smoke.yml/runs?{query}&page={page}"
        )
        if not isinstance(response, dict) or not isinstance(
            response.get("workflow_runs"), list
        ):
            raise ReleaseCIError("invalid workflow search response")
        count = response.get("total_count")
        if (
            type(count) is not int
            or not 0 <= count <= MAX_RUNS
            or (total is not None and count != total)
        ):
            raise ReleaseCIError(
                "workflow search is oversized or changed while paginating"
            )
        total = count
        batch = response["workflow_runs"]
        if len(batch) > 100 or any(not isinstance(item, dict) for item in batch):
            raise ReleaseCIError("invalid workflow search page")
        summaries.extend(batch)
        if len(summaries) > MAX_RUNS:
            raise ReleaseCIError("workflow search exceeds the safety limit")
        if len(summaries) > total:
            raise ReleaseCIError("workflow search exceeds its declared total")
        if len(summaries) == total:
            break
        if len(batch) < 100:
            raise ReleaseCIError("workflow search is truncated or inconsistent")
    else:
        raise ReleaseCIError("workflow search may be truncated; refusing to guess")
    runs: list[dict[str, Any]] = []
    seen: set[int] = set()
    for summary in summaries:
        run_id = positive_integer(summary.get("id"), "run ID")
        attempt = positive_integer(summary.get("run_attempt"), "run attempt")
        if run_id in seen:
            raise ReleaseCIError("duplicate workflow search result")
        seen.add(run_id)
        run = api_call(f"/repos/{repository}/actions/runs/{run_id}/attempts/{attempt}")
        if (
            not isinstance(run, dict)
            or type(run.get("id")) is not int
            or type(run.get("run_attempt")) is not int
            or run.get("id") != run_id
            or run.get("run_attempt") != attempt
        ):
            raise ReleaseCIError(
                "workflow attempt changed or does not match its search result"
            )
        runs.append(run)
    return runs


def wait_for_release_ci(
    repository: str,
    commit: str,
    *,
    timeout: float = 3600,
    interval: float = 15,
    check: bool = False,
    api_call: Callable[[str], Any] = github_api,
    sleep: Callable[[float], None] = time.sleep,
    monotonic: Callable[[], float] = time.monotonic,
    now: Callable[[], dt.datetime] = lambda: dt.datetime.now(UTC),
    report: Callable[[str], None] = lambda message: print(message, file=sys.stderr),
) -> dict[str, Any]:
    validate_identity(repository, commit)
    if not 0 <= timeout <= 86400 or not 0 < interval <= 60:
        raise ReleaseCIError(
            "timeout must be 0..86400 seconds and interval 0..60 seconds"
        )
    deadline = monotonic() + timeout

    def bounded_api(path: str) -> Any:
        if not check and timeout > 0 and monotonic() >= deadline:
            raise ReleaseCIError("main CI wait timed out during metadata reads")
        return api_call(path)

    previous = ""
    while True:
        runs = load_runs(repository, commit, api_call=bounded_api)
        if not check and timeout > 0 and monotonic() > deadline:
            raise ReleaseCIError("main CI wait timed out during metadata reads")
        state = evaluate_runs(repository, commit, runs, completed_before=now())
        if state.state == "ready":
            assert state.run is not None
            return state.run
        if state.state == "failed":
            raise ReleaseCIError(state.reason)
        if state.reason != previous:
            report(state.reason)
            previous = state.reason
        remaining = deadline - monotonic()
        if check or remaining <= 0:
            raise ReleaseCIError(
                "main CI is not ready; do not create or push the release tag"
            )
        sleep(min(interval, remaining))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--timeout", type=float, default=3600)
    parser.add_argument("--interval", type=float, default=15)
    parser.add_argument(
        "--check", action="store_true", help="Check once without waiting"
    )
    args = parser.parse_args()
    try:
        run = wait_for_release_ci(
            args.repository,
            args.commit,
            timeout=args.timeout,
            interval=args.interval,
            check=args.check,
        )
    except ReleaseCIError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(
        json.dumps(
            {
                "repository": args.repository,
                "commit": args.commit,
                "run_id": run["id"],
                "run_attempt": run["run_attempt"],
                "completed_at": run["updated_at"],
                "url": f'https://github.com/{args.repository}/actions/runs/{run["id"]}',
            }
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
