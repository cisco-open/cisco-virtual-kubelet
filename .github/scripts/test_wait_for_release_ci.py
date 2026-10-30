# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
"""Offline readiness and polling regression tests; no release mutations."""
from __future__ import annotations

import datetime as dt
import unittest
from unittest import mock

import wait_for_release_ci as guard

REPOSITORY = "cisco-open/cisco-virtual-kubelet"
COMMIT = "e68dd3d2454485e535fc30468847895044a850dd"
CUTOFF = dt.datetime(2026, 10, 30, 12, 0, tzinfo=dt.timezone.utc)


def make_run(**overrides):
    run = {
        "id": 123,
        "run_attempt": 1,
        "repository": {"full_name": REPOSITORY},
        "path": ".github/workflows/smoke.yml",
        "name": "smoke",
        "head_sha": COMMIT,
        "head_branch": "main",
        "event": "push",
        "status": "completed",
        "conclusion": "success",
        "updated_at": "2026-10-30T11:59:00Z",
    }
    run.update(overrides)
    return run


class FakeClock:
    def __init__(self):
        self.elapsed = 0.0
        self.sleeps = []

    def monotonic(self):
        return self.elapsed

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.elapsed += seconds


class FakeAPI:
    def __init__(self, snapshots):
        self.snapshots = snapshots
        self.index = -1
        self.calls = []
        self.current = []

    def __call__(self, path):
        self.calls.append(path)
        if "/workflows/smoke.yml/runs?" in path:
            self.index = min(self.index + 1, len(self.snapshots) - 1)
            self.current = self.snapshots[self.index]
            return {
                "total_count": len(self.current),
                "workflow_runs": [
                    {"id": run["id"], "run_attempt": run["run_attempt"]}
                    for run in self.current
                ],
            }
        for run in self.current:
            if path.endswith(f'/runs/{run["id"]}/attempts/{run["run_attempt"]}'):
                return run
        raise AssertionError(path)


class EvaluateTests(unittest.TestCase):
    def evaluate(self, runs):
        return guard.evaluate_runs(REPOSITORY, COMMIT, runs, completed_before=CUTOFF)

    def test_unique_success_uses_trusted_path_not_dynamic_name(self):
        run = make_run(
            name="custom smoke run title",
            path=".github/workflows/smoke.yml@refs/heads/main",
        )
        self.assertEqual(self.evaluate([run]).state, "ready")
        self.assertIs(self.evaluate([run]).run, run)

    def test_old_failure_can_coexist_with_unique_success(self):
        self.assertEqual(
            self.evaluate([make_run(id=1, conclusion="failure"), make_run()]).state,
            "ready",
        )

    def test_active_run_blocks_an_older_success(self):
        for status in guard.ACTIVE:
            with self.subTest(status=status):
                state = self.evaluate(
                    [make_run(), make_run(id=2, status=status, conclusion=None)]
                )
                self.assertEqual(state.state, "waiting")
                self.assertIsNone(state.run)

    def test_duplicate_successes_are_ambiguous(self):
        with self.assertRaisesRegex(guard.ReleaseCIError, "ambiguous"):
            self.evaluate([make_run(), make_run(id=2)])

    def test_terminal_non_success_is_actionable(self):
        for conclusion in guard.TERMINAL - {"success"}:
            with self.subTest(conclusion=conclusion):
                self.assertEqual(
                    self.evaluate([make_run(conclusion=conclusion)]).state, "failed"
                )

    def test_missing_run_waits(self):
        self.assertEqual(self.evaluate([]).state, "waiting")

    def test_wrong_identity_is_rejected(self):
        for overrides in [
            {"head_sha": "a" * 40},
            {"head_branch": "topic"},
            {"event": "pull_request"},
            {"repository": {"full_name": "someone/else"}},
            {"path": ".github/workflows/release.yml"},
            {"repository": []},
            {"path": None},
        ]:
            with self.subTest(overrides=overrides), self.assertRaises(
                guard.ReleaseCIError
            ):
                self.evaluate([make_run(**overrides)])

    def test_late_and_malformed_completion_are_rejected(self):
        for value in [
            "2026-10-30T12:00:01Z",
            "2026-02-30T11:00:00Z",
            "2026-10-30T11:00:00",
            "2026-10-30T11:00:00+01:00",
            None,
            "bad",
        ]:
            with self.subTest(value=value), self.assertRaises(guard.ReleaseCIError):
                self.evaluate([make_run(updated_at=value)])
        self.assertEqual(
            self.evaluate([make_run(updated_at="2026-10-30T12:00:00Z")]).state, "ready"
        )

    def test_malformed_state_ids_and_duplicate_ids_are_rejected(self):
        for overrides in [
            {"id": True},
            {"run_attempt": 0},
            {"status": "new-state"},
            {"conclusion": None},
            {"status": []},
            {"conclusion": []},
            {"status": "queued", "conclusion": "success"},
        ]:
            with self.subTest(overrides=overrides), self.assertRaises(
                guard.ReleaseCIError
            ):
                self.evaluate([make_run(**overrides)])
        with self.assertRaises(guard.ReleaseCIError):
            self.evaluate([make_run(), make_run()])


class LoadTests(unittest.TestCase):
    def test_query_uses_exact_commit_and_refetches_attempt_without_success_filter(self):
        api = FakeAPI([[make_run(run_attempt=2)]])
        self.assertEqual(
            guard.load_runs(REPOSITORY, COMMIT, api_call=api)[0]["run_attempt"], 2
        )
        self.assertIn(f"branch=main&event=push&head_sha={COMMIT}", api.calls[0])
        self.assertNotIn("status=", api.calls[0])
        self.assertTrue(api.calls[1].endswith("/runs/123/attempts/2"))

    def test_pagination_refetches_every_candidate(self):
        runs = [make_run(id=n, conclusion="failure") for n in range(1, 102)]
        calls = []

        def api(path):
            calls.append(path)
            if "/workflows/" in path:
                page = int(path.rsplit("page=", 1)[1])
                return {
                    "total_count": 101,
                    "workflow_runs": runs[(page - 1) * 100 : page * 100],
                }
            run_id = int(path.split("/runs/")[1].split("/")[0])
            return runs[run_id - 1]

        self.assertEqual(len(guard.load_runs(REPOSITORY, COMMIT, api_call=api)), 101)
        self.assertEqual(len(calls), 103)

    def test_bad_page_and_truncated_search_are_rejected(self):
        for page in [
            None,
            {"workflow_runs": []},
            {"total_count": 501, "workflow_runs": []},
            {"total_count": 2, "workflow_runs": [make_run()]},
            {"total_count": 1, "workflow_runs": [None]},
        ]:
            with self.subTest(page=page), self.assertRaises(guard.ReleaseCIError):
                guard.load_runs(REPOSITORY, COMMIT, api_call=lambda path: page)

    def test_exact_500_candidates_are_fully_refetched(self):
        calls = []

        def api(path):
            calls.append(path)
            if "/workflows/" in path:
                page = int(path.rsplit("page=", 1)[1])
                return {
                    "total_count": 500,
                    "workflow_runs": [
                        {"id": n, "run_attempt": 1}
                        for n in range((page - 1) * 100 + 1, page * 100 + 1)
                    ],
                }
            run_id = int(path.split("/runs/")[1].split("/")[0])
            return make_run(
                id=run_id, conclusion="success" if run_id == 1 else "failure"
            )

        runs = guard.load_runs(REPOSITORY, COMMIT, api_call=api)
        self.assertEqual(len(runs), 500)
        self.assertEqual(len(calls), 505)
        self.assertEqual(
            guard.evaluate_runs(
                REPOSITORY, COMMIT, runs, completed_before=CUTOFF
            ).state,
            "ready",
        )

    def test_total_count_drift_and_overfull_search_are_rejected(self):
        for final_count in [100, 102]:

            def api(path):
                if path.endswith("page=1"):
                    return {
                        "total_count": 101,
                        "workflow_runs": [
                            {"id": n, "run_attempt": 1} for n in range(1, 101)
                        ],
                    }
                return {"total_count": final_count, "workflow_runs": [make_run()]}

            with self.subTest(final_count=final_count), self.assertRaises(
                guard.ReleaseCIError
            ):
                guard.load_runs(REPOSITORY, COMMIT, api_call=api)
        with self.assertRaises(guard.ReleaseCIError):
            guard.load_runs(
                REPOSITORY,
                COMMIT,
                api_call=lambda path: {"total_count": 0, "workflow_runs": [make_run()]},
            )

    def test_refetched_identity_and_attempt_must_match_summary(self):
        for replacement in [
            make_run(id=999),
            make_run(run_attempt=2),
            make_run(id=True),
            None,
        ]:

            def api(path):
                if "/workflows/" in path:
                    return {
                        "total_count": 1,
                        "workflow_runs": [{"id": 123, "run_attempt": 1}],
                    }
                return replacement

            with self.subTest(replacement=replacement), self.assertRaises(
                guard.ReleaseCIError
            ):
                guard.load_runs(REPOSITORY, COMMIT, api_call=api)

    def test_point_lookup_failure_does_not_create_false_unique_success(self):
        def api(path):
            if "/workflows/" in path:
                return {"total_count": 2, "workflow_runs": [make_run(), make_run(id=2)]}
            if path.endswith("/runs/123/attempts/1"):
                return make_run()
            raise guard.ReleaseCIError("read failed")

        with self.assertRaisesRegex(guard.ReleaseCIError, "read failed"):
            guard.load_runs(REPOSITORY, COMMIT, api_call=api)

    def test_gh_errors_are_sanitized_and_use_get_only(self):
        with mock.patch.object(
            guard.subprocess,
            "run",
            side_effect=guard.subprocess.CalledProcessError(
                1, "gh", stderr="private detail"
            ),
        ):
            with self.assertRaises(guard.ReleaseCIError) as raised:
                guard.github_api("/repos/example/repo/actions/runs/1")
            self.assertNotIn("private detail", str(raised.exception))
        with mock.patch.object(
            guard.subprocess, "run", return_value=mock.Mock(stdout="{}")
        ) as call:
            self.assertEqual(guard.github_api("/repos/example/repo/actions/runs/1"), {})
            args = call.call_args.args[0]
            self.assertNotIn("--method", args)
            self.assertNotIn("--input", args)
            self.assertNotIn("-f", args)


class PollTests(unittest.TestCase):
    def invoke(self, api, clock, **options):
        return guard.wait_for_release_ci(
            REPOSITORY,
            COMMIT,
            api_call=api,
            sleep=clock.sleep,
            monotonic=clock.monotonic,
            now=lambda: CUTOFF,
            report=lambda message: None,
            **options,
        )

    def test_missing_then_active_then_success(self):
        api = FakeAPI(
            [[], [make_run(status="in_progress", conclusion=None)], [make_run()]]
        )
        clock = FakeClock()
        self.assertEqual(self.invoke(api, clock, timeout=60, interval=15)["id"], 123)
        self.assertEqual(clock.sleeps, [15, 15])

    def test_success_plus_active_waits_and_detects_later_ambiguity(self):
        api = FakeAPI(
            [
                [make_run(), make_run(id=2, status="queued", conclusion=None)],
                [make_run(), make_run(id=2)],
            ]
        )
        clock = FakeClock()
        with self.assertRaisesRegex(guard.ReleaseCIError, "ambiguous"):
            self.invoke(api, clock, timeout=60, interval=15)
        self.assertEqual(clock.sleeps, [15])

    def test_failed_terminal_stops_without_sleep(self):
        clock = FakeClock()
        with self.assertRaisesRegex(guard.ReleaseCIError, "without success"):
            self.invoke(FakeAPI([[make_run(conclusion="failure")]]), clock)
        self.assertEqual(clock.sleeps, [])

    def test_check_does_not_sleep_and_timeout_is_bounded(self):
        clock = FakeClock()
        with self.assertRaises(guard.ReleaseCIError):
            self.invoke(FakeAPI([[]]), clock, check=True)
        self.assertEqual(clock.sleeps, [])
        with self.assertRaises(guard.ReleaseCIError):
            self.invoke(FakeAPI([[]]), clock, timeout=20, interval=15)
        self.assertEqual(clock.sleeps, [15, 5])
        self.assertEqual(clock.elapsed, 20)

    def test_metadata_reads_cannot_authorize_after_wait_deadline(self):
        clock = FakeClock()
        wrapped = FakeAPI([[make_run()]])

        def slow_api(path):
            clock.elapsed += 12
            return wrapped(path)

        with self.assertRaisesRegex(guard.ReleaseCIError, "timed out during metadata"):
            self.invoke(slow_api, clock, timeout=20)
        self.assertEqual(clock.sleeps, [])

    def test_api_failure_does_not_poll_or_authorize(self):
        clock = FakeClock()
        with self.assertRaisesRegex(guard.ReleaseCIError, "API unavailable"):
            self.invoke(
                lambda path: (_ for _ in ()).throw(
                    guard.ReleaseCIError("API unavailable")
                ),
                clock,
            )
        self.assertEqual(clock.sleeps, [])

    def test_invalid_options_are_rejected_before_network(self):
        api = mock.Mock(side_effect=AssertionError("unexpected API call"))
        for repository, commit in [
            ("../repo", COMMIT),
            (REPOSITORY, "short"),
            ("repo", COMMIT),
        ]:
            with self.subTest(repository=repository, commit=commit), self.assertRaises(
                guard.ReleaseCIError
            ):
                guard.wait_for_release_ci(repository, commit, api_call=api)
        for options in [
            {"timeout": -1},
            {"interval": 0},
            {"interval": 61},
            {"timeout": float("nan")},
        ]:
            with self.subTest(options=options), self.assertRaises(guard.ReleaseCIError):
                self.invoke(api, FakeClock(), **options)
        api.assert_not_called()


if __name__ == "__main__":
    unittest.main()
