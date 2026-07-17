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
"""Gate and dispatch native Lab CI for approved pull requests.

The reconciler runs only from trusted ``main``.  It never checks out pull
request code.  Before dispatching a credentialed lab wrapper it verifies the
current PR head and base, an approval for that head, and every hosted merge
gate.  The same verification is also called by each wrapper's
``prepare`` job and again on the lab runner immediately before submission.
Those checks close queue-time races and protect manual workflow dispatches.

Required environment variables:
  GH_TOKEN  Built-in token for the current repository.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from typing import Any, Callable


GITHUB_API = "https://api.github.com"
GITHUB_ACTIONS_APP_ID = 15368
GITHUB_ACTIONS_BOT_ID = 41898282
# Hosted checks that must pass before scarce native lab capacity is consumed.
# Never include TARGETS' native contexts here: this dispatcher is responsible
# for creating those statuses, so waiting for them would deadlock the flow.
REQUIRED_HOSTED_CHECKS = (
    "build-and-smoke",
    "ygot-validate",
    "terraform-provider",
    "govulncheck",
    "helm4-compat",
)
OPINIONATED_REVIEW_STATES = {"APPROVED", "CHANGES_REQUESTED", "DISMISSED"}
ACTIVE_WORKFLOW_STATES = {"in_progress", "pending", "queued", "requested", "waiting"}
MAX_API_PAGES = 100
MAX_AUTOMATIC_DISPATCH_ATTEMPTS = 3
RETRY_CONFIRMATION_SECONDS = 10 * 60
STALE_PENDING_SECONDS = 12 * 60 * 60
STATUS_DESCRIPTION_LIMIT = 140

TARGETS: tuple[dict[str, str], ...] = (
    {
        "key": "cat8kv",
        "context": "lab-ci-next / cat8kv",
        "description": "Approved PR queued for Cat8kv",
        "run_title": "Lab CI Cat8kv",
        "workflow": "lab-ci-cat8kv.yaml",
    },
    {
        "key": "cat9k",
        "context": "lab-ci-next / cat9k",
        "description": "Approved PR queued for Cat9k",
        "run_title": "Lab CI Cat9k",
        "workflow": "lab-ci-cat9k.yaml",
    },
)

REPOSITORY_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
PR_RE = re.compile(r"^[1-9][0-9]*$")


class GateError(RuntimeError):
    """The pull request is not currently eligible for native Lab CI."""


@dataclass(frozen=True)
class VerifiedPullRequest:
    pr_number: int
    head_sha: str
    base_sha: str
    merge_sha: str


ApiCall = Callable[..., Any]


def validate_repository(repository: str) -> str:
    if not REPOSITORY_RE.fullmatch(repository):
        raise GateError(f"invalid repository: {repository!r}")
    return repository


def validate_sha(name: str, value: str) -> str:
    if not SHA_RE.fullmatch(value):
        raise GateError(f"invalid {name}: {value!r}")
    return value


def validate_pr_number(value: str | int) -> int:
    rendered = str(value)
    if not PR_RE.fullmatch(rendered):
        raise GateError(f"invalid PR number: {rendered!r}")
    return int(rendered)


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
    if not auth_token:
        raise GateError("GH_TOKEN is required")
    headers = {
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {auth_token}",
        "Content-Type": "application/json",
        "User-Agent": "cisco-virtual-kubelet-approved-lab-ci",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    data = json.dumps(body).encode("utf-8") if body is not None else None
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
        detail = error.read().decode("utf-8", errors="replace")[:500]
        raise GateError(
            f"GitHub API {method} {path} failed with HTTP {error.code}: {detail}"
        ) from error
    except urllib.error.URLError as error:
        raise GateError(f"GitHub API {method} {path} failed: {error.reason}") from error
    return json.loads(payload) if payload else None


def _get_pull_request(
    repository: str,
    pr_number: int,
    api_call: ApiCall,
    sleep: Callable[[float], None],
) -> dict[str, Any]:
    """Fetch a PR, retrying briefly while GitHub computes mergeability."""
    pull: dict[str, Any] = {}
    for attempt in range(1, 6):
        pull = api_call(f"/repos/{repository}/pulls/{pr_number}")
        if pull.get("mergeable") is not None:
            return pull
        sleep(float(attempt * 2))
    raise GateError(f"PR #{pr_number}: GitHub did not compute mergeability")


def _paginated_items(
    path: str,
    *,
    api_call: ApiCall,
    response_key: str | None = None,
) -> list[dict[str, Any]]:
    """Read every page of a list endpoint, rejecting unsafe truncation."""
    items: list[dict[str, Any]] = []
    separator = "&" if "?" in path else "?"
    for page in range(1, MAX_API_PAGES + 1):
        response = api_call(f"{path}{separator}per_page=100&page={page}")
        batch = response if response_key is None else (response or {}).get(response_key)
        if not isinstance(batch, list):
            raise GateError(f"GitHub API {path} returned an invalid paginated response")
        items.extend(batch)
        if len(batch) < 100:
            return items
    raise GateError(f"GitHub API {path} exceeded the pagination safety limit")


def _latest_reviews_by_user(reviews: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    latest: dict[str, dict[str, Any]] = {}
    for review in reviews:
        # COMMENTED and PENDING reviews do not change a reviewer's approval or
        # change-request state. GitHub's aggregate reviewDecision follows the
        # same opinionated-review semantics.
        if review.get("state") not in OPINIONATED_REVIEW_STATES:
            continue
        login = (review.get("user") or {}).get("login")
        if not login:
            continue
        previous = latest.get(login)
        if previous is None or int(review.get("id") or 0) > int(
            previous.get("id") or 0
        ):
            latest[login] = review
    return latest


def _verify_review(
    repository: str,
    reviews: list[dict[str, Any]],
    head_sha: str,
    pr_number: int,
    api_call: ApiCall,
) -> None:
    owner, name = repository.split("/", 1)
    decision_data = api_call(
        "/graphql",
        method="POST",
        body={
            "query": """
                query($owner: String!, $name: String!, $number: Int!) {
                  repository(owner: $owner, name: $name) {
                    pullRequest(number: $number) { reviewDecision }
                  }
                }
            """,
            "variables": {"owner": owner, "name": name, "number": pr_number},
        },
    )
    pull_request_data = (
        ((decision_data or {}).get("data") or {}).get("repository") or {}
    ).get("pullRequest") or {}
    decision = pull_request_data.get("reviewDecision")
    if decision != "APPROVED":
        raise GateError(f"PR #{pr_number}: aggregate review decision is {decision!r}")

    latest = _latest_reviews_by_user(reviews)
    approval_candidates = [
        review
        for review in latest.values()
        if review.get("state") == "APPROVED" and review.get("commit_id") == head_sha
    ]
    for review in approval_candidates:
        login = (review.get("user") or {}).get("login", "")
        permission = api_call(
            f"/repos/{repository}/collaborators/"
            f"{urllib.parse.quote(login, safe='')}/permission"
        )
        if permission.get("permission") in {"admin", "write"}:
            return

    if not approval_candidates:
        raise GateError(
            f"PR #{pr_number}: no trusted approval applies to current head {head_sha}"
        )
    raise GateError(
        f"PR #{pr_number}: current-head approver does not have write permission"
    )


def _verify_required_checks(
    check_runs: list[dict[str, Any]], head_sha: str, pr_number: int
) -> None:
    for required_name in REQUIRED_HOSTED_CHECKS:
        candidates = [
            run
            for run in check_runs
            if run.get("name") == required_name
            and (run.get("app") or {}).get("id") == GITHUB_ACTIONS_APP_ID
            and run.get("head_sha") == head_sha
        ]
        if not candidates:
            raise GateError(
                f"PR #{pr_number}: required check {required_name!r} is missing"
            )
        latest = max(candidates, key=lambda run: int(run.get("id") or 0))
        if latest.get("status") != "completed" or latest.get("conclusion") != "success":
            raise GateError(
                f"PR #{pr_number}: required check {required_name!r} is not successful"
            )


def verify_pull_request(
    repository: str,
    pr_number: str | int,
    expected_head_sha: str | None = None,
    expected_base_sha: str | None = None,
    expected_merge_sha: str | None = None,
    *,
    api_call: ApiCall = api,
    sleep: Callable[[float], None] = time.sleep,
) -> VerifiedPullRequest:
    """Return pinned PR inputs after every trust and readiness check passes."""
    repository = validate_repository(repository)
    number = validate_pr_number(pr_number)
    if expected_head_sha is not None:
        expected_head_sha = validate_sha("expected head SHA", expected_head_sha)
    if expected_base_sha is not None:
        expected_base_sha = validate_sha("expected base SHA", expected_base_sha)
    if expected_merge_sha is not None:
        expected_merge_sha = validate_sha("expected merge SHA", expected_merge_sha)

    pull = _get_pull_request(repository, number, api_call, sleep)
    if pull.get("state") != "open":
        raise GateError(f"PR #{number}: PR is not open")
    if pull.get("draft") is not False:
        raise GateError(f"PR #{number}: draft PRs are not eligible")
    if (pull.get("base") or {}).get("ref") != "main":
        raise GateError(f"PR #{number}: base branch is not main")
    if ((pull.get("base") or {}).get("repo") or {}).get("full_name") != repository:
        raise GateError(f"PR #{number}: base repository does not match {repository}")
    if pull.get("mergeable") is not True:
        raise GateError(f"PR #{number}: PR is not mergeable")

    head_sha = validate_sha("PR head SHA", (pull.get("head") or {}).get("sha", ""))
    base_sha = validate_sha("PR base SHA", (pull.get("base") or {}).get("sha", ""))
    merge_sha = validate_sha("prospective merge SHA", pull.get("merge_commit_sha", ""))
    if head_sha == merge_sha:
        raise GateError(f"PR #{number}: prospective merge SHA equals head SHA")
    if expected_head_sha is not None and head_sha != expected_head_sha:
        raise GateError(
            f"PR #{number}: head changed from {expected_head_sha} to {head_sha}"
        )
    if expected_base_sha is not None and base_sha != expected_base_sha:
        raise GateError(
            f"PR #{number}: base changed from {expected_base_sha} to {base_sha}"
        )
    if expected_merge_sha is not None and merge_sha != expected_merge_sha:
        raise GateError(
            f"PR #{number}: prospective merge changed from {expected_merge_sha} to {merge_sha}"
        )

    main = api_call(f"/repos/{repository}/branches/main")
    current_main_sha = validate_sha(
        "current main SHA", (main.get("commit") or {}).get("sha", "")
    )
    if base_sha != current_main_sha:
        raise GateError(
            f"PR #{number}: base {base_sha} is not current main {current_main_sha}"
        )

    comparison = api_call(f"/repos/{repository}/compare/{base_sha}...{head_sha}") or {}
    if comparison.get("behind_by") != 0:
        raise GateError(f"PR #{number}: branch is not up to date with main")

    reviews = _paginated_items(
        f"/repos/{repository}/pulls/{number}/reviews",
        api_call=api_call,
    )
    _verify_review(repository, reviews, head_sha, number, api_call)

    check_runs = _paginated_items(
        f"/repos/{repository}/commits/{head_sha}/check-runs",
        api_call=api_call,
        response_key="check_runs",
    )
    _verify_required_checks(check_runs, head_sha, number)

    # Close the verification race before returning inputs to a dispatcher.
    refreshed = api_call(f"/repos/{repository}/pulls/{number}")
    if (refreshed.get("head") or {}).get("sha") != head_sha:
        raise GateError(f"PR #{number}: head changed during verification")
    if (refreshed.get("base") or {}).get("sha") != base_sha:
        raise GateError(f"PR #{number}: base changed during verification")
    if refreshed.get("merge_commit_sha") != merge_sha:
        raise GateError(f"PR #{number}: prospective merge changed during verification")
    refreshed_main = api_call(f"/repos/{repository}/branches/main")
    if (refreshed_main.get("commit") or {}).get("sha") != base_sha:
        raise GateError(f"PR #{number}: main changed during verification")

    return VerifiedPullRequest(number, head_sha, base_sha, merge_sha)


def list_open_pr_numbers(repository: str, *, api_call: ApiCall = api) -> list[int]:
    pulls = _paginated_items(
        f"/repos/{repository}/pulls?state=open&base=main",
        api_call=api_call,
    )
    return [validate_pr_number(pull.get("number", "")) for pull in pulls]


def native_status_history(
    repository: str, head_sha: str, *, api_call: ApiCall = api
) -> dict[str, list[dict[str, Any]]]:
    wanted = {target["context"] for target in TARGETS}
    history = {context: [] for context in wanted}
    statuses = _paginated_items(
        f"/repos/{repository}/statuses/{head_sha}",
        api_call=api_call,
    )
    for status in statuses:
        context = status.get("context", "")
        if context in wanted:
            history[context].append(status)
    for context in history:
        history[context].sort(
            key=lambda status: int(status.get("id") or 0), reverse=True
        )
    return history


def status_pin(verified: VerifiedPullRequest) -> str:
    return f"base={verified.base_sha}; merge={verified.merge_sha}"


def status_is_for_verified_merge(
    status: dict[str, Any], verified: VerifiedPullRequest
) -> bool:
    return status_pin(verified) in str(status.get("description") or "")


def dispatch_id(
    verified: VerifiedPullRequest, target: dict[str, str], attempt: int
) -> str:
    if attempt < 1 or attempt > MAX_AUTOMATIC_DISPATCH_ATTEMPTS:
        raise GateError(f"invalid automatic dispatch attempt: {attempt}")
    return (
        f"{target['key']}-pr{verified.pr_number}"
        f"-h{verified.head_sha[:12]}-b{verified.base_sha[:12]}"
        f"-m{verified.merge_sha[:12]}-a{attempt}"
    )


def dispatch_run_title(
    verified: VerifiedPullRequest, target: dict[str, str], attempt: int
) -> str:
    return f"{target['run_title']} - {dispatch_id(verified, target, attempt)}"


def _status_attempt(status: dict[str, Any]) -> int | None:
    match = re.search(
        r"(?:^|; )try=([1-9][0-9]*)(?:;|$)", str(status.get("description") or "")
    )
    return int(match.group(1)) if match else None


def _automatic_dispatch_attempts(
    statuses: list[dict[str, Any]],
    target: dict[str, str],
    verified: VerifiedPullRequest,
) -> int:
    return sum(
        1
        for status in statuses
        if status_is_for_verified_merge(status, verified)
        and str(status.get("description") or "").startswith(target["description"])
    )


def _has_active_correlated_run(
    repository: str,
    verified: VerifiedPullRequest,
    target: dict[str, str],
    attempt: int,
    *,
    api_call: ApiCall,
) -> bool:
    expected_title = dispatch_run_title(verified, target, attempt)
    workflow = urllib.parse.quote(target["workflow"], safe="")
    runs = _paginated_items(
        f"/repos/{repository}/actions/workflows/{workflow}/runs"
        "?event=workflow_dispatch&branch=main",
        api_call=api_call,
        response_key="workflow_runs",
    )
    return any(
        run.get("display_title") == expected_title
        and run.get("status") in ACTIVE_WORKFLOW_STATES
        for run in runs
    )


def _is_trusted_terminal_status(
    repository: str,
    status: dict[str, Any],
    verified: VerifiedPullRequest,
    target: dict[str, str],
    *,
    api_call: ApiCall,
) -> bool:
    attempt = _status_attempt(status)
    creator_id = int(((status.get("creator") or {}).get("id") or 0))
    run_id = _workflow_run_id(repository, str(status.get("target_url") or ""))
    if attempt is None or creator_id != GITHUB_ACTIONS_BOT_ID or run_id is None:
        return False
    run = api_call(f"/repos/{repository}/actions/runs/{run_id}") or {}
    run_path = str(run.get("path") or "").split("@", 1)[0]
    return (
        int(run.get("id") or 0) == run_id
        and run.get("event") == "workflow_dispatch"
        and run.get("head_branch") == "main"
        and run.get("head_sha") == verified.base_sha
        and run_path == f".github/workflows/{target['workflow']}"
        and run.get("display_title") == dispatch_run_title(verified, target, attempt)
    )


def _status_age_seconds(status: dict[str, Any], now: datetime.datetime) -> float | None:
    rendered = str(status.get("created_at") or "")
    try:
        created = datetime.datetime.fromisoformat(rendered.replace("Z", "+00:00"))
    except ValueError:
        return None
    if created.tzinfo is None:
        created = created.replace(tzinfo=datetime.timezone.utc)
    return max(0.0, (now - created).total_seconds())


def _workflow_run_id(repository: str, target_url: str) -> int | None:
    match = re.fullmatch(
        rf"https://github\.com/{re.escape(repository)}/actions/runs/([1-9][0-9]*)",
        target_url,
    )
    return int(match.group(1)) if match else None


def status_blocks_automatic_dispatch(
    repository: str,
    statuses: list[dict[str, Any]],
    target: dict[str, str],
    verified: VerifiedPullRequest,
    *,
    api_call: ApiCall = api,
    now: datetime.datetime | None = None,
) -> bool:
    """Return whether the latest exact-pin status should suppress a dispatch."""
    if not statuses:
        return False
    latest = statuses[0]
    if not status_is_for_verified_merge(latest, verified):
        return False

    state = str(latest.get("state") or "")
    description = str(latest.get("description") or "")
    attempts = _automatic_dispatch_attempts(statuses, target, verified)
    if state in {"success", "failure"}:
        return _is_trusted_terminal_status(
            repository,
            latest,
            verified,
            target,
            api_call=api_call,
        )
    if state == "error":
        retryable = description.startswith("Automatic dispatch failed;") or any(
            marker in description for marker in (" gate rejected;", " incomplete;")
        )
        if not retryable or attempts >= MAX_AUTOMATIC_DISPATCH_ATTEMPTS:
            return True
        current_time = now or datetime.datetime.now(datetime.timezone.utc)
        age = _status_age_seconds(latest, current_time)
        if age is None or age < RETRY_CONFIRMATION_SECONDS:
            return True
        attempt = _status_attempt(latest)
        if attempt is None:
            return True
        return _has_active_correlated_run(
            repository,
            verified,
            target,
            attempt,
            api_call=api_call,
        )
    if state != "pending":
        return True

    current_time = now or datetime.datetime.now(datetime.timezone.utc)
    age = _status_age_seconds(latest, current_time)
    workflow_url = (
        f"https://github.com/{repository}/actions/workflows/{target['workflow']}"
    )
    is_dispatch_marker = (
        description.startswith(target["description"])
        and str(latest.get("target_url") or "") == workflow_url
    )
    stale_after = (
        RETRY_CONFIRMATION_SECONDS if is_dispatch_marker else STALE_PENDING_SECONDS
    )
    if age is None or age < stale_after:
        return True

    run_id = _workflow_run_id(repository, str(latest.get("target_url") or ""))
    if run_id is not None:
        run = api_call(f"/repos/{repository}/actions/runs/{run_id}") or {}
        if run.get("status") in ACTIVE_WORKFLOW_STATES:
            return True
        if run.get("status") != "completed":
            return True
    attempt = _status_attempt(latest)
    if attempt is None:
        return True
    if _has_active_correlated_run(
        repository,
        verified,
        target,
        attempt,
        api_call=api_call,
    ):
        return True
    return attempts >= MAX_AUTOMATIC_DISPATCH_ATTEMPTS


def post_status(
    repository: str,
    head_sha: str,
    *,
    context: str,
    state: str,
    description: str,
    target_url: str,
    api_call: ApiCall = api,
) -> None:
    if len(description) > STATUS_DESCRIPTION_LIMIT:
        raise GateError(
            f"status description exceeds {STATUS_DESCRIPTION_LIMIT} characters"
        )
    api_call(
        f"/repos/{repository}/statuses/{head_sha}",
        method="POST",
        body={
            "context": context,
            "description": description,
            "state": state,
            "target_url": target_url,
        },
    )


def dispatch_target(
    repository: str,
    verified: VerifiedPullRequest,
    target: dict[str, str],
    attempt: int,
    *,
    api_call: ApiCall = api,
) -> None:
    correlation = dispatch_id(verified, target, attempt)
    workflow = target["workflow"]
    workflow_url = f"https://github.com/{repository}/actions/workflows/{workflow}"
    post_status(
        repository,
        verified.head_sha,
        context=target["context"],
        state="pending",
        description=(f"{target['description']}; try={attempt}; {status_pin(verified)}"),
        target_url=workflow_url,
        api_call=api_call,
    )
    try:
        api_call(
            f"/repos/{repository}/actions/workflows/{workflow}/dispatches",
            method="POST",
            body={
                "ref": "main",
                "inputs": {
                    "upstream_pr": str(verified.pr_number),
                    "expected_head_sha": verified.head_sha,
                    "expected_base_sha": verified.base_sha,
                    "expected_merge_sha": verified.merge_sha,
                    "dispatch_attempt": str(attempt),
                    "dispatch_id": correlation,
                },
            },
        )
    except Exception:
        post_status(
            repository,
            verified.head_sha,
            context=target["context"],
            state="error",
            description=(
                f"Automatic dispatch failed; try={attempt}; {status_pin(verified)}"
            ),
            target_url=workflow_url,
            api_call=api_call,
        )
        raise


def reconcile(repository: str, *, api_call: ApiCall = api) -> None:
    repository = validate_repository(repository)
    failures: list[str] = []
    for pr_number in list_open_pr_numbers(repository, api_call=api_call):
        try:
            verified = verify_pull_request(
                repository,
                pr_number,
                api_call=api_call,
            )
        except GateError as error:
            print(f"skip PR #{pr_number}: {error}")
            continue

        status_history = native_status_history(
            repository,
            verified.head_sha,
            api_call=api_call,
        )
        for target in TARGETS:
            statuses = status_history.get(target["context"], [])
            try:
                blocked = status_blocks_automatic_dispatch(
                    repository,
                    statuses,
                    target,
                    verified,
                    api_call=api_call,
                )
            except GateError as error:
                failures.append(f"PR #{pr_number} {target['context']}: {error}")
                continue
            if blocked:
                print(
                    f"skip PR #{pr_number} {target['context']}: "
                    "current prospective merge is already handled"
                )
                continue
            print(f"dispatch PR #{pr_number} {target['context']}")
            try:
                attempt = _automatic_dispatch_attempts(statuses, target, verified) + 1
                dispatch_target(
                    repository,
                    verified,
                    target,
                    attempt,
                    api_call=api_call,
                )
            except Exception as error:  # report every target before failing the run
                failures.append(f"PR #{pr_number} {target['context']}: {error}")

    if failures:
        raise GateError("; ".join(failures))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    verify = subparsers.add_parser("verify", help="verify and pin one PR")
    verify.add_argument("--repository", required=True)
    verify.add_argument("--pr-number", required=True)
    verify.add_argument("--expected-head-sha", required=True)
    verify.add_argument("--expected-base-sha", required=True)
    verify.add_argument("--expected-merge-sha", required=True)

    reconcile_parser = subparsers.add_parser(
        "reconcile", help="dispatch missing native tests for eligible PRs"
    )
    reconcile_parser.add_argument("--repository", required=True)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        if args.command == "verify":
            verified = verify_pull_request(
                args.repository,
                args.pr_number,
                args.expected_head_sha,
                args.expected_base_sha,
                args.expected_merge_sha,
            )
            print(json.dumps(asdict(verified), sort_keys=True))
        else:
            reconcile(args.repository)
    except (GateError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
