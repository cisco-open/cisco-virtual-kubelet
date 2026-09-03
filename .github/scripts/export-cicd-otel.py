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
"""Export completed GitHub workflow metadata as deterministic OTLP/HTTP JSON.

This observer runs trusted code from ``main`` on the lab runner. It reads only
GitHub's workflow metadata API and sends only to the loopback Collector. It
never downloads or executes code from the observed run.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Callable

from cicd_otel import (
    CorrelationError,
    github_job_span_id,
    github_queue_span_id,
    github_release_published_span_id,
    github_release_published_traceparent,
    github_release_trace_id,
    github_root_span_id,
    github_root_traceparent,
    github_step_span_id,
    github_trace_id,
    positive_integer,
    pr_lifecycle_id,
    release_lifecycle_id,
    run_lifecycle_id,
    split_traceparent,
    validate_lifecycle_id,
    validate_traceparent,
)


GITHUB_API = "https://api.github.com"
GITHUB_METADATA_API_VERSION = "2022-11-28"
GITHUB_RELEASE_API_VERSION = "2026-03-10"
OTLP_ENDPOINT = "http://127.0.0.1:4318/v1/traces"
HTTP_TIMEOUT_SECONDS = 15
OTLP_TIMEOUT_SECONDS = 10
MAX_API_PAGES = 20
MAX_RESPONSE_BYTES = 20 * 1024 * 1024
MAX_OTLP_BYTES = 10 * 1024 * 1024
MAX_JOBS = 1000
MAX_STEPS = 5000
MAX_CORRELATION_PAGES = 5
MAX_CORRELATION_CANDIDATES = 500
OBSERVER_WORKFLOW_PATH = ".github/workflows/cicd-otel-export.yaml"
OBSERVED_WORKFLOW_PATHS = {
    "smoke": ".github/workflows/smoke.yml",
    "Lab CI approval signal": ".github/workflows/lab-ci-approval-signal.yaml",
    "Lab CI approved dispatcher": ".github/workflows/lab-ci-auto-dispatch.yaml",
    "Lab CI (Cat8kv)": ".github/workflows/lab-ci-cat8kv.yaml",
    "Lab CI (Cat9k)": ".github/workflows/lab-ci-cat9k.yaml",
    "lab-ci-static": ".github/workflows/lab-ci-static.yaml",
    "release": ".github/workflows/release.yml",
    "CI build and deploy documentation": ".github/workflows/develop.yml",
    "krew-index": ".github/workflows/krew-index.yml",
    "ci-tiers": ".github/workflows/tiers.yml",
    "yang-drift": ".github/workflows/yang-drift.yml",
    "recover v2026.8.1 release assets": ".github/workflows/recover-v2026.8.1-release-assets.yml",
}
OBSERVED_WORKFLOW_NAMES = {
    path: name for name, path in OBSERVED_WORKFLOW_PATHS.items()
}
REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
TITLE_LIFECYCLE_RE = re.compile(
    r"(?:^| - )(cvk-(?:pr[1-9][0-9]*-h[0-9a-f]{40}"
    r"|release-v[1-9][0-9]{3}\.(?:[1-9]|1[0-2])\."
    r"(?:0|[1-9][0-9]{0,8})))(?: - |$)"
)
TITLE_TRACEPARENT_RE = re.compile(
    r" - (00-[0-9a-f]{32}-[0-9a-f]{16}-01)$"
)
TITLE_UPSTREAM_RUN_RE = re.compile(
    r"(?:^| - )github-upstream-run([1-9][0-9]*)-a([1-9][0-9]*)(?: - |$)"
)
TITLE_LIFECYCLE_WORKFLOWS = {
    "Lab CI approval signal",
    "Lab CI (Cat8kv)",
    "Lab CI (Cat9k)",
}

SMOKE_WORKFLOW_NAME = "smoke"
SMOKE_WORKFLOW_PATH = ".github/workflows/smoke.yml"
RELEASE_WORKFLOW_NAME = "release"
RELEASE_WORKFLOW_PATH = ".github/workflows/release.yml"
PUBLICATION_DOWNSTREAM_WORKFLOWS = {
    "CI build and deploy documentation": ".github/workflows/develop.yml",
    "krew-index": ".github/workflows/krew-index.yml",
}


class ExportError(RuntimeError):
    """Observed metadata or local export failed a closed validation."""


UrlOpen = Callable[..., Any]


@dataclass(frozen=True)
class SpanReference:
    """A validated, explicit causal reference rendered as an OTLP span link."""

    traceparent: str
    link_type: str
    upstream_lifecycle_id: str = ""

    def __post_init__(self) -> None:
        validate_traceparent(self.traceparent)
        if not re.fullmatch(r"[a-z0-9_.-]{1,64}", self.link_type):
            raise CorrelationError(f"invalid span link type: {self.link_type!r}")
        if self.upstream_lifecycle_id:
            validate_lifecycle_id(self.upstream_lifecycle_id)


@dataclass(frozen=True)
class RunCorrelation:
    """Pure correlation input supplied to the deterministic span renderer."""

    lifecycle_id: str
    references: tuple[SpanReference, ...] = ()
    state: str = "base"
    warnings: tuple[str, ...] = ()
    required_transition_failed: bool = False

    def __post_init__(self) -> None:
        validate_lifecycle_id(self.lifecycle_id)
        if not re.fullmatch(r"[a-z0-9_.-]{1,64}", self.state):
            raise CorrelationError(f"invalid correlation state: {self.state!r}")
        if not isinstance(self.references, tuple) or not all(
            isinstance(reference, SpanReference) for reference in self.references
        ):
            raise CorrelationError("correlation references must be a tuple of SpanReference")
        if not isinstance(self.warnings, tuple) or not all(
            isinstance(warning, str) and warning for warning in self.warnings
        ):
            raise CorrelationError("correlation warnings must be non-empty strings")
        if not isinstance(self.required_transition_failed, bool):
            raise CorrelationError("required-transition failure flag must be boolean")


@dataclass(frozen=True)
class MergedPullRequest:
    number: int
    head_sha: str
    merged_at: int

    def __post_init__(self) -> None:
        positive_integer("GitHub pull request number", self.number)
        if SHA_RE.fullmatch(self.head_sha) is None:
            raise CorrelationError(f"invalid PR head SHA: {self.head_sha!r}")
        if not isinstance(self.merged_at, int) or self.merged_at < 0:
            raise CorrelationError("invalid PR merge timestamp")

    @property
    def lifecycle_id(self) -> str:
        return pr_lifecycle_id(self.number, self.head_sha)


@dataclass(frozen=True)
class PublishedRelease:
    release_id: int
    tag: str
    commit_sha: str
    published_at: int
    html_url: str

    def __post_init__(self) -> None:
        positive_integer("GitHub release ID", self.release_id)
        release_lifecycle_id(self.tag)
        if SHA_RE.fullmatch(self.commit_sha) is None:
            raise CorrelationError(
                f"invalid release tag commit SHA: {self.commit_sha!r}"
            )
        if not isinstance(self.published_at, int) or self.published_at <= 0:
            raise CorrelationError("invalid release publication timestamp")

    @property
    def lifecycle_id(self) -> str:
        return release_lifecycle_id(self.tag)


def validate_repository(repository: str) -> str:
    if not REPOSITORY_RE.fullmatch(repository):
        raise ExportError(f"invalid repository: {repository!r}")
    return repository


def _read_bounded(response: Any) -> bytes:
    length = response.headers.get("Content-Length")
    if length is not None and int(length) > MAX_RESPONSE_BYTES:
        raise ExportError("HTTP response exceeds size limit")
    payload = response.read(MAX_RESPONSE_BYTES + 1)
    if not isinstance(payload, bytes):
        raise ExportError("HTTP response body was not bytes")
    if len(payload) > MAX_RESPONSE_BYTES:
        raise ExportError("HTTP response exceeds size limit")
    return payload


def github_api(
    path: str,
    *,
    token: str,
    opener: UrlOpen = urllib.request.urlopen,
) -> Any:
    if not path.startswith("/"):
        raise ExportError("GitHub API path must start with '/'")
    if not token:
        raise ExportError("GH_TOKEN is required for metadata reads")
    api_version = (
        GITHUB_RELEASE_API_VERSION
        if re.match(r"^/repos/[^/]+/[^/]+/releases(?:/|\?|$)", path)
        else GITHUB_METADATA_API_VERSION
    )
    request = urllib.request.Request(
        f"{GITHUB_API}{path}",
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "cisco-virtual-kubelet-cicd-otel-observer",
            # ``immutable`` uses the release API version pinned by the release
            # and Krew workflows. Workflow/PR metadata stays on GitHub's stable
            # version, where merged PRs retain ``merge_commit_sha``.
            "X-GitHub-Api-Version": api_version,
        },
    )
    try:
        with opener(request, timeout=HTTP_TIMEOUT_SECONDS) as response:
            payload = _read_bounded(response)
    except (OSError, urllib.error.URLError) as error:
        raise ExportError(f"GitHub metadata request failed: {error}") from error
    try:
        return json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ExportError("GitHub metadata response was not valid JSON") from error


def load_completed_run(
    repository: str,
    run_id: str | int,
    run_attempt: str | int,
    *,
    token: str,
    api_call: Callable[[str], Any] | None = None,
) -> dict[str, Any]:
    repository = validate_repository(repository)
    expected_id = positive_integer("observed GitHub run ID", run_id)
    expected_attempt = positive_integer("observed GitHub run attempt", run_attempt)
    call = api_call or (lambda path: github_api(path, token=token))
    run = call(
        f"/repos/{repository}/actions/runs/{expected_id}/attempts/{expected_attempt}"
    )
    if not isinstance(run, dict):
        raise ExportError("GitHub workflow run response is not an object")
    if int(run.get("id") or 0) != expected_id:
        raise ExportError("GitHub workflow run ID does not match the event")
    if ((run.get("repository") or {}).get("full_name")) != repository:
        raise ExportError("GitHub workflow run repository does not match")
    if run.get("status") != "completed":
        raise ExportError("only completed GitHub workflow runs may be exported")
    actual_path = _workflow_path(run)
    if actual_path == OBSERVER_WORKFLOW_PATH:
        raise ExportError("the observer must never observe itself")
    if actual_path not in OBSERVED_WORKFLOW_NAMES:
        raise ExportError(
            f"workflow at {actual_path!r} is not observed"
        )
    actual_attempt = positive_integer(
        "GitHub run attempt", run.get("run_attempt", "")
    )
    if actual_attempt != expected_attempt:
        raise ExportError("GitHub workflow run attempt does not match the event")
    return run


def load_jobs(
    repository: str,
    run_id: str | int,
    run_attempt: str | int,
    *,
    token: str,
    api_call: Callable[[str], Any] | None = None,
) -> list[dict[str, Any]]:
    repository = validate_repository(repository)
    run = positive_integer("observed GitHub run ID", run_id)
    attempt = positive_integer("GitHub run attempt", run_attempt)
    call = api_call or (lambda path: github_api(path, token=token))
    jobs: list[dict[str, Any]] = []
    for page in range(1, MAX_API_PAGES + 1):
        response = call(
            f"/repos/{repository}/actions/runs/{run}/attempts/{attempt}/jobs"
            f"?per_page=100&page={page}"
        )
        batch = (response or {}).get("jobs") if isinstance(response, dict) else None
        if not isinstance(batch, list):
            raise ExportError("GitHub jobs response is invalid")
        jobs.extend(batch)
        if len(jobs) > MAX_JOBS:
            raise ExportError("GitHub workflow exceeds the job safety limit")
        if len(batch) < 100:
            return jobs
    raise ExportError("GitHub jobs pagination exceeded the safety limit")


def _query_path(path: str, parameters: list[tuple[str, object]]) -> str:
    return f"{path}?{urllib.parse.urlencode(parameters)}"


def _load_correlation_pages(
    path: str,
    *,
    list_field: str | None,
    api_call: Callable[[str], Any],
) -> list[dict[str, Any]]:
    """Load a small, bounded list used only to find causal predecessors."""
    results: list[dict[str, Any]] = []
    for page in range(1, MAX_CORRELATION_PAGES + 1):
        separator = "&" if "?" in path else "?"
        response = api_call(f"{path}{separator}per_page=100&page={page}")
        if list_field is None:
            batch = response
        else:
            batch = response.get(list_field) if isinstance(response, dict) else None
        if not isinstance(batch, list) or not all(
            isinstance(item, dict) for item in batch
        ):
            raise ExportError("GitHub correlation list response is invalid")
        results.extend(batch)
        if len(results) > MAX_CORRELATION_CANDIDATES:
            raise ExportError("GitHub correlation lookup exceeds its safety limit")
        if len(batch) < 100:
            return results
    raise ExportError("GitHub correlation pagination exceeded its safety limit")


def _workflow_path(run: dict[str, Any]) -> str:
    return str(run.get("path") or "").split("@", 1)[0]


def _workflow_name(run: dict[str, Any]) -> str:
    """Return the stable workflow name associated with GitHub's server path.

    Runs that define ``run-name`` can expose that dynamic title through the
    attempts API's ``name`` field. The repository-owned workflow path remains
    stable and is already the observer's security allow-list boundary.
    """
    return OBSERVED_WORKFLOW_NAMES.get(_workflow_path(run), "")


def _validated_sha(value: object, field: str) -> str:
    rendered = str(value or "")
    if SHA_RE.fullmatch(rendered) is None:
        raise ExportError(f"invalid {field}: {rendered!r}")
    return rendered


def _workflow_reference(
    run: dict[str, Any],
    link_type: str,
    *,
    upstream_lifecycle_id: str = "",
) -> SpanReference:
    return SpanReference(
        traceparent=github_root_traceparent(
            run.get("id", ""), run.get("run_attempt", "")
        ),
        link_type=link_type,
        upstream_lifecycle_id=upstream_lifecycle_id,
    )


def _with_correlation(
    base: RunCorrelation,
    *,
    lifecycle_id: str | None = None,
    references: tuple[SpanReference, ...] = (),
    state: str,
    warnings: tuple[str, ...] = (),
    required_transition_failed: bool = False,
) -> RunCorrelation:
    # Stable ordering makes repeated observer deliveries byte-for-byte
    # reproducible. Keep semantically different links to one span, but remove
    # exact duplicates.
    deduplicated = tuple(dict.fromkeys((*base.references, *references)))
    return RunCorrelation(
        lifecycle_id=lifecycle_id or base.lifecycle_id,
        references=deduplicated,
        state=state,
        warnings=(*base.warnings, *warnings),
        required_transition_failed=(
            base.required_transition_failed or required_transition_failed
        ),
    )


def _timestamp(value: object, field: str) -> int:
    if not isinstance(value, str) or not value:
        raise ExportError(f"missing timestamp: {field}")
    try:
        parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ExportError(f"invalid timestamp {field}: {value!r}") from error
    if parsed.tzinfo is None:
        raise ExportError(f"timestamp {field} has no timezone")
    return int(parsed.timestamp() * 1_000_000_000)


def _time_range(
    record: dict[str, Any],
    start_fields: tuple[str, ...],
    end_fields: tuple[str, ...],
    *,
    fallback: tuple[int, int] | None = None,
) -> tuple[int, int]:
    start_value = next((record.get(field) for field in start_fields if record.get(field)), None)
    end_value = next((record.get(field) for field in end_fields if record.get(field)), None)
    if start_value is None or end_value is None:
        if fallback is None:
            raise ExportError("workflow metadata has no complete time range")
        start = fallback[0] if start_value is None else _timestamp(start_value, start_fields[0])
        end = fallback[1] if end_value is None else _timestamp(end_value, end_fields[0])
    else:
        start = _timestamp(start_value, start_fields[0])
        end = _timestamp(end_value, end_fields[0])
    if end < start:
        # GitHub reports reversed timestamps for some skipped jobs and steps.
        # Match githubreceiver by retaining the later timestamp as a
        # zero-duration span instead of dropping the whole workflow trace.
        end = start
    return start, end


def _attribute(key: str, value: object) -> dict[str, Any]:
    encoded: dict[str, object]
    if isinstance(value, bool):
        encoded = {"boolValue": value}
    elif isinstance(value, int):
        encoded = {"intValue": str(value)}
    else:
        encoded = {"stringValue": str(value)}
    return {"key": key, "value": encoded}


def _attributes(values: dict[str, object]) -> list[dict[str, Any]]:
    return [_attribute(key, value) for key, value in values.items() if value != ""]


def _status(conclusion: object) -> dict[str, object]:
    rendered = str(conclusion or "unknown")
    if rendered == "success":
        code = 1
    elif rendered in {
        "action_required",
        "cancelled",
        "failure",
        "stale",
        "startup_failure",
        "timed_out",
    }:
        code = 2
    # GitHub's neutral and skipped conclusions do not mean the operation
    # failed. Unknown/future conclusions remain UNSET rather than being
    # misclassified, while the original value stays available in message.
    else:
        code = 0
    return {"code": code, "message": rendered}


def lifecycle_for_run(run: dict[str, Any]) -> str:
    # Pull-request metadata comes from GitHub's API and cannot be forged by a
    # contributor-controlled PR title. Always prefer it to display_title.
    pulls = run.get("pull_requests") or []
    if isinstance(pulls, list) and len(pulls) == 1:
        pull = pulls[0] or {}
        try:
            return pr_lifecycle_id(
                pull.get("number", ""),
                ((pull.get("head") or {}).get("sha", "")),
            )
        except (AttributeError, CorrelationError):
            pass

    workflow_name = _workflow_name(run)
    event = str(run.get("event") or "")
    tag = str(run.get("head_branch") or "")
    is_release_build = workflow_name == RELEASE_WORKFLOW_NAME and event == "push"
    is_publication_downstream = (
        workflow_name in PUBLICATION_DOWNSTREAM_WORKFLOWS and event == "release"
    )
    if is_release_build or is_publication_downstream:
        try:
            return release_lifecycle_id(tag)
        except CorrelationError:
            # The resolver records the malformed trusted metadata as a
            # correlation warning. Rendering must still retain the base trace.
            pass

    if workflow_name in TITLE_LIFECYCLE_WORKFLOWS:
        title = str(run.get("display_title") or "")
        candidates = {match.group(1) for match in TITLE_LIFECYCLE_RE.finditer(title)}
        if len(candidates) > 1:
            raise ExportError("workflow title contains multiple lifecycle IDs")
        if candidates:
            return validate_lifecycle_id(candidates.pop())
    return run_lifecycle_id(run.get("id", ""), run.get("run_attempt", ""))


def upstream_traceparents_for_run(run: dict[str, Any]) -> list[str]:
    title = str(run.get("display_title") or "")
    workflow_name = _workflow_name(run)
    candidates: list[str] = []
    if workflow_name in {"Lab CI (Cat8kv)", "Lab CI (Cat9k)"}:
        match = TITLE_TRACEPARENT_RE.search(title)
        if match is not None:
            split_traceparent(match.group(1))
            candidates.append(match.group(1))
    if workflow_name == "Lab CI approved dispatcher":
        for upstream_run, upstream_attempt in TITLE_UPSTREAM_RUN_RE.findall(title):
            candidates.append(
                "00-"
                f"{github_trace_id(upstream_run, upstream_attempt)}-"
                f"{github_root_span_id(upstream_run, upstream_attempt)}-01"
            )
    # Preserve title order while ensuring a repeated carrier cannot create
    # duplicate links in the emitted root span.
    return list(dict.fromkeys(candidates))


def base_correlation_for_run(run: dict[str, Any]) -> RunCorrelation:
    warnings: list[str] = []
    required_transition_failed = False
    try:
        lifecycle = lifecycle_for_run(run)
    except ExportError as error:
        lifecycle = run_lifecycle_id(run.get("id", ""), run.get("run_attempt", ""))
        warnings.append(f"trusted workflow correlation metadata is invalid: {error}")
        required_transition_failed = True

    workflow_name = _workflow_name(run)
    event = str(run.get("event") or "")
    if workflow_name in TITLE_LIFECYCLE_WORKFLOWS and lifecycle.startswith("cvk-run"):
        warnings.append("required PR lifecycle metadata is missing")
        required_transition_failed = True

    upstream_metadata_invalid = False
    try:
        upstream_traceparents = upstream_traceparents_for_run(run)
    except (CorrelationError, ExportError, TypeError, ValueError) as error:
        # A trusted workflow title can contain a carrier with the expected
        # shape but forbidden all-zero IDs. Keep the workflow's standalone
        # trace exportable and make the broken transition fail visibly.
        upstream_traceparents = []
        upstream_metadata_invalid = True
        warnings.append(f"trusted workflow trace context is invalid: {error}")
        required_transition_failed = True
    if workflow_name in {"Lab CI (Cat8kv)", "Lab CI (Cat9k)"} and not upstream_traceparents:
        if not upstream_metadata_invalid:
            warnings.append("required dispatcher-to-wrapper trace context is missing")
        required_transition_failed = True
    if (
        workflow_name == "Lab CI approved dispatcher"
        and event == "workflow_run"
        and not upstream_traceparents
    ):
        warnings.append("required trigger-to-dispatcher trace context is missing")
        required_transition_failed = True

    references = [
        SpanReference(
            traceparent=upstream,
            link_type="github.dispatch",
        )
        for upstream in upstream_traceparents
    ]
    attempt = positive_integer("GitHub run attempt", run.get("run_attempt", ""))
    if attempt > 1:
        references.append(
            SpanReference(
                traceparent=github_root_traceparent(run.get("id", ""), attempt - 1),
                link_type="github.workflow.retry",
            )
        )
    return RunCorrelation(
        lifecycle_id=lifecycle,
        references=tuple(references),
        state="base_metadata_invalid" if required_transition_failed else "base",
        warnings=tuple(warnings),
        required_transition_failed=required_transition_failed,
    )


def _pull_numbers_for_commit(
    repository: str,
    commit_sha: str,
    *,
    api_call: Callable[[str], Any],
) -> tuple[int, ...]:
    encoded_sha = urllib.parse.quote(commit_sha, safe="")
    summaries = _load_correlation_pages(
        f"/repos/{repository}/commits/{encoded_sha}/pulls",
        list_field=None,
        api_call=api_call,
    )
    numbers = {
        positive_integer("GitHub pull request number", summary.get("number", ""))
        for summary in summaries
    }
    return tuple(sorted(numbers))


def _merged_pull_request(
    repository: str,
    main_run: dict[str, Any],
    pull: dict[str, Any],
) -> MergedPullRequest | None:
    try:
        number = positive_integer("GitHub pull request number", pull.get("number", ""))
        merge_sha = _validated_sha(pull.get("merge_commit_sha"), "merge commit SHA")
        head_sha = _validated_sha((pull.get("head") or {}).get("sha"), "PR head SHA")
        merged_at = _timestamp(pull.get("merged_at"), "pull request merged_at")
        main_created = _timestamp(main_run.get("created_at"), "workflow created_at")
    except (AttributeError, CorrelationError, ExportError):
        return None
    base = pull.get("base") or {}
    base_repository = (base.get("repo") or {}).get("full_name")
    if not (
        pull.get("state") == "closed"
        and pull.get("merged") is True
        and base.get("ref") == "main"
        and base_repository == repository
        and merge_sha == main_run.get("head_sha")
        and merged_at <= main_created
    ):
        return None
    return MergedPullRequest(number=number, head_sha=head_sha, merged_at=merged_at)


def _pull_request_for_head(
    repository: str,
    workflow_run: dict[str, Any],
    pull: dict[str, Any],
) -> MergedPullRequest | None:
    """Validate a PR identity for a PR workflow without trusting its title."""
    try:
        number = positive_integer("GitHub pull request number", pull.get("number", ""))
        head_sha = _validated_sha((pull.get("head") or {}).get("sha"), "PR head SHA")
    except (AttributeError, CorrelationError, ExportError):
        return None
    base = pull.get("base") or {}
    base_repository = (base.get("repo") or {}).get("full_name")
    if not (
        base.get("ref") == "main"
        and base_repository == repository
        and head_sha == workflow_run.get("head_sha")
        and pull.get("state") in {"open", "closed"}
    ):
        return None
    merged_at_value = pull.get("merged_at")
    merged_at = _timestamp(merged_at_value, "pull request merged_at") if merged_at_value else 0
    return MergedPullRequest(number=number, head_sha=head_sha, merged_at=merged_at)


def _select_pull_request(
    repository: str,
    run: dict[str, Any],
    *,
    require_merge_commit: bool,
    api_call: Callable[[str], Any],
) -> tuple[MergedPullRequest | None, str]:
    head_sha = _validated_sha(run.get("head_sha"), "workflow head SHA")
    numbers = _pull_numbers_for_commit(repository, head_sha, api_call=api_call)
    if not numbers:
        return None, "direct_push" if require_merge_commit else "missing"

    matches: list[MergedPullRequest] = []
    for number in numbers:
        response = api_call(f"/repos/{repository}/pulls/{number}")
        if not isinstance(response, dict):
            raise ExportError("GitHub pull request response is not an object")
        if require_merge_commit:
            candidate = _merged_pull_request(repository, run, response)
        else:
            candidate = _pull_request_for_head(repository, run, response)
        if candidate is not None:
            matches.append(candidate)
    if len(matches) == 1:
        return matches[0], "linked"
    if len(matches) > 1:
        return None, "ambiguous"
    return None, "invalid"


def _load_workflow_candidates(
    repository: str,
    workflow_file: str,
    parameters: list[tuple[str, object]],
    *,
    api_call: Callable[[str], Any],
) -> list[dict[str, Any]]:
    workflow = urllib.parse.quote(workflow_file, safe="")
    path = _query_path(
        f"/repos/{repository}/actions/workflows/{workflow}/runs", parameters
    )
    summaries = _load_correlation_pages(
        path,
        list_field="workflow_runs",
        api_call=api_call,
    )
    run_attempts = {
        (
            positive_integer("GitHub workflow run ID", summary.get("id", "")),
            positive_integer(
                "GitHub workflow run attempt", summary.get("run_attempt", "")
            ),
        )
        for summary in summaries
    }
    candidates: list[dict[str, Any]] = []
    for run_id, run_attempt in sorted(run_attempts):
        # A failed point lookup must invalidate the correlation search. Skipping
        # it could turn two real candidates into one apparent match and create
        # a false causal link. The caller will retain and export its base trace.
        candidate = load_completed_run(
            repository,
            run_id,
            run_attempt,
            token="metadata-api-call",
            api_call=api_call,
        )
        candidates.append(candidate)
    return candidates


def _pull_request_matches_run(
    pulls: object,
    *,
    number: int,
    head_sha: str,
) -> bool:
    if not isinstance(pulls, list):
        return False
    # GitHub can empty this array after a PR branch is deleted. In that case
    # the exact refetched head SHA plus the unique workflow-run match remains
    # the trusted identity. A non-empty, contradictory array is rejected.
    if not pulls:
        return True
    if len(pulls) != 1 or not isinstance(pulls[0], dict):
        return False
    pull = pulls[0]
    try:
        pull_number = positive_integer("GitHub pull request number", pull.get("number", ""))
    except CorrelationError:
        return False
    return pull_number == number and (pull.get("head") or {}).get("sha") == head_sha


def _find_pull_request_smoke_run(
    repository: str,
    pull: MergedPullRequest,
    *,
    api_call: Callable[[str], Any],
) -> tuple[dict[str, Any] | None, str]:
    candidates = _load_workflow_candidates(
        repository,
        "smoke.yml",
        [
            ("event", "pull_request"),
            ("status", "success"),
            ("head_sha", pull.head_sha),
        ],
        api_call=api_call,
    )
    matches: list[dict[str, Any]] = []
    for candidate in candidates:
        try:
            completed_at = _timestamp(candidate.get("updated_at"), "workflow updated_at")
        except ExportError:
            continue
        if (
            _workflow_name(candidate) == SMOKE_WORKFLOW_NAME
            and _workflow_path(candidate) == SMOKE_WORKFLOW_PATH
            and candidate.get("event") == "pull_request"
            and candidate.get("conclusion") == "success"
            and candidate.get("head_sha") == pull.head_sha
            and completed_at <= pull.merged_at
            and _pull_request_matches_run(
                candidate.get("pull_requests"),
                number=pull.number,
                head_sha=pull.head_sha,
            )
        ):
            matches.append(candidate)
    if len(matches) == 1:
        return matches[0], "linked"
    return None, "ambiguous" if len(matches) > 1 else "missing"


def _find_successful_main_smoke_run(
    repository: str,
    commit_sha: str,
    before: int,
    *,
    api_call: Callable[[str], Any],
) -> tuple[dict[str, Any] | None, str]:
    candidates = _load_workflow_candidates(
        repository,
        "smoke.yml",
        [
            ("branch", "main"),
            ("event", "push"),
            ("status", "success"),
            ("head_sha", commit_sha),
        ],
        api_call=api_call,
    )
    matches: list[dict[str, Any]] = []
    for candidate in candidates:
        try:
            completed_at = _timestamp(candidate.get("updated_at"), "workflow updated_at")
        except ExportError:
            continue
        if (
            _workflow_name(candidate) == SMOKE_WORKFLOW_NAME
            and _workflow_path(candidate) == SMOKE_WORKFLOW_PATH
            and candidate.get("event") == "push"
            and candidate.get("head_branch") == "main"
            and candidate.get("head_sha") == commit_sha
            and candidate.get("conclusion") == "success"
            and completed_at <= before
        ):
            matches.append(candidate)
    if len(matches) == 1:
        return matches[0], "linked"
    return None, "ambiguous" if len(matches) > 1 else "missing"


def _find_successful_release_run(
    repository: str,
    release: PublishedRelease,
    *,
    api_call: Callable[[str], Any],
) -> tuple[dict[str, Any] | None, str]:
    candidates = _load_workflow_candidates(
        repository,
        "release.yml",
        [
            ("branch", release.tag),
            ("event", "push"),
            ("status", "success"),
            ("head_sha", release.commit_sha),
        ],
        api_call=api_call,
    )
    matches: list[dict[str, Any]] = []
    for candidate in candidates:
        try:
            completed_at = _timestamp(candidate.get("updated_at"), "workflow updated_at")
        except ExportError:
            continue
        if (
            _workflow_name(candidate) == RELEASE_WORKFLOW_NAME
            and _workflow_path(candidate) == RELEASE_WORKFLOW_PATH
            and candidate.get("event") == "push"
            and candidate.get("head_branch") == release.tag
            and candidate.get("head_sha") == release.commit_sha
            and candidate.get("conclusion") == "success"
            and completed_at <= release.published_at
        ):
            matches.append(candidate)
    if len(matches) == 1:
        return matches[0], "linked"
    return None, "ambiguous" if len(matches) > 1 else "missing"


def load_published_release(
    repository: str,
    *,
    token: str,
    release_id: str | int | None = None,
    release_tag: str | None = None,
    api_call: Callable[[str], Any] | None = None,
) -> PublishedRelease:
    """Refetch and validate the immutable release and its dereferenced tag."""
    repository = validate_repository(repository)
    if release_id is None and release_tag is None:
        raise ExportError("a GitHub release ID or tag is required")
    expected_id = (
        positive_integer("GitHub release ID", release_id)
        if release_id is not None
        else None
    )
    if release_tag is not None:
        release_lifecycle_id(release_tag)
    call = api_call or (lambda path: github_api(path, token=token))
    if expected_id is not None:
        release = call(f"/repos/{repository}/releases/{expected_id}")
    else:
        encoded_tag = urllib.parse.quote(str(release_tag), safe="")
        release = call(f"/repos/{repository}/releases/tags/{encoded_tag}")
    if not isinstance(release, dict):
        raise ExportError("GitHub release response is not an object")

    actual_id = positive_integer("GitHub release ID", release.get("id", ""))
    tag = str(release.get("tag_name") or "")
    release_lifecycle_id(tag)
    if expected_id is not None and actual_id != expected_id:
        raise ExportError("GitHub release ID does not match the event")
    if release_tag is not None and tag != release_tag:
        raise ExportError("GitHub release tag does not match the event")
    if not (
        release.get("draft") is False
        and release.get("prerelease") is False
        and release.get("immutable") is True
    ):
        raise ExportError("GitHub release is not public, final, and immutable")
    published_at = _timestamp(release.get("published_at"), "release published_at")

    encoded_tag = urllib.parse.quote(tag, safe="")
    commit = call(f"/repos/{repository}/commits/{encoded_tag}")
    if not isinstance(commit, dict):
        raise ExportError("GitHub tag commit response is not an object")
    commit_sha = _validated_sha(commit.get("sha"), "release tag commit SHA")
    if release.get("target_commitish") != commit_sha:
        raise ExportError("GitHub release target does not match its tag commit")
    return PublishedRelease(
        release_id=actual_id,
        tag=tag,
        commit_sha=commit_sha,
        published_at=published_at,
        html_url=str(release.get("html_url") or ""),
    )


def _resolve_pull_request_workflow(
    repository: str,
    run: dict[str, Any],
    base: RunCorrelation,
    *,
    api_call: Callable[[str], Any],
) -> RunCorrelation:
    pulls = run.get("pull_requests")
    if (
        isinstance(pulls, list)
        and len(pulls) == 1
        and base.lifecycle_id.startswith("cvk-pr")
    ):
        return base
    pull, state = _select_pull_request(
        repository,
        run,
        require_merge_commit=False,
        api_call=api_call,
    )
    if pull is not None:
        return _with_correlation(
            base,
            lifecycle_id=pull.lifecycle_id,
            state="pull_request",
        )
    return _with_correlation(
        base,
        state=f"pull_request_{state}",
        warnings=(f"pull-request identity is {state}; no lifecycle was inferred",),
        required_transition_failed=True,
    )


def _resolve_main_smoke(
    repository: str,
    run: dict[str, Any],
    base: RunCorrelation,
    *,
    api_call: Callable[[str], Any],
) -> RunCorrelation:
    pull, pull_state = _select_pull_request(
        repository,
        run,
        require_merge_commit=True,
        api_call=api_call,
    )
    if pull is None:
        warning: tuple[str, ...] = ()
        if pull_state != "direct_push":
            warning = (
                f"merged-PR lookup is {pull_state}; main smoke remains a standalone trace",
            )
        return _with_correlation(
            base,
            state=f"main_{pull_state}",
            warnings=warning,
            required_transition_failed=pull_state != "direct_push",
        )

    smoke, smoke_state = _find_pull_request_smoke_run(
        repository,
        pull,
        api_call=api_call,
    )
    if smoke is None:
        return _with_correlation(
            base,
            lifecycle_id=pull.lifecycle_id,
            state=f"main_pr_smoke_{smoke_state}",
            warnings=(
                f"PR smoke lookup is {smoke_state}; main smoke has no upstream link",
            ),
            required_transition_failed=True,
        )
    return _with_correlation(
        base,
        lifecycle_id=pull.lifecycle_id,
        references=(
            _workflow_reference(
                smoke,
                "github.pull_request.merge",
                upstream_lifecycle_id=pull.lifecycle_id,
            ),
        ),
        state="main_pr_linked",
    )


def _resolve_release_tag(
    repository: str,
    run: dict[str, Any],
    base: RunCorrelation,
    *,
    api_call: Callable[[str], Any],
) -> RunCorrelation:
    tag = str(run.get("head_branch") or "")
    lifecycle = release_lifecycle_id(tag)
    commit_sha = _validated_sha(run.get("head_sha"), "release workflow head SHA")
    release_started = _timestamp(run.get("created_at"), "release workflow created_at")
    smoke, state = _find_successful_main_smoke_run(
        repository,
        commit_sha,
        release_started,
        api_call=api_call,
    )
    if smoke is None:
        return _with_correlation(
            base,
            lifecycle_id=lifecycle,
            state=f"release_main_smoke_{state}",
            warnings=(
                f"successful main smoke lookup is {state}; release has no upstream link",
            ),
            required_transition_failed=True,
        )
    return _with_correlation(
        base,
        lifecycle_id=lifecycle,
        references=(
            _workflow_reference(smoke, "github.tag.after-main-ci"),
        ),
        state="release_main_smoke_linked",
    )


def _resolve_publication_downstream(
    repository: str,
    run: dict[str, Any],
    base: RunCorrelation,
    *,
    token: str,
    api_call: Callable[[str], Any],
) -> RunCorrelation:
    tag = str(run.get("head_branch") or "")
    release = load_published_release(
        repository,
        token=token,
        release_tag=tag,
        api_call=api_call,
    )
    run_sha = _validated_sha(run.get("head_sha"), "release workflow head SHA")
    run_created = _timestamp(run.get("created_at"), "release workflow created_at")
    if release.commit_sha != run_sha or release.published_at > run_created:
        raise ExportError("release workflow does not match the published release")
    return _with_correlation(
        base,
        lifecycle_id=release.lifecycle_id,
        references=(
            SpanReference(
                traceparent=github_release_published_traceparent(release.release_id),
                link_type="github.release.published",
                upstream_lifecycle_id=release.lifecycle_id,
            ),
        ),
        state="release_publication_linked",
    )


def resolve_run_correlation(
    repository: str,
    run: dict[str, Any],
    *,
    token: str,
    api_call: Callable[[str], Any] | None = None,
) -> RunCorrelation:
    """Resolve trusted cross-workflow context without executing observed code."""
    repository = validate_repository(repository)
    base = base_correlation_for_run(run)
    call = api_call or (lambda path: github_api(path, token=token))
    name = _workflow_name(run)
    event = str(run.get("event") or "")
    path = _workflow_path(run)
    try:
        if name == SMOKE_WORKFLOW_NAME and path == SMOKE_WORKFLOW_PATH:
            if event == "pull_request":
                return _resolve_pull_request_workflow(
                    repository, run, base, api_call=call
                )
            if event == "push" and run.get("head_branch") == "main":
                return _resolve_main_smoke(repository, run, base, api_call=call)
        if (
            name == RELEASE_WORKFLOW_NAME
            and path == RELEASE_WORKFLOW_PATH
            and event == "push"
        ):
            return _resolve_release_tag(repository, run, base, api_call=call)
        if (
            name in PUBLICATION_DOWNSTREAM_WORKFLOWS
            and path == PUBLICATION_DOWNSTREAM_WORKFLOWS[name]
            and event == "release"
        ):
            return _resolve_publication_downstream(
                repository,
                run,
                base,
                token=token,
                api_call=call,
            )
    except (CorrelationError, ExportError, TypeError, ValueError) as error:
        # Never discard the completed workflow's own trace merely because
        # history is unavailable or ambiguous. Mark the required transition as
        # failed so main exports the standalone trace and then fails visibly.
        return _with_correlation(
            base,
            state="lookup_error",
            warnings=(f"trusted correlation lookup failed: {error}",),
            required_transition_failed=True,
        )
    return base


def resolve_release_publication(
    repository: str,
    release_id: str | int,
    release_tag: str,
    *,
    token: str,
    api_call: Callable[[str], Any] | None = None,
) -> tuple[PublishedRelease, RunCorrelation]:
    """Validate a publication event and optionally link its release build."""
    repository = validate_repository(repository)
    call = api_call or (lambda path: github_api(path, token=token))
    release = load_published_release(
        repository,
        token=token,
        release_id=release_id,
        release_tag=release_tag,
        api_call=call,
    )
    base = RunCorrelation(lifecycle_id=release.lifecycle_id)
    try:
        upstream, state = _find_successful_release_run(
            repository,
            release,
            api_call=call,
        )
    except (CorrelationError, ExportError, TypeError, ValueError) as error:
        return release, _with_correlation(
            base,
            state="lookup_error",
            warnings=(f"release-workflow lookup failed: {error}",),
            required_transition_failed=True,
        )
    if upstream is None:
        return release, _with_correlation(
            base,
            state=f"release_workflow_{state}",
            warnings=(
                f"successful release-workflow lookup is {state}; publication has no upstream link",
            ),
            required_transition_failed=True,
        )
    return release, _with_correlation(
        base,
        references=(
            _workflow_reference(upstream, "github.release.publish"),
        ),
        state="release_workflow_linked",
    )


def _span(
    *,
    trace_id: str,
    span_id: str,
    parent_span_id: str | None,
    name: str,
    start: int,
    end: int,
    conclusion: object,
    attributes: dict[str, object],
    links: list[dict[str, Any]] | None = None,
    kind: int = 1,
) -> dict[str, Any]:
    span: dict[str, Any] = {
        "traceId": trace_id,
        "spanId": span_id,
        "name": name,
        "kind": kind,
        "startTimeUnixNano": str(start),
        "endTimeUnixNano": str(end),
        "attributes": _attributes(attributes),
        "status": _status(conclusion),
        "flags": 1,
    }
    if parent_span_id is not None:
        span["parentSpanId"] = parent_span_id
    if links:
        span["links"] = links
    return span


def _render_links(
    references: tuple[SpanReference, ...],
    lifecycle: str,
) -> list[dict[str, Any]]:
    links: list[dict[str, Any]] = []
    for reference in references:
        upstream_trace_id, upstream_span_id = split_traceparent(
            reference.traceparent
        )
        attributes: dict[str, object] = {
            "cvk.link.type": reference.link_type,
            "cvk.lifecycle.id": lifecycle,
        }
        if reference.upstream_lifecycle_id:
            attributes["cvk.upstream.lifecycle.id"] = (
                reference.upstream_lifecycle_id
            )
        links.append(
            {
                "traceId": upstream_trace_id,
                "spanId": upstream_span_id,
                "flags": 1,
                "attributes": _attributes(attributes),
            }
        )
    return links


def _otlp_payload(spans: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": _attributes(
                        {
                            "service.name": "cisco-virtual-kubelet-cicd",
                            "service.namespace": "cisco-open",
                            "telemetry.sdk.name": "cvk-github-observer",
                            "cvk.telemetry.source": (
                                "github-workflow-run-observer"
                            ),
                        }
                    )
                },
                "scopeSpans": [
                    {
                        "scope": {
                            "name": "cisco-open.cvk.github-observer",
                            "version": "1",
                        },
                        "spans": spans,
                    }
                ],
            }
        ]
    }


def build_otlp_payload(
    repository: str,
    run: dict[str, Any],
    jobs: list[dict[str, Any]],
    correlation: RunCorrelation | None = None,
) -> tuple[dict[str, Any], str]:
    repository = validate_repository(repository)
    run_id = positive_integer("observed GitHub run ID", run.get("id", ""))
    attempt = positive_integer("GitHub run attempt", run.get("run_attempt", ""))
    correlation = correlation or base_correlation_for_run(run)
    lifecycle = correlation.lifecycle_id
    trace_id = github_trace_id(run_id, attempt)
    root_span_id = github_root_span_id(run_id, attempt)
    root_times = _time_range(
        run,
        ("run_started_at", "created_at"),
        ("updated_at",),
    )
    common = {
        "cvk.lifecycle.id": lifecycle,
        "cicd.pipeline.name": _workflow_name(run),
        "cicd.pipeline.run.id": str(run_id),
        "cicd.pipeline.run.url.full": str(run.get("html_url") or ""),
        "cicd.pipeline.run.state": str(run.get("conclusion") or "unknown"),
        "github.workflow.run.attempt": attempt,
        "github.workflow.event.name": str(run.get("event") or ""),
        "cvk.correlation.state": correlation.state,
        "cvk.correlation.required_transition_failed": (
            correlation.required_transition_failed
        ),
        "vcs.repository.url.full": f"https://github.com/{repository}",
        "vcs.ref.head.name": str(run.get("head_branch") or ""),
        "vcs.ref.head.revision": str(run.get("head_sha") or ""),
    }
    links = _render_links(correlation.references, lifecycle)
    spans = [
        _span(
            trace_id=trace_id,
            span_id=root_span_id,
            parent_span_id=None,
            name=_workflow_name(run) or "GitHub workflow",
            start=root_times[0],
            end=root_times[1],
            conclusion=run.get("conclusion"),
            attributes={
                **common,
                "github.workflow.run.display_title": str(
                    run.get("display_title") or ""
                ),
            },
            links=links,
            kind=2,
        )
    ]

    step_count = 0
    seen_job_ids: set[int] = set()
    for job in jobs:
        check_run_id = positive_integer("GitHub check run ID", job.get("id", ""))
        if check_run_id in seen_job_ids:
            raise ExportError(f"duplicate GitHub check run ID: {check_run_id}")
        seen_job_ids.add(check_run_id)
        job_times = _time_range(
            job,
            ("created_at", "started_at"),
            ("completed_at",),
            fallback=root_times,
        )
        job_span_id = github_job_span_id(check_run_id)
        job_name = str(job.get("name") or "")
        spans.append(
            _span(
                trace_id=trace_id,
                span_id=job_span_id,
                parent_span_id=root_span_id,
                name=job_name,
                start=job_times[0],
                end=job_times[1],
                conclusion=job.get("conclusion"),
                attributes={
                    **common,
                    "github.workflow.job.id": check_run_id,
                    "github.workflow.job.name": job_name,
                    "github.workflow.job.runner.name": str(
                        job.get("runner_name") or ""
                    ),
                    "github.workflow.job.url.full": str(job.get("html_url") or ""),
                },
            )
        )

        created = job.get("created_at") or job.get("started_at")
        started = job.get("started_at") or created
        queue_times = _time_range(
            {"start": created, "end": started},
            ("start",),
            ("end",),
            fallback=job_times,
        )
        spans.append(
            _span(
                trace_id=trace_id,
                span_id=github_queue_span_id(check_run_id),
                parent_span_id=job_span_id,
                name=f"{job_name}: queued",
                start=queue_times[0],
                end=queue_times[1],
                conclusion="success",
                attributes={
                    **common,
                    "github.workflow.job.id": check_run_id,
                    "github.workflow.job.name": job_name,
                    "cicd.pipeline.run.queue_time": True,
                },
            )
        )

        raw_steps = job.get("steps") or []
        if not isinstance(raw_steps, list):
            raise ExportError(f"job {job_name!r} has invalid step metadata")
        names = [str(step.get("name") or "") for step in raw_steps]
        if len(names) != len(set(names)):
            raise ExportError(
                f"job {job_name!r} contains duplicate step names; IDs would collide"
            )
        step_count += len(raw_steps)
        if step_count > MAX_STEPS:
            raise ExportError("GitHub workflow exceeds the step safety limit")
        for step in raw_steps:
            step_name = str(step.get("name") or "")
            number = positive_integer("GitHub step number", step.get("number", ""))
            step_times = _time_range(
                step,
                ("started_at",),
                ("completed_at",),
                fallback=job_times,
            )
            spans.append(
                _span(
                    trace_id=trace_id,
                    span_id=github_step_span_id(check_run_id, step_name),
                    parent_span_id=job_span_id,
                    name=step_name,
                    start=step_times[0],
                    end=step_times[1],
                    conclusion=step.get("conclusion"),
                    attributes={
                        **common,
                        "github.workflow.job.id": check_run_id,
                        "github.workflow.job.name": job_name,
                        "github.workflow.step.name": step_name,
                        "github.workflow.step.number": number,
                    },
                )
            )

    payload = _otlp_payload(spans)
    payload["resourceSpans"][0]["resource"]["attributes"].append(
        _attribute("vcs.repository.name", repository)
    )
    return payload, lifecycle


def build_release_published_payload(
    repository: str,
    release: PublishedRelease,
    correlation: RunCorrelation,
) -> tuple[dict[str, Any], str]:
    """Render the trusted GitHub ``release.published`` event as one span."""
    repository = validate_repository(repository)
    if correlation.lifecycle_id != release.lifecycle_id:
        raise ExportError("release publication lifecycle does not match release")
    span = _span(
        trace_id=github_release_trace_id(release.release_id),
        span_id=github_release_published_span_id(release.release_id),
        parent_span_id=None,
        name="github.release.published",
        start=release.published_at,
        end=release.published_at + 1,
        conclusion="success",
        attributes={
            "cvk.lifecycle.id": correlation.lifecycle_id,
            "cvk.correlation.state": correlation.state,
            "cvk.correlation.required_transition_failed": (
                correlation.required_transition_failed
            ),
            "github.release.id": release.release_id,
            "github.release.tag.name": release.tag,
            "github.release.url.full": release.html_url,
            "github.workflow.event.name": "release.published",
            "vcs.repository.url.full": f"https://github.com/{repository}",
            "vcs.ref.head.name": release.tag,
            "vcs.ref.head.revision": release.commit_sha,
        },
        links=_render_links(correlation.references, correlation.lifecycle_id),
        kind=2,
    )
    payload = _otlp_payload([span])
    payload["resourceSpans"][0]["resource"]["attributes"].append(
        _attribute("vcs.repository.name", repository)
    )
    return payload, correlation.lifecycle_id


def export_otlp(
    payload: dict[str, Any],
    *,
    endpoint: str = OTLP_ENDPOINT,
    opener: UrlOpen = urllib.request.urlopen,
) -> None:
    if endpoint != OTLP_ENDPOINT:
        raise ExportError("OTLP endpoint must be the loopback Collector")
    parsed = urllib.parse.urlparse(endpoint)
    if parsed.scheme != "http" or parsed.hostname != "127.0.0.1":
        raise ExportError("OTLP export is restricted to loopback HTTP")
    encoded = json.dumps(payload, separators=(",", ":"), sort_keys=True).encode()
    if len(encoded) > MAX_OTLP_BYTES:
        raise ExportError("OTLP payload exceeds the safety limit")
    request = urllib.request.Request(
        endpoint,
        data=encoded,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "User-Agent": "cisco-virtual-kubelet-cicd-otel-observer",
        },
    )
    try:
        with opener(request, timeout=OTLP_TIMEOUT_SECONDS) as response:
            status = int(response.getcode())
            response_body = _read_bounded(response)
    except (OSError, urllib.error.URLError) as error:
        raise ExportError(f"local OTLP export failed: {error}") from error
    if status < 200 or status >= 300:
        raise ExportError(f"local OTLP export returned HTTP {status}")
    if not response_body.strip():
        return
    try:
        acknowledgement = json.loads(response_body)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ExportError("local OTLP export response was not valid JSON") from error
    if not isinstance(acknowledgement, dict):
        raise ExportError("local OTLP export response was not an object")
    partial = acknowledgement.get("partialSuccess")
    if partial is None:
        return
    if not isinstance(partial, dict):
        raise ExportError("local OTLP partial-success response was invalid")
    rejected = partial.get("rejectedSpans", 0)
    if isinstance(rejected, bool) or not re.fullmatch(r"[0-9]+", str(rejected)):
        raise ExportError("local OTLP rejected-spans count was invalid")
    if int(rejected) > 0:
        raise ExportError(f"local OTLP Collector rejected {rejected} spans")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--run-id")
    source.add_argument("--release-id")
    parser.add_argument("--run-attempt")
    parser.add_argument("--release-tag")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    token = os.environ.get("GH_TOKEN", "")
    try:
        if args.run_id is not None:
            if args.release_tag is not None:
                raise ExportError("--release-tag is only valid with --release-id")
            if args.run_attempt is None:
                raise ExportError("--run-attempt is required with --run-id")
            run = load_completed_run(
                args.repository,
                args.run_id,
                args.run_attempt,
                token=token,
            )
            jobs = load_jobs(
                args.repository,
                run["id"],
                run["run_attempt"],
                token=token,
            )
            correlation = resolve_run_correlation(
                args.repository,
                run,
                token=token,
            )
            payload, lifecycle = build_otlp_payload(
                args.repository,
                run,
                jobs,
                correlation,
            )
            source = f"GitHub run {run['id']}"
        else:
            if args.run_attempt is not None:
                raise ExportError("--run-attempt is only valid with --run-id")
            if args.release_tag is None:
                raise ExportError("--release-tag is required with --release-id")
            release, correlation = resolve_release_publication(
                args.repository,
                args.release_id,
                args.release_tag,
                token=token,
            )
            payload, lifecycle = build_release_published_payload(
                args.repository,
                release,
                correlation,
            )
            source = f"GitHub release {release.release_id}"
        export_otlp(payload)
    except (CorrelationError, ExportError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    for warning in correlation.warnings:
        print(f"warning: {warning}", file=sys.stderr)
    span_count = len(payload["resourceSpans"][0]["scopeSpans"][0]["spans"])
    print(f"exported {span_count} spans for {lifecycle} from {source}")
    if correlation.required_transition_failed:
        print(
            "error: required lifecycle transition could not be correlated; "
            "the standalone base trace was exported",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
