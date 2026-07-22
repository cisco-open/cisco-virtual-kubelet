#!/usr/bin/env python3
# Copyright 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Upsert the cisco-open-native lab CI report on an upstream PR.

The reporter runs on a GitHub-hosted runner from trusted ``main`` after a lab
job completes. It rebuilds one canary report from commit statuses and the
small, sanitized evidence artifacts produced by the native Cat8kv/Cat9k
wrappers. It deliberately does not consume raw Argo objects or pod logs.

Required environment variables:
  REPOSITORY  Current ``owner/repo`` (status and comment destination)
  HEAD_SHA    Verified PR head SHA to which statuses were posted
  BASE_SHA    Verified base SHA used by both lab workflows
  MERGE_SHA   Verified prospective merge SHA tested by both lab workflows
  PR_NUMBER   Verified open PR number
  GH_TOKEN    Built-in token for this repository
"""

from __future__ import annotations

import http.client
import io
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from datetime import datetime, timezone
from typing import Any

GITHUB_API = "https://api.github.com"
EVIDENCE_SCHEMA = "cvk-lab-evidence/v1"
COMMENT_MARKER = "<!-- lab-ci-next-report -->"
GITHUB_ACTIONS_BOT_ID = 41_898_282
TERMINAL_STATES = frozenset({"success", "failure", "error"})

CONTEXTS: tuple[dict[str, str], ...] = (
    {
        "context": "lab-ci-next / cat8kv",
        "emoji": "🖥️",
        "label": "Cat8kv (virtual)",
        "artifact": "argo-evidence-cat8kv",
        "job": "cat8kv",
    },
    {
        "context": "lab-ci-next / cat9k",
        "emoji": "🛰️",
        "label": "Cat9k (physical)",
        "artifact": "argo-evidence-cat9k",
        "job": "cat9k",
    },
)

STATE_EMOJI = {
    "success": "✅",
    "failure": "❌",
    "error": "❌",
    "pending": "⏳",
}

REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
PR_RE = re.compile(r"^[1-9][0-9]*$")


class GitHubAPIError(RuntimeError):
    """A sanitized GitHub API failure safe to include in Actions logs."""

    def __init__(
        self,
        status: int,
        method: str,
        path: str,
        message: str = "",
        request_id: str = "",
    ) -> None:
        super().__init__(f"GitHub API returned HTTP {status}")
        self.status = status
        self.method = method
        self.path = path
        self.message = message
        self.request_id = request_id


def _github_api_error(error: urllib.error.HTTPError, method: str, path: str) -> GitHubAPIError:
    """Convert an HTTPError into bounded, repository-relative diagnostics."""
    message = ""
    try:
        payload = json.loads(error.read(65_536))
        if isinstance(payload, dict):
            message = str(payload.get("message") or "")
    except (UnicodeDecodeError, json.JSONDecodeError):
        pass
    message = " ".join(message.split())[:300]
    headers = error.headers or {}
    request_id = str(headers.get("X-GitHub-Request-Id", ""))[:100]
    safe_path = path.split("?", 1)[0]
    return GitHubAPIError(error.code, method, safe_path, message, request_id)


def github_api_error_detail(error: GitHubAPIError) -> str:
    """Format bounded GitHub diagnostics without headers, tokens, or absolute URLs."""
    detail = f"GitHub API {error.method} {error.path} returned HTTP {error.status}"
    if error.message:
        detail += f": {error.message}"
    if error.request_id:
        detail += f" (request {error.request_id})"
    return detail


def api(
    path: str,
    method: str = "GET",
    body: dict[str, Any] | None = None,
    token: str | None = None,
) -> Any:
    """Call the GitHub REST API and decode a JSON response."""
    if not path.startswith("/"):
        raise ValueError("GitHub API paths must start with '/'")
    auth_token = token if token is not None else os.environ.get("GH_TOKEN", "")
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "cisco-virtual-kubelet-lab-ci-report",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if auth_token:
        headers["Authorization"] = f"Bearer {auth_token}"
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode("utf-8")
    request = urllib.request.Request(
        f"{GITHUB_API}{path}",
        data=data,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = response.read()
    except urllib.error.HTTPError as error:
        raise _github_api_error(error, method, path) from error
    return json.loads(payload) if payload else None


def latest_status_per_context(
    repository: str,
    sha: str,
    base_sha: str,
    merge_sha: str,
) -> dict[str, dict[str, Any]]:
    """Return each newest trusted status for this exact prospective merge."""
    wanted = {entry["context"] for entry in CONTEXTS}
    expected_pin = f"base={base_sha}; merge={merge_sha}"
    latest: dict[str, dict[str, Any]] = {}
    for page in range(1, 11):
        statuses = api(
            f"/repos/{repository}/statuses/{sha}?per_page=100&page={page}"
        ) or []
        for status in statuses:
            context = status.get("context")
            creator = status.get("creator") or {}
            description = str(status.get("description") or "")
            if (
                context in wanted
                and context not in latest
                and creator.get("id") == GITHUB_ACTIONS_BOT_ID
                and expected_pin in description
            ):
                latest[context] = status
        if wanted.issubset(latest) or len(statuses) < 100:
            break
    return latest


def all_contexts_terminal(latest: dict[str, dict[str, Any]]) -> bool:
    """Return true only when every expected native lab context has finished."""
    return all(
        str((latest.get(entry["context"]) or {}).get("state") or "")
        in TERMINAL_STATES
        for entry in CONTEXTS
    )


def run_id_from_target_url(url: str) -> int | None:
    match = re.search(r"/actions/runs/(\d+)(?:/|$)", url or "")
    return int(match.group(1)) if match else None


def duration_seconds(started: str | None, finished: str | None) -> int:
    if not started or not finished:
        return 0
    try:
        start = datetime.fromisoformat(started.replace("Z", "+00:00"))
        end = datetime.fromisoformat(finished.replace("Z", "+00:00"))
    except (TypeError, ValueError):
        return 0
    if (start.tzinfo is None) != (end.tzinfo is None):
        return 0
    return max(0, int((end - start).total_seconds()))


def format_duration(started: str | None, finished: str | None) -> str:
    seconds = duration_seconds(started, finished)
    if seconds <= 0:
        return ""
    minutes, seconds = divmod(seconds, 60)
    hours, minutes = divmod(minutes, 60)
    if hours:
        return f"{hours}h{minutes:02d}m{seconds:02d}s"
    if minutes:
        return f"{minutes}m{seconds:02d}s"
    return f"{seconds}s"


def fetch_run_duration(repository: str, run_id: int) -> str:
    run = api(f"/repos/{repository}/actions/runs/{run_id}") or {}
    return format_duration(run.get("run_started_at"), run.get("updated_at"))


def fetch_job_steps(repository: str, run_id: int, expected_job: str) -> list[dict[str, Any]]:
    """Return steps from the named lab job, never the first job by accident."""
    jobs: list[dict[str, Any]] = []
    for page in range(1, 11):
        payload = api(
            f"/repos/{repository}/actions/runs/{run_id}/jobs"
            f"?filter=all&per_page=100&page={page}"
        ) or {}
        page_jobs = payload.get("jobs", [])
        jobs.extend(page_jobs)
        if len(page_jobs) < 100:
            break

    exact = next((job for job in jobs if job.get("name") == expected_job), None)
    if exact is None:
        # GitHub appends matrix values to job names. The native wrappers are
        # not matrices today, but this keeps selection deterministic if that
        # changes later.
        prefix = f"{expected_job} ("
        exact = next(
            (job for job in jobs if str(job.get("name", "")).startswith(prefix)),
            None,
        )
    return list((exact or {}).get("steps", []))


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Expose GitHub's signed artifact redirect instead of following it."""

    def redirect_request(  # type: ignore[override]
        self,
        req: urllib.request.Request,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> None:
        return None


def http_get_bytes(url: str, token: str | None = None) -> bytes:
    headers = {
        "Accept": "application/octet-stream",
        "User-Agent": "cisco-virtual-kubelet-lab-ci-report",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return response.read()


def download_artifact_archive(archive_url: str, token: str) -> bytes:
    """Download an artifact without forwarding auth to signed storage.

    GitHub's archive endpoint requires authentication and responds with a 302
    to a short-lived storage URL. We stop that redirect, validate the target,
    then make a fresh request without the Authorization header.
    """
    request = urllib.request.Request(
        archive_url,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "cisco-virtual-kubelet-lab-ci-report",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    opener = urllib.request.build_opener(_NoRedirect())
    try:
        with opener.open(request, timeout=30) as response:
            # Retain compatibility if GitHub ever serves the ZIP directly.
            return response.read()
    except urllib.error.HTTPError as error:
        if error.code not in {301, 302, 303, 307, 308}:
            raise
        location = error.headers.get("Location", "")
        parsed = urllib.parse.urlparse(location)
        if parsed.scheme != "https" or not parsed.hostname:
            raise ValueError("artifact redirect was not a valid HTTPS URL") from error
        # Intentionally omit the token: the redirect URL is already signed.
        return http_get_bytes(location)


def fetch_artifact_file(
    repository: str,
    run_id: int,
    artifact_name: str,
    filename: str = "argo-evidence.json",
) -> bytes | None:
    try:
        payload = api(
            f"/repos/{repository}/actions/runs/{run_id}/artifacts"
            f"?name={urllib.parse.quote(artifact_name)}&per_page=100"
        ) or {}
        artifacts = [
            artifact
            for artifact in payload.get("artifacts", [])
            if artifact.get("name") == artifact_name and not artifact.get("expired", False)
        ]
        if not artifacts:
            return None
        artifact = max(artifacts, key=lambda item: int(item.get("id", 0)))
        archive_url = artifact.get("archive_download_url")
        if not archive_url:
            return None
        archive = download_artifact_archive(archive_url, os.environ["GH_TOKEN"])
        with zipfile.ZipFile(io.BytesIO(archive)) as zipped:
            candidates = [
                name
                for name in zipped.namelist()
                if name == filename or name.endswith(f"/{filename}")
            ]
            if not candidates:
                return None
            return zipped.read(sorted(candidates)[0])
    except GitHubAPIError as error:
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            f"was unavailable ({github_api_error_detail(error)})",
            file=sys.stderr,
        )
    except urllib.error.HTTPError as error:
        host = urllib.parse.urlparse(error.url).hostname
        target = "GitHub API" if host == "api.github.com" else "artifact storage"
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            f"was unavailable ({target} HTTP {error.code})",
            file=sys.stderr,
        )
    except urllib.error.URLError:
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            "was unavailable (network request failed)",
            file=sys.stderr,
        )
    except (OSError, http.client.HTTPException):
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            "was unavailable (artifact transport failed)",
            file=sys.stderr,
        )
    except zipfile.BadZipFile:
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            "was unavailable (invalid ZIP archive)",
            file=sys.stderr,
        )
    except ValueError:
        print(
            f"warning: optional artifact {artifact_name!r} for run {run_id} "
            "was unavailable (invalid artifact response)",
            file=sys.stderr,
        )
    return None


def parse_sanitized_evidence(content: bytes) -> dict[str, Any]:
    """Parse and validate the intentionally small public evidence schema."""
    document = json.loads(content)
    if not isinstance(document, dict) or document.get("schema") != EVIDENCE_SCHEMA:
        raise ValueError("unsupported lab evidence schema")
    scenarios = document.get("scenarios")
    if not isinstance(scenarios, list):
        raise ValueError("lab evidence scenarios must be a list")

    clean: list[dict[str, str]] = []
    allowed_phases = {
        "Succeeded",
        "Failed",
        "Error",
        "Running",
        "Pending",
        "Skipped",
        "Omitted",
        "Unknown",
    }
    for row in scenarios:
        if not isinstance(row, dict):
            raise ValueError("lab evidence scenario must be an object")
        phase = str(row.get("phase") or "Unknown")
        if phase not in allowed_phases:
            phase = "Unknown"
        clean.append(
            {
                "crd": str(row.get("crd") or "Lab")[:100],
                "name": str(row.get("name") or "unnamed")[:150],
                "template": str(row.get("template") or "")[:150],
                "phase": phase,
                "started_at": str(row.get("started_at") or "")[:40],
                "finished_at": str(row.get("finished_at") or "")[:40],
            }
        )

    return {
        "schema": EVIDENCE_SCHEMA,
        "workflow": str(document.get("workflow") or "")[:253],
        "phase": str(document.get("phase") or "Unknown")[:30],
        "started_at": str(document.get("started_at") or "")[:40],
        "finished_at": str(document.get("finished_at") or "")[:40],
        "scenarios": clean,
    }


def escape_cell(value: str) -> str:
    return value.replace("|", "\\|").replace("\n", " ").replace("\r", " ")


def render_scenarios(evidence: dict[str, Any]) -> list[str]:
    scenarios = sorted(
        evidence.get("scenarios", []),
        key=lambda row: (row.get("started_at", ""), row.get("crd", ""), row.get("name", "")),
    )
    if not scenarios:
        return ["", "_No scenario-level evidence was emitted by this run._"]

    lines = [
        "",
        "**Sanitized Argo scenarios**",
        "",
        "| CRD | Scenario | Template | Result | Duration |",
        "| :--- | :--- | :--- | :---: | :---: |",
    ]
    for row in scenarios:
        phase = row["phase"]
        icon = {
            "Succeeded": "✅",
            "Failed": "❌",
            "Error": "❌",
            "Running": "⏳",
            "Pending": "⏳",
            "Skipped": "⏭️",
            "Omitted": "⏭️",
        }.get(phase, "❔")
        duration = format_duration(row.get("started_at"), row.get("finished_at")) or "—"
        lines.append(
            f"| `{escape_cell(row['crd'])}` | `{escape_cell(row['name'])}` | "
            f"`{escape_cell(row['template']) or '—'}` | {icon} `{phase.lower()}` | {duration} |"
        )
    return lines


def render_report(
    repository: str,
    head_sha: str,
    latest: dict[str, dict[str, Any]],
) -> str:
    """Render the complete native canary report from current GitHub state."""
    lines = [
        COMMENT_MARKER,
        "## 🤖 Native Lab CI canary report",
        "",
        f"PR head `{head_sha[:12]}` · lab workflows test its prospective merge with `main`.",
        "",
        "| Check | Result | Duration | Details |",
        "| :--- | :---: | :---: | :--- |",
    ]
    pass_count = fail_count = pending_count = 0
    detail_data: list[tuple[dict[str, str], dict[str, Any], int]] = []

    for entry in CONTEXTS:
        status = latest.get(entry["context"])
        if status is None:
            state, duration, link = "pending", "—", "—"
            pending_count += 1
        else:
            state = str(status.get("state") or "pending")
            if state == "success":
                pass_count += 1
            elif state in {"failure", "error"}:
                fail_count += 1
            else:
                pending_count += 1
            target_url = str(status.get("target_url") or "")
            run_id = run_id_from_target_url(target_url)
            duration = fetch_run_duration(repository, run_id) if run_id else ""
            duration = duration or "—"
            link = f"[run]({target_url})" if target_url else "—"
            if run_id:
                detail_data.append((entry, status, run_id))
        lines.append(
            f"| {entry['emoji']} {entry['label']} | "
            f"{STATE_EMOJI.get(state, '❔')} `{state}` | {duration} | {link} |"
        )

    lines.append("")
    totals: list[str] = []
    if pass_count:
        totals.append(f"**{pass_count} passed** ✅")
    if fail_count:
        totals.append(f"**{fail_count} failed** ❌")
    if pending_count:
        totals.append(f"**{pending_count} pending** ⏳")
    lines.append("**Summary:** " + " · ".join(totals))

    for entry, status, run_id in detail_data:
        steps = fetch_job_steps(repository, run_id, entry["job"])
        evidence_content = fetch_artifact_file(repository, run_id, entry["artifact"])
        try:
            evidence = parse_sanitized_evidence(evidence_content) if evidence_content else None
        except (ValueError, json.JSONDecodeError) as error:
            print(
                f"warning: optional artifact {entry['artifact']!r} for run {run_id} "
                f"contained invalid evidence ({error})",
                file=sys.stderr,
            )
            evidence = None
        visible_steps = [step for step in steps if step.get("conclusion") != "skipped"]
        state = str(status.get("state") or "pending")
        lines.extend(
            [
                "",
                f"<details><summary>{entry['emoji']} {entry['label']} — "
                f"{STATE_EMOJI.get(state, '❔')} {len(visible_steps)} lab-wrapper steps</summary>",
            ]
        )
        if evidence:
            lines.extend(render_scenarios(evidence))
        else:
            lines.extend(["", "_No sanitized Argo evidence was available for this run._"])
        if visible_steps:
            lines.extend(
                [
                    "",
                    "**Lab-wrapper steps**",
                    "",
                    "| # | Step | Result | Duration |",
                    "| ---: | :--- | :---: | :---: |",
                ]
            )
            for index, step in enumerate(visible_steps, 1):
                result = str(step.get("conclusion") or step.get("status") or "unknown")
                icon = {
                    "success": "✅",
                    "failure": "❌",
                    "cancelled": "🚫",
                    "in_progress": "⏳",
                }.get(result, "❔")
                duration = format_duration(step.get("started_at"), step.get("completed_at")) or "—"
                name = escape_cell(str(step.get("name") or "unnamed"))
                lines.append(f"| {index} | {name} | {icon} `{result}` | {duration} |")
        lines.extend(["", "</details>"])

    updated = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    lines.extend(
        [
            "",
            "---",
            f"_Updated {updated}. Artifacts contain sanitized status metadata only; raw device logs are not published._",
        ]
    )
    return "\n".join(lines)


def upsert_comment(repository: str, pr_number: str, body: str) -> None:
    existing_id: int | None = None
    for page in range(1, 11):
        comments = api(
            f"/repos/{repository}/issues/{pr_number}/comments?per_page=100&page={page}"
        ) or []
        for comment in comments:
            author = (comment.get("user") or {}).get("login")
            if (
                COMMENT_MARKER in str(comment.get("body") or "")
                and author == "github-actions[bot]"
            ):
                existing_id = int(comment["id"])
                break
        if existing_id is not None or len(comments) < 100:
            break

    if existing_id is None:
        api(
            f"/repos/{repository}/issues/{pr_number}/comments",
            method="POST",
            body={"body": body},
        )
    else:
        api(
            f"/repos/{repository}/issues/comments/{existing_id}",
            method="PATCH",
            body={"body": body},
        )


def validate_inputs(
    repository: str,
    head_sha: str,
    base_sha: str,
    merge_sha: str,
    pr_number: str,
) -> None:
    if not REPOSITORY_RE.fullmatch(repository):
        raise ValueError("REPOSITORY must be an owner/repo name")
    if not SHA_RE.fullmatch(head_sha):
        raise ValueError("HEAD_SHA must be a 40-character lowercase hex SHA")
    if not SHA_RE.fullmatch(base_sha):
        raise ValueError("BASE_SHA must be a 40-character lowercase hex SHA")
    if not SHA_RE.fullmatch(merge_sha):
        raise ValueError("MERGE_SHA must be a 40-character lowercase hex SHA")
    if not PR_RE.fullmatch(pr_number):
        raise ValueError("PR_NUMBER must be a positive integer")


def pr_still_has_head(repository: str, pr_number: str, head_sha: str) -> bool:
    """Prevent an older run from overwriting the report for a newer PR head."""
    pull = api(f"/repos/{repository}/pulls/{pr_number}") or {}
    return pull.get("state") == "open" and pull.get("head", {}).get("sha") == head_sha


def main() -> int:
    required = (
        "REPOSITORY",
        "HEAD_SHA",
        "BASE_SHA",
        "MERGE_SHA",
        "PR_NUMBER",
        "GH_TOKEN",
    )
    missing = [name for name in required if not os.environ.get(name)]
    if missing:
        print(f"error: missing required environment: {', '.join(missing)}", file=sys.stderr)
        return 2

    repository = os.environ["REPOSITORY"]
    head_sha = os.environ["HEAD_SHA"]
    base_sha = os.environ["BASE_SHA"]
    merge_sha = os.environ["MERGE_SHA"]
    pr_number = os.environ["PR_NUMBER"]
    try:
        validate_inputs(repository, head_sha, base_sha, merge_sha, pr_number)
        if not pr_still_has_head(repository, pr_number, head_sha):
            print("notice: PR head changed or PR closed; skipping stale report")
            return 0
        latest = latest_status_per_context(
            repository,
            head_sha,
            base_sha,
            merge_sha,
        )
        if not all_contexts_terminal(latest):
            states = ", ".join(
                f"{entry['context']}="
                f"{(latest.get(entry['context']) or {}).get('state', 'missing')}"
                for entry in CONTEXTS
            )
            print(f"notice: waiting for terminal native lab statuses ({states})")
            return 0
        report = render_report(repository, head_sha, latest)
        upsert_comment(repository, pr_number, report)
    except (ValueError, json.JSONDecodeError, zipfile.BadZipFile) as error:
        print(f"error: {error}", file=sys.stderr)
        return 2
    except GitHubAPIError as error:
        print(f"error: {github_api_error_detail(error)}", file=sys.stderr)
        return 1
    except urllib.error.HTTPError as error:
        print(f"error: HTTP request returned {error.code}", file=sys.stderr)
        return 1
    except urllib.error.URLError as error:
        print(f"error: GitHub API request failed: {error.reason}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
