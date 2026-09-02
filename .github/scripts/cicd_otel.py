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
"""Deterministic, non-secret correlation values for CVK CI/CD telemetry."""

from __future__ import annotations

import hashlib
import re


PR_LIFECYCLE_RE = re.compile(r"^cvk-pr([1-9][0-9]*)-h([0-9a-f]{40})$")
RELEASE_TAG_RE = re.compile(
    r"^v[1-9][0-9]{3}\.([1-9]|1[0-2])\.(0|[1-9][0-9]{0,8})$"
)
RELEASE_LIFECYCLE_RE = re.compile(
    r"^cvk-release-v[1-9][0-9]{3}\.([1-9]|1[0-2])\."
    r"(0|[1-9][0-9]{0,8})$"
)
RUN_LIFECYCLE_RE = re.compile(r"^cvk-run([1-9][0-9]*)-a([1-9][0-9]*)$")
TRACEPARENT_RE = re.compile(
    r"^00-([0-9a-f]{32})-([0-9a-f]{16})-01$"
)
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
POSITIVE_INTEGER_RE = re.compile(r"^[1-9][0-9]*$")


class CorrelationError(ValueError):
    """A correlation value is malformed or cannot be reproduced safely."""


def positive_integer(name: str, value: str | int) -> int:
    rendered = str(value)
    if not POSITIVE_INTEGER_RE.fullmatch(rendered):
        raise CorrelationError(f"invalid {name}: {rendered!r}")
    return int(rendered)


def validate_lifecycle_id(value: str) -> str:
    if not any(
        pattern.fullmatch(value)
        for pattern in (PR_LIFECYCLE_RE, RELEASE_LIFECYCLE_RE, RUN_LIFECYCLE_RE)
    ):
        raise CorrelationError(f"invalid CVK lifecycle ID: {value!r}")
    return value


def pr_lifecycle_id(pr_number: str | int, head_sha: str) -> str:
    number = positive_integer("PR number", pr_number)
    if not SHA_RE.fullmatch(head_sha):
        raise CorrelationError(f"invalid PR head SHA: {head_sha!r}")
    return validate_lifecycle_id(f"cvk-pr{number}-h{head_sha}")


def release_lifecycle_id(tag: str) -> str:
    if not RELEASE_TAG_RE.fullmatch(tag):
        raise CorrelationError(f"invalid release tag: {tag!r}")
    return validate_lifecycle_id(f"cvk-release-{tag}")


def run_lifecycle_id(run_id: str | int, run_attempt: str | int) -> str:
    run = positive_integer("GitHub run ID", run_id)
    attempt = positive_integer("GitHub run attempt", run_attempt)
    return validate_lifecycle_id(f"cvk-run{run}-a{attempt}")


def _digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def github_trace_id(run_id: str | int, run_attempt: str | int) -> str:
    """Match githubreceiver's deterministic workflow trace ID."""
    run = positive_integer("GitHub run ID", run_id)
    attempt = positive_integer("GitHub run attempt", run_attempt)
    return _digest(f"{run}{attempt}t")[:32]


def github_root_span_id(run_id: str | int, run_attempt: str | int) -> str:
    """Match githubreceiver's deterministic workflow root span ID."""
    run = positive_integer("GitHub run ID", run_id)
    attempt = positive_integer("GitHub run attempt", run_attempt)
    return _digest(f"{run}{attempt}s")[16:32]


def github_job_span_id(check_run_id: str | int) -> str:
    check_run = positive_integer("GitHub check run ID", check_run_id)
    return _digest(f"{check_run}-j")[16:32]


def github_queue_span_id(check_run_id: str | int) -> str:
    check_run = positive_integer("GitHub check run ID", check_run_id)
    return _digest(f"{check_run}-q")[16:32]


def github_step_span_id(check_run_id: str | int, step_name: str) -> str:
    check_run = positive_integer("GitHub check run ID", check_run_id)
    if not step_name or len(step_name) > 255 or any(ord(char) < 32 for char in step_name):
        raise CorrelationError("GitHub step name must be 1-255 printable characters")
    return _digest(f"{check_run}-{step_name}-s")[16:32]


def github_step_traceparent(
    run_id: str | int,
    run_attempt: str | int,
    check_run_id: str | int,
    step_name: str,
) -> str:
    value = (
        f"00-{github_trace_id(run_id, run_attempt)}-"
        f"{github_step_span_id(check_run_id, step_name)}-01"
    )
    return validate_traceparent(value)


def github_root_traceparent(
    run_id: str | int,
    run_attempt: str | int,
) -> str:
    """Return the deterministic context for a GitHub workflow root span."""
    value = (
        f"00-{github_trace_id(run_id, run_attempt)}-"
        f"{github_root_span_id(run_id, run_attempt)}-01"
    )
    return validate_traceparent(value)


def github_release_trace_id(release_id: str | int) -> str:
    """Return the deterministic trace ID for a GitHub publication event."""
    release = positive_integer("GitHub release ID", release_id)
    return _digest(f"github-release-{release}-t")[:32]


def github_release_published_span_id(release_id: str | int) -> str:
    """Return the deterministic span ID for a GitHub publication event."""
    release = positive_integer("GitHub release ID", release_id)
    return _digest(f"github-release-{release}-published-s")[16:32]


def github_release_published_traceparent(release_id: str | int) -> str:
    """Return the deterministic context downstream release jobs can link to."""
    value = (
        f"00-{github_release_trace_id(release_id)}-"
        f"{github_release_published_span_id(release_id)}-01"
    )
    return validate_traceparent(value)


def validate_traceparent(value: str) -> str:
    match = TRACEPARENT_RE.fullmatch(value)
    if match is None:
        raise CorrelationError(f"invalid CVK upstream traceparent: {value!r}")
    trace_id, span_id = match.groups()
    if trace_id == "0" * 32 or span_id == "0" * 16:
        raise CorrelationError("traceparent trace and span IDs must be non-zero")
    return value


def split_traceparent(value: str) -> tuple[str, str]:
    validated = validate_traceparent(value)
    _, trace_id, span_id, _ = validated.split("-", 3)
    return trace_id, span_id
