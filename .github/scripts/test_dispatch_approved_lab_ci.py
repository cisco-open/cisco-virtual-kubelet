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

from __future__ import annotations

import copy
import datetime
import io
import importlib.util
import pathlib
import re
import sys
import unittest
from contextlib import redirect_stdout
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("dispatch-approved-lab-ci.py")
SPEC = importlib.util.spec_from_file_location("dispatch_approved_lab_ci", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
dispatcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = dispatcher
SPEC.loader.exec_module(dispatcher)

REPOSITORY = "cisco-open/cisco-virtual-kubelet"
HEAD = "a" * 40
BASE = "b" * 40
MERGE = "c" * 40


def eligible_pull() -> dict[str, object]:
    return {
        "state": "open",
        "draft": False,
        "mergeable": True,
        "merge_commit_sha": MERGE,
        "head": {"sha": HEAD},
        "base": {
            "ref": "main",
            "sha": BASE,
            "repo": {"full_name": REPOSITORY},
        },
    }


def approved_review(**overrides: object) -> dict[str, object]:
    review: dict[str, object] = {
        "id": 10,
        "state": "APPROVED",
        "commit_id": HEAD,
        "author_association": "MEMBER",
        "user": {"login": "reviewer"},
    }
    review.update(overrides)
    return review


def successful_checks() -> list[dict[str, object]]:
    return [
        {
            "id": index,
            "name": name,
            "head_sha": HEAD,
            "status": "completed",
            "conclusion": "success",
            "app": {"id": dispatcher.GITHUB_ACTIONS_APP_ID},
        }
        for index, name in enumerate(dispatcher.REQUIRED_CHECKS, start=20)
    ]


class VerificationApi:
    def __init__(
        self,
        *,
        pull: dict[str, object] | None = None,
        reviews: list[dict[str, object]] | None = None,
        checks: list[dict[str, object]] | None = None,
        review_decision: str | None = "APPROVED",
        reviewer_permission: str = "write",
        compare_behind_by: int = 0,
    ) -> None:
        self.pull = pull if pull is not None else eligible_pull()
        self.reviews = reviews if reviews is not None else [approved_review()]
        self.checks = checks if checks is not None else successful_checks()
        self.review_decision = review_decision
        self.reviewer_permission = reviewer_permission
        self.compare_behind_by = compare_behind_by
        self.pull_calls = 0

    def __call__(
        self,
        path: str,
        method: str = "GET",
        body: dict[str, object] | None = None,
    ) -> object:
        del method, body
        if path.endswith("/branches/main"):
            return {"commit": {"sha": BASE}}
        if "/compare/" in path:
            return {"behind_by": self.compare_behind_by}
        if path == "/graphql":
            return {
                "data": {
                    "repository": {
                        "pullRequest": {"reviewDecision": self.review_decision}
                    }
                }
            }
        if "/collaborators/" in path and path.endswith("/permission"):
            return {"permission": self.reviewer_permission}
        if "/reviews?" in path:
            return copy.deepcopy(self.reviews)
        if "/check-runs?" in path:
            return {"check_runs": copy.deepcopy(self.checks)}
        if path.endswith("/pulls/7"):
            self.pull_calls += 1
            return copy.deepcopy(self.pull)
        raise AssertionError(f"unexpected API call: {path}")


class DispatcherTests(unittest.TestCase):
    def test_current_head_approval_and_protected_checks_are_accepted(self) -> None:
        result = dispatcher.verify_pull_request(
            REPOSITORY,
            7,
            HEAD,
            BASE,
            MERGE,
            api_call=VerificationApi(),
            sleep=lambda _: None,
        )
        self.assertEqual(
            result,
            dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE),
        )

    def test_stale_or_dismissed_approval_is_rejected(self) -> None:
        cases = (
            approved_review(commit_id="d" * 40),
            approved_review(state="DISMISSED"),
        )
        for review in cases:
            with self.subTest(review=review):
                with self.assertRaisesRegex(
                    dispatcher.GateError, "no trusted approval"
                ):
                    dispatcher.verify_pull_request(
                        REPOSITORY,
                        7,
                        api_call=VerificationApi(reviews=[review]),
                        sleep=lambda _: None,
                    )

    def test_write_reviewer_is_accepted_despite_unreliable_association(self) -> None:
        result = dispatcher.verify_pull_request(
            REPOSITORY,
            7,
            api_call=VerificationApi(
                reviews=[approved_review(author_association="CONTRIBUTOR")]
            ),
            sleep=lambda _: None,
        )
        self.assertEqual(result.head_sha, HEAD)

    def test_non_approved_aggregate_review_decision_is_rejected(self) -> None:
        with self.assertRaisesRegex(dispatcher.GateError, "aggregate review decision"):
            dispatcher.verify_pull_request(
                REPOSITORY,
                7,
                api_call=VerificationApi(review_decision="CHANGES_REQUESTED"),
                sleep=lambda _: None,
            )

    def test_comment_does_not_replace_an_opinionated_review(self) -> None:
        approval_then_comment = [
            approved_review(),
            approved_review(id=11, state="COMMENTED"),
        ]
        result = dispatcher.verify_pull_request(
            REPOSITORY,
            7,
            api_call=VerificationApi(reviews=approval_then_comment),
            sleep=lambda _: None,
        )
        self.assertEqual(result.head_sha, HEAD)

        changes_then_comment = [
            approved_review(state="CHANGES_REQUESTED"),
            approved_review(id=11, state="COMMENTED"),
        ]
        self.assertEqual(
            dispatcher._latest_reviews_by_user(changes_then_comment)["reviewer"][
                "state"
            ],
            "CHANGES_REQUESTED",
        )

    def test_reviews_are_paginated_before_current_head_approval_check(self) -> None:
        delegate = VerificationApi()

        def paginated_api(
            path: str,
            method: str = "GET",
            body: dict[str, object] | None = None,
        ) -> object:
            if "/reviews?" in path:
                if path.endswith("&page=1"):
                    return [
                        approved_review(
                            id=index,
                            state="COMMENTED",
                            user={"login": f"commenter-{index}"},
                        )
                        for index in range(1, 101)
                    ]
                if path.endswith("&page=2"):
                    return [approved_review(id=101)]
            return delegate(path, method, body)

        result = dispatcher.verify_pull_request(
            REPOSITORY,
            7,
            api_call=paginated_api,
            sleep=lambda _: None,
        )
        self.assertEqual(result.head_sha, HEAD)

    def test_read_only_member_approval_is_rejected(self) -> None:
        with self.assertRaisesRegex(dispatcher.GateError, "write permission"):
            dispatcher.verify_pull_request(
                REPOSITORY,
                7,
                api_call=VerificationApi(reviewer_permission="read"),
                sleep=lambda _: None,
            )

    def test_missing_failed_or_spoofed_required_check_is_rejected(self) -> None:
        cases: list[tuple[str, list[dict[str, object]]]] = []
        missing = successful_checks()[1:]
        cases.append(("missing", missing))

        failed = successful_checks()
        failed[0]["conclusion"] = "failure"
        cases.append(("not successful", failed))

        pending = successful_checks()
        pending[0]["status"] = "in_progress"
        pending[0]["conclusion"] = None
        cases.append(("not successful", pending))

        spoofed = successful_checks()
        spoofed[0]["app"] = {"id": 1}
        cases.append(("missing", spoofed))

        for message, checks in cases:
            with self.subTest(message=message):
                with self.assertRaisesRegex(dispatcher.GateError, message):
                    dispatcher.verify_pull_request(
                        REPOSITORY,
                        7,
                        api_call=VerificationApi(checks=checks),
                        sleep=lambda _: None,
                    )

    def test_wrong_pr_shape_or_pinned_sha_is_rejected(self) -> None:
        mutations = (
            ("draft", lambda pull: pull.update(draft=True)),
            ("base branch", lambda pull: pull["base"].update(ref="develop")),
            (
                "base repository",
                lambda pull: pull["base"]["repo"].update(full_name="evil/fork"),
            ),
            ("not mergeable", lambda pull: pull.update(mergeable=False)),
        )
        for message, mutate in mutations:
            pull = eligible_pull()
            mutate(pull)
            with self.subTest(message=message):
                with self.assertRaisesRegex(dispatcher.GateError, message):
                    dispatcher.verify_pull_request(
                        REPOSITORY,
                        7,
                        api_call=VerificationApi(pull=pull),
                        sleep=lambda _: None,
                    )

        with self.assertRaisesRegex(dispatcher.GateError, "head changed"):
            dispatcher.verify_pull_request(
                REPOSITORY,
                7,
                "d" * 40,
                BASE,
                MERGE,
                api_call=VerificationApi(),
                sleep=lambda _: None,
            )

    def test_branch_behind_main_is_rejected(self) -> None:
        with self.assertRaisesRegex(dispatcher.GateError, "not up to date"):
            dispatcher.verify_pull_request(
                REPOSITORY,
                7,
                api_call=VerificationApi(compare_behind_by=1),
                sleep=lambda _: None,
            )

    def test_head_change_during_verification_is_rejected(self) -> None:
        api_call = VerificationApi()

        def changing_api(
            path: str,
            method: str = "GET",
            body: dict[str, object] | None = None,
        ) -> object:
            result = api_call(path, method, body)
            if path.endswith("/pulls/7") and api_call.pull_calls == 2:
                changed = copy.deepcopy(result)
                changed["head"]["sha"] = "d" * 40
                return changed
            return result

        with self.assertRaisesRegex(
            dispatcher.GateError, "changed during verification"
        ):
            dispatcher.verify_pull_request(
                REPOSITORY,
                7,
                api_call=changing_api,
                sleep=lambda _: None,
            )

    def test_dispatch_posts_pending_and_pins_all_shas(self) -> None:
        calls: list[tuple[str, str, dict[str, object] | None]] = []

        def record(
            path: str,
            method: str = "GET",
            body: dict[str, object] | None = None,
        ) -> None:
            calls.append((path, method, body))

        target = dispatcher.TARGETS[0]
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        dispatcher.dispatch_target(REPOSITORY, verified, target, 1, api_call=record)

        self.assertEqual(calls[0][1], "POST")
        self.assertEqual(calls[0][2]["state"], "pending")
        self.assertIn(f"base={BASE}; merge={MERGE}", calls[0][2]["description"])
        self.assertIn("try=1", calls[0][2]["description"])
        self.assertEqual(
            calls[1][2]["inputs"],
            {
                "upstream_pr": "7",
                "expected_head_sha": HEAD,
                "expected_base_sha": BASE,
                "expected_merge_sha": MERGE,
                "dispatch_attempt": "1",
                "dispatch_id": dispatcher.dispatch_id(verified, target, 1),
            },
        )

    def test_dispatch_failure_replaces_pending_with_error(self) -> None:
        states: list[str] = []

        def fail_dispatch(
            path: str,
            method: str = "GET",
            body: dict[str, object] | None = None,
        ) -> None:
            del method
            if "/dispatches" in path:
                raise dispatcher.GateError("dispatch failed")
            if body and "state" in body:
                states.append(str(body["state"]))

        with self.assertRaisesRegex(dispatcher.GateError, "dispatch failed"):
            dispatcher.dispatch_target(
                REPOSITORY,
                dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE),
                dispatcher.TARGETS[0],
                1,
                api_call=fail_dispatch,
            )
        self.assertEqual(states, ["pending", "error"])

    def test_reconcile_suppresses_existing_context_independently(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        run_id = 123
        trusted_terminal = {
            "creator": {"id": dispatcher.GITHUB_ACTIONS_BOT_ID},
            "description": f"passed; try=1; {dispatcher.status_pin(verified)}",
            "state": "success",
            "target_url": f"https://github.com/{REPOSITORY}/actions/runs/{run_id}",
        }
        run = {
            "id": run_id,
            "display_title": dispatcher.dispatch_run_title(verified, target, 1),
            "event": "workflow_dispatch",
            "head_branch": "main",
            "head_sha": BASE,
            "path": ".github/workflows/lab-ci-cat8kv.yaml",
        }
        with (
            mock.patch.object(dispatcher, "list_open_pr_numbers", return_value=[7]),
            mock.patch.object(dispatcher, "verify_pull_request", return_value=verified),
            mock.patch.object(
                dispatcher,
                "native_status_history",
                return_value={
                    dispatcher.TARGETS[0]["context"]: [trusted_terminal],
                    dispatcher.TARGETS[1]["context"]: [],
                },
            ),
            mock.patch.object(dispatcher, "dispatch_target") as dispatch,
        ):
            with redirect_stdout(io.StringIO()):
                dispatcher.reconcile(REPOSITORY, api_call=mock.Mock(return_value=run))

        dispatch.assert_called_once()
        self.assertEqual(dispatch.call_args.args[2], dispatcher.TARGETS[1])
        self.assertEqual(dispatch.call_args.args[3], 1)

    def test_status_for_old_prospective_merge_does_not_suppress_dispatch(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        stale = {
            "description": f"passed; base={'d' * 40}; merge={'e' * 40}",
            "state": "success",
        }
        self.assertFalse(dispatcher.status_is_for_verified_merge(stale, verified))

    def test_dispatch_error_is_retried_but_attempts_are_bounded(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pin = dispatcher.status_pin(verified)
        one_failed_attempt = [
            {
                "id": 2,
                "created_at": "2026-07-01T00:00:00Z",
                "description": f"Automatic dispatch failed; try=1; {pin}",
                "state": "error",
            },
            {
                "id": 1,
                "description": f"{target['description']}; try=1; {pin}",
                "state": "pending",
            },
        ]
        now = datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc)
        no_runs = lambda *_args, **_kwargs: {"workflow_runs": []}
        self.assertFalse(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                one_failed_attempt,
                target,
                verified,
                api_call=no_runs,
                now=now,
            )
        )

        exhausted = list(one_failed_attempt)
        for attempt in range(2, dispatcher.MAX_AUTOMATIC_DISPATCH_ATTEMPTS + 1):
            exhausted.extend(
                [
                    {
                        "id": attempt * 2,
                        "created_at": "2026-07-01T00:00:00Z",
                        "description": f"Automatic dispatch failed; try={attempt}; {pin}",
                        "state": "error",
                    },
                    {
                        "id": attempt * 2 - 1,
                        "description": f"{target['description']}; try={attempt}; {pin}",
                        "state": "pending",
                    },
                ]
            )
        exhausted.sort(key=lambda status: int(status["id"]), reverse=True)
        self.assertTrue(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                exhausted,
                target,
                verified,
                api_call=no_runs,
                now=now,
            )
        )

    def test_accepted_dispatch_timeout_does_not_duplicate_an_active_run(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pin = dispatcher.status_pin(verified)
        statuses = [
            {
                "id": 2,
                "created_at": "2026-07-01T00:00:00Z",
                "description": f"Automatic dispatch failed; try=1; {pin}",
                "state": "error",
            },
            {
                "id": 1,
                "description": f"{target['description']}; try=1; {pin}",
                "state": "pending",
            },
        ]

        def active_run(
            _path: str,
            method: str = "GET",
            body: dict[str, object] | None = None,
        ) -> object:
            del method, body
            return {
                "workflow_runs": [
                    {
                        "display_title": dispatcher.dispatch_run_title(
                            verified, target, 1
                        ),
                        "status": "queued",
                    }
                ]
            }

        self.assertTrue(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                statuses,
                target,
                verified,
                api_call=active_run,
                now=datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc),
            )
        )

    def test_pre_argo_errors_retry_after_approval_or_tooling_recovers(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pin = dispatcher.status_pin(verified)
        dispatch_marker = {
            "id": 1,
            "description": f"{target['description']}; try=1; {pin}",
            "state": "pending",
        }
        for description in (
            f"Cat8kv gate rejected; try=1; {pin}",
            f"Cat8kv incomplete; try=1; {pin}",
        ):
            with self.subTest(description=description):
                error = {
                    "id": 2,
                    "created_at": "2026-07-01T00:00:00Z",
                    "description": description,
                    "state": "error",
                }
                self.assertFalse(
                    dispatcher.status_blocks_automatic_dispatch(
                        REPOSITORY,
                        [error, dispatch_marker],
                        target,
                        verified,
                        api_call=lambda *_args, **_kwargs: {"workflow_runs": []},
                        now=datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc),
                    )
                )

    def test_next_reconcile_retries_a_failed_dispatch(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pin = dispatcher.status_pin(verified)
        history = {
            target["context"]: [
                {
                    "id": 2,
                    "created_at": "2020-01-01T00:00:00Z",
                    "description": f"Automatic dispatch failed; try=1; {pin}",
                    "state": "error",
                },
                {
                    "id": 1,
                    "description": f"{target['description']}; try=1; {pin}",
                    "state": "pending",
                },
            ],
            dispatcher.TARGETS[1]["context"]: [
                {
                    "id": 3,
                    "description": f"Cat9k cancelled; try=1; {pin}",
                    "state": "error",
                }
            ],
        }
        api_call = mock.Mock(return_value={"workflow_runs": []})
        with (
            mock.patch.object(dispatcher, "list_open_pr_numbers", return_value=[7]),
            mock.patch.object(dispatcher, "verify_pull_request", return_value=verified),
            mock.patch.object(
                dispatcher, "native_status_history", return_value=history
            ),
            mock.patch.object(dispatcher, "dispatch_target") as dispatch,
        ):
            with redirect_stdout(io.StringIO()):
                dispatcher.reconcile(REPOSITORY, api_call=api_call)
        dispatch.assert_called_once_with(
            REPOSITORY,
            verified,
            target,
            2,
            api_call=mock.ANY,
        )

    def test_stale_pending_retries_only_after_workflow_is_complete(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pending = {
            "id": 2,
            "created_at": "2026-07-01T00:00:00Z",
            "description": f"Cat8kv testing; try=1; {dispatcher.status_pin(verified)}",
            "state": "pending",
            "target_url": f"https://github.com/{REPOSITORY}/actions/runs/123",
        }
        dispatch_marker = {
            "id": 1,
            "description": f"{target['description']}; try=1; {dispatcher.status_pin(verified)}",
            "state": "pending",
        }
        now = datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc)

        def workflow_with_status(status: str):
            def response(path: str, *_args, **_kwargs) -> object:
                if path.endswith("/actions/runs/123"):
                    return {"status": status}
                if "/actions/workflows/" in path:
                    return {"workflow_runs": []}
                raise AssertionError(f"unexpected API call: {path}")

            return response

        self.assertTrue(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [pending, dispatch_marker],
                target,
                verified,
                api_call=workflow_with_status("in_progress"),
                now=now,
            )
        )
        self.assertFalse(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [pending, dispatch_marker],
                target,
                verified,
                api_call=workflow_with_status("completed"),
                now=now,
            )
        )

    def test_stale_generic_marker_does_not_duplicate_a_correlated_queue(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        pending = {
            "id": 1,
            "created_at": "2026-07-01T00:00:00Z",
            "description": (
                f"{target['description']}; try=1; {dispatcher.status_pin(verified)}"
            ),
            "state": "pending",
            "target_url": (
                f"https://github.com/{REPOSITORY}/actions/workflows/"
                f"{target['workflow']}"
            ),
        }

        def correlated_queue(path: str, *_args, **_kwargs) -> object:
            if "/actions/workflows/" not in path:
                raise AssertionError(f"unexpected API call: {path}")
            return {
                "workflow_runs": [
                    {
                        "display_title": dispatcher.dispatch_run_title(
                            verified, target, 1
                        ),
                        "status": "waiting",
                    }
                ]
            }

        self.assertTrue(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [pending],
                target,
                verified,
                api_call=correlated_queue,
                now=datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc),
            )
        )
        self.assertFalse(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [pending],
                target,
                verified,
                api_call=lambda *_args, **_kwargs: {"workflow_runs": []},
                now=datetime.datetime(2026, 7, 2, tzinfo=datetime.timezone.utc),
            )
        )

    def test_test_failure_is_terminal_for_automatic_retry(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        target = dispatcher.TARGETS[0]
        run_id = 123
        status = {
            "creator": {"id": dispatcher.GITHUB_ACTIONS_BOT_ID},
            "description": f"Cat8kv failed; try=1; {dispatcher.status_pin(verified)}",
            "state": "failure",
            "target_url": f"https://github.com/{REPOSITORY}/actions/runs/{run_id}",
        }
        trusted_run = {
            "id": run_id,
            "display_title": dispatcher.dispatch_run_title(verified, target, 1),
            "event": "workflow_dispatch",
            "head_branch": "main",
            "head_sha": BASE,
            "path": ".github/workflows/lab-ci-cat8kv.yaml",
        }
        self.assertTrue(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [status],
                target,
                verified,
                api_call=mock.Mock(return_value=trusted_run),
            )
        )
        forged = copy.deepcopy(status)
        forged["creator"] = {"id": 1}
        self.assertFalse(
            dispatcher.status_blocks_automatic_dispatch(
                REPOSITORY,
                [forged],
                target,
                verified,
                api_call=mock.Mock(),
            )
        )

    def test_status_descriptions_preserve_pin_and_fit_github_limit(self) -> None:
        verified = dispatcher.VerifiedPullRequest(7, HEAD, BASE, MERGE)
        pin = dispatcher.status_pin(verified)
        descriptions = [
            f"{target['description']}; try=1; {pin}" for target in dispatcher.TARGETS
        ] + [f"Automatic dispatch failed; try=1; {pin}"]
        for description in descriptions:
            with self.subTest(description=description):
                self.assertIn(pin, description)
                self.assertLessEqual(
                    len(description), dispatcher.STATUS_DESCRIPTION_LIMIT
                )

        with self.assertRaisesRegex(dispatcher.GateError, "exceeds"):
            dispatcher.post_status(
                REPOSITORY,
                HEAD,
                context="test",
                state="pending",
                description="x" * (dispatcher.STATUS_DESCRIPTION_LIMIT + 1),
                target_url="https://example.invalid",
                api_call=mock.Mock(),
            )

    def test_untrusted_cli_values_are_rejected(self) -> None:
        with self.assertRaises(dispatcher.GateError):
            dispatcher.validate_repository("owner/repo;touch-pwned")
        with self.assertRaises(dispatcher.GateError):
            dispatcher.validate_pr_number("7; rm -rf")
        with self.assertRaises(dispatcher.GateError):
            dispatcher.validate_sha("head", "not-a-sha")

    def test_workflow_security_contracts_are_present(self) -> None:
        repository_root = MODULE_PATH.parents[2]
        dispatcher_workflow = (
            repository_root / ".github/workflows/lab-ci-auto-dispatch.yaml"
        ).read_text()
        signal_workflow = (
            repository_root / ".github/workflows/lab-ci-approval-signal.yaml"
        ).read_text()

        self.assertIn("workflow_run:", dispatcher_workflow)
        self.assertIn("schedule:", dispatcher_workflow)
        self.assertIn("actions: write", dispatcher_workflow)
        self.assertIn("checks: read", dispatcher_workflow)
        self.assertIn("LAB_CI_AUTO_DISPATCH_ENABLED", dispatcher_workflow)
        self.assertIn("ref: main", dispatcher_workflow)
        self.assertIn("workflow_run.conclusion == 'success'", dispatcher_workflow)
        self.assertIn("approval signal [approved] for PR #", dispatcher_workflow)
        self.assertNotIn("self-hosted", dispatcher_workflow)

        self.assertIn("pull_request_review:", signal_workflow)
        self.assertIn("github.event.review.state", signal_workflow)
        self.assertNotIn("github.event.review.author_association", signal_workflow)
        self.assertIn("permissions: {}", signal_workflow)
        self.assertNotIn("actions/checkout", signal_workflow)

        for workflow_name in ("lab-ci-cat8kv.yaml", "lab-ci-cat9k.yaml"):
            wrapper = (
                repository_root / ".github/workflows" / workflow_name
            ).read_text()
            self.assertIn("expected_head_sha:", wrapper)
            self.assertIn("expected_base_sha:", wrapper)
            self.assertIn("expected_merge_sha:", wrapper)
            self.assertIn("dispatch_attempt:", wrapper)
            self.assertIn("dispatch_id:", wrapper)
            self.assertIn("run-name: Lab CI", wrapper)
            self.assertIn("checks: read", wrapper)
            self.assertEqual(wrapper.count("dispatch-approved-lab-ci.py verify"), 2)
            self.assertIn("Checkout trusted gate at the verified base", wrapper)
            self.assertIn("ref: ${{ needs.prepare.outputs.base_sha }}", wrapper)
            self.assertIn("gate_outcome: ${{ steps.gate.outcome }}", wrapper)
            self.assertIn('"$GATE_OUTCOME" == "failure"', wrapper)
            self.assertIn(
                '[[ -n "$ARGO_WORKFLOW" && "$ARGO_PHASE" == "Failed" ]]',
                wrapper,
            )
            self.assertIn("environment: lab-ci", wrapper)
            status_templates = [
                template
                for template in re.findall(
                    r'(?:description=|DESCRIPTION=)"([^"]+)"', wrapper
                )
                if "$BASE_SHA" in template and "$MERGE_SHA" in template
            ]
            self.assertEqual(len(status_templates), 6)
            for template in status_templates:
                rendered = template.replace("$BASE_SHA", BASE).replace(
                    "$MERGE_SHA", MERGE
                )
                self.assertIn(f"base={BASE}; merge={MERGE}", rendered)
                self.assertLessEqual(len(rendered), dispatcher.STATUS_DESCRIPTION_LIMIT)


if __name__ == "__main__":
    unittest.main()
