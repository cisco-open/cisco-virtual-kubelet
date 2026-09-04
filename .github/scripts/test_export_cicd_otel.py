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

import importlib.util
import io
import json
import pathlib
import sys
import unittest
from typing import Any
from unittest import mock


SCRIPT_DIR = pathlib.Path(__file__).parent
sys.path.insert(0, str(SCRIPT_DIR))
MODULE_PATH = SCRIPT_DIR / "export-cicd-otel.py"
SPEC = importlib.util.spec_from_file_location("export_cicd_otel", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
exporter = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = exporter
SPEC.loader.exec_module(exporter)

import cicd_otel  # noqa: E402


REPOSITORY = "cisco-open/cisco-virtual-kubelet"
RUN_ID = 123456789
ATTEMPT = 2
CHECK_RUN_ID = 987654321
HEAD_SHA = "a" * 40
MERGE_SHA = "b" * 40
RELEASE_TAG = "v2026.9.2"
RELEASE_ID = 380593737


def completed_run(**overrides: object) -> dict[str, object]:
    run: dict[str, object] = {
        "id": RUN_ID,
        "run_attempt": ATTEMPT,
        # GitHub's attempts API may expose a workflow's dynamic ``run-name``
        # here instead of the static top-level workflow name. Production code
        # must derive workflow identity from the trusted path below.
        "name": "Smoke validation for a dynamic run",
        "path": ".github/workflows/smoke.yml",
        "status": "completed",
        "conclusion": "success",
        "display_title": "Validate lifecycle tracing",
        "event": "pull_request",
        "created_at": "2026-09-02T10:00:00Z",
        "run_started_at": "2026-09-02T10:00:03Z",
        "updated_at": "2026-09-02T10:04:00Z",
        "head_branch": "feature/tracing",
        "head_sha": HEAD_SHA,
        "html_url": f"https://github.com/{REPOSITORY}/actions/runs/{RUN_ID}",
        "repository": {"full_name": REPOSITORY},
        "pull_requests": [
            {"number": 181, "head": {"sha": HEAD_SHA}},
        ],
    }
    run.update(overrides)
    return run


def completed_jobs() -> list[dict[str, object]]:
    return [
        {
            "id": CHECK_RUN_ID,
            "name": "build-and-smoke",
            "conclusion": "success",
            "created_at": "2026-09-02T10:00:01Z",
            "started_at": "2026-09-02T10:00:04Z",
            "completed_at": "2026-09-02T10:03:59Z",
            "runner_name": "GitHub Actions 1",
            "html_url": "https://example.invalid/job",
            "steps": [
                {
                    "name": "Build",
                    "number": 1,
                    "conclusion": "success",
                    "started_at": "2026-09-02T10:00:05Z",
                    "completed_at": "2026-09-02T10:01:05Z",
                },
                {
                    "name": "Unit tests",
                    "number": 2,
                    "conclusion": "success",
                    "started_at": "2026-09-02T10:01:06Z",
                    "completed_at": "2026-09-02T10:03:58Z",
                },
            ],
        }
    ]


def span_attributes(span: dict[str, object]) -> dict[str, object]:
    values: dict[str, object] = {}
    for item in span["attributes"]:
        encoded = item["value"]
        values[item["key"]] = next(iter(encoded.values()))
    return values


def link_attributes(link: dict[str, object]) -> dict[str, object]:
    values: dict[str, object] = {}
    for item in link["attributes"]:
        encoded = item["value"]
        values[item["key"]] = next(iter(encoded.values()))
    return values


def mapped_api(
    responses: dict[str, Any],
) -> tuple[Any, list[str]]:
    calls: list[str] = []

    def call(path: str) -> Any:
        calls.append(path)
        if path not in responses:
            raise AssertionError(f"unexpected API request: {path}")
        response = responses[path]
        if isinstance(response, Exception):
            raise response
        return response

    return call, calls


def workflow_search_path(
    workflow: str,
    query: str,
    *,
    page: int = 1,
) -> str:
    return (
        f"/repos/{REPOSITORY}/actions/workflows/{workflow}/runs?{query}"
        f"&per_page=100&page={page}"
    )


def release_record(**overrides: object) -> dict[str, object]:
    release: dict[str, object] = {
        "id": RELEASE_ID,
        "tag_name": RELEASE_TAG,
        "target_commitish": MERGE_SHA,
        "draft": False,
        "prerelease": False,
        "immutable": True,
        "published_at": "2026-09-02T12:00:00Z",
        "html_url": f"https://github.com/{REPOSITORY}/releases/tag/{RELEASE_TAG}",
    }
    release.update(overrides)
    return release


def merged_pull(number: int = 181, **overrides: object) -> dict[str, object]:
    pull: dict[str, object] = {
        "number": number,
        "state": "closed",
        "merged": True,
        "merged_at": "2026-09-02T10:59:59Z",
        "merge_commit_sha": MERGE_SHA,
        "head": {"sha": HEAD_SHA},
        "base": {"ref": "main", "repo": {"full_name": REPOSITORY}},
    }
    pull.update(overrides)
    return pull


class Response:
    def __init__(self, payload: bytes = b"{}", status: int = 200) -> None:
        self.payload = io.BytesIO(payload)
        self.status = status
        self.headers: dict[str, str] = {"Content-Length": str(len(payload))}

    def __enter__(self) -> "Response":
        return self

    def __exit__(self, *_: object) -> None:
        return None

    def read(self, size: int = -1) -> bytes:
        return self.payload.read(size)

    def getcode(self) -> int:
        return self.status


class ExporterTests(unittest.TestCase):
    def test_payload_matches_githubreceiver_ids_and_pr_lifecycle(self) -> None:
        payload, lifecycle = exporter.build_otlp_payload(
            REPOSITORY, completed_run(), completed_jobs()
        )
        self.assertEqual(lifecycle, f"cvk-pr181-h{HEAD_SHA}")
        spans = payload["resourceSpans"][0]["scopeSpans"][0]["spans"]
        root, job, queue, build, unit = spans
        self.assertEqual(root["traceId"], cicd_otel.github_trace_id(RUN_ID, ATTEMPT))
        self.assertEqual(root["spanId"], cicd_otel.github_root_span_id(RUN_ID, ATTEMPT))
        self.assertEqual(job["spanId"], cicd_otel.github_job_span_id(CHECK_RUN_ID))
        self.assertEqual(job["parentSpanId"], root["spanId"])
        self.assertEqual(queue["spanId"], cicd_otel.github_queue_span_id(CHECK_RUN_ID))
        self.assertEqual(build["spanId"], cicd_otel.github_step_span_id(CHECK_RUN_ID, "Build"))
        self.assertEqual(unit["spanId"], cicd_otel.github_step_span_id(CHECK_RUN_ID, "Unit tests"))
        self.assertEqual(span_attributes(root)["cvk.lifecycle.id"], lifecycle)
        self.assertEqual(root["kind"], 2)
        self.assertEqual(job["startTimeUnixNano"], "1788343201000000000")
        retry = root["links"][0]
        self.assertEqual(
            retry["traceId"], cicd_otel.github_trace_id(RUN_ID, ATTEMPT - 1)
        )
        self.assertEqual(
            retry["spanId"], cicd_otel.github_root_span_id(RUN_ID, ATTEMPT - 1)
        )
        self.assertEqual(link_attributes(retry)["cvk.link.type"], "github.workflow.retry")

    def test_wrapper_root_links_to_trusted_dispatch_step(self) -> None:
        upstream = "00-" + "1" * 32 + "-" + "2" * 16 + "-01"
        title = (
            f"Lab CI Cat8kv - cvk-pr181-h{HEAD_SHA} - dispatch - {upstream}"
        )
        run = completed_run(
            name=title,
            path=".github/workflows/lab-ci-cat8kv.yaml",
            pull_requests=[],
            display_title=title,
        )
        payload, lifecycle = exporter.build_otlp_payload(REPOSITORY, run, [])
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(lifecycle, f"cvk-pr181-h{HEAD_SHA}")
        self.assertEqual(root["name"], "Lab CI (Cat8kv)")
        self.assertEqual(
            span_attributes(root)["cicd.pipeline.name"], "Lab CI (Cat8kv)"
        )
        self.assertEqual(
            span_attributes(root)["github.workflow.run.display_title"], title
        )
        self.assertEqual(root["links"][0]["traceId"], "1" * 32)
        self.assertEqual(root["links"][0]["spanId"], "2" * 16)
        self.assertNotIn(
            "cvk.upstream.lifecycle.id", link_attributes(root["links"][0])
        )

    def test_dispatcher_root_links_to_triggering_workflow_root(self) -> None:
        upstream_run = 4444
        upstream_attempt = 3
        title = (
            "Lab CI approved dispatcher - "
            f"github-upstream-run{upstream_run}-a{upstream_attempt}"
        )
        run = completed_run(
            name=title,
            path=".github/workflows/lab-ci-auto-dispatch.yaml",
            pull_requests=[],
            display_title=title,
        )
        payload, _ = exporter.build_otlp_payload(REPOSITORY, run, [])
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(root["name"], "Lab CI approved dispatcher")
        self.assertEqual(
            root["links"][0]["traceId"],
            cicd_otel.github_trace_id(upstream_run, upstream_attempt),
        )
        self.assertEqual(
            root["links"][0]["spanId"],
            cicd_otel.github_root_span_id(upstream_run, upstream_attempt),
        )

    def test_required_title_carriers_fail_visible_but_keep_base_trace(self) -> None:
        run = completed_run(
            run_attempt=1,
            name="Lab CI (Cat8kv)",
            path=".github/workflows/lab-ci-cat8kv.yaml",
            event="workflow_dispatch",
            pull_requests=[],
            display_title="Lab CI Cat8kv - missing trusted carriers",
        )

        correlation = exporter.base_correlation_for_run(run)
        payload, lifecycle = exporter.build_otlp_payload(
            REPOSITORY, run, [], correlation
        )

        self.assertEqual(lifecycle, f"cvk-run{RUN_ID}-a1")
        self.assertEqual(correlation.references, ())
        self.assertTrue(correlation.required_transition_failed)
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(
            span_attributes(root)["cvk.correlation.state"], "base_metadata_invalid"
        )

    def test_release_lifecycle_is_stable_across_downstream_workflows(self) -> None:
        release = completed_run(
            name=f"Release {RELEASE_TAG}",
            path=".github/workflows/release.yml",
            pull_requests=[],
            event="push",
            head_branch=RELEASE_TAG,
        )
        docs = completed_run(
            name=f"Publish documentation for {RELEASE_TAG}",
            path=".github/workflows/develop.yml",
            pull_requests=[],
            event="release",
            head_branch=RELEASE_TAG,
        )
        self.assertEqual(exporter.lifecycle_for_run(release), f"cvk-release-{RELEASE_TAG}")
        self.assertEqual(exporter.lifecycle_for_run(docs), f"cvk-release-{RELEASE_TAG}")

    def test_main_smoke_links_to_unique_successful_pr_smoke(self) -> None:
        main = completed_run(
            run_attempt=1,
            event="push",
            head_branch="main",
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:00:00Z",
            run_started_at="2026-09-02T11:00:00Z",
            updated_at="2026-09-02T11:20:00Z",
        )
        pr_run_id = 222222222
        pr_run = completed_run(
            id=pr_run_id,
            run_attempt=1,
            event="pull_request",
            head_sha=HEAD_SHA,
            pull_requests=[],
            created_at="2026-09-02T10:30:00Z",
            run_started_at="2026-09-02T10:30:00Z",
            updated_at="2026-09-02T10:55:00Z",
        )
        responses = {
            f"/repos/{REPOSITORY}/commits/{MERGE_SHA}/pulls?per_page=100&page=1": [
                {"number": 181}
            ],
            f"/repos/{REPOSITORY}/pulls/181": merged_pull(),
            workflow_search_path(
                "smoke.yml",
                f"event=pull_request&status=success&head_sha={HEAD_SHA}",
            ): {"workflow_runs": [{"id": pr_run_id, "run_attempt": 1}]},
            f"/repos/{REPOSITORY}/actions/runs/{pr_run_id}/attempts/1": pr_run,
        }
        api, _ = mapped_api(responses)

        correlation = exporter.resolve_run_correlation(
            REPOSITORY, main, token="not-logged", api_call=api
        )

        self.assertEqual(correlation.lifecycle_id, f"cvk-pr181-h{HEAD_SHA}")
        self.assertEqual(correlation.state, "main_pr_linked")
        self.assertEqual(len(correlation.references), 1)
        reference = correlation.references[0]
        self.assertEqual(reference.link_type, "github.pull_request.merge")
        self.assertEqual(
            reference.traceparent,
            cicd_otel.github_root_traceparent(pr_run_id, 1),
        )
        payload, _ = exporter.build_otlp_payload(REPOSITORY, main, [], correlation)
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(
            link_attributes(root["links"][0])["cvk.link.type"],
            "github.pull_request.merge",
        )

    def test_main_smoke_direct_push_and_ambiguous_merge_never_guess(self) -> None:
        main = completed_run(
            run_attempt=1,
            event="push",
            head_branch="main",
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:00:00Z",
        )
        commit_pulls = (
            f"/repos/{REPOSITORY}/commits/{MERGE_SHA}/pulls?per_page=100&page=1"
        )
        direct_api, _ = mapped_api({commit_pulls: []})
        direct = exporter.resolve_run_correlation(
            REPOSITORY, main, token="not-logged", api_call=direct_api
        )
        self.assertEqual(direct.state, "main_direct_push")
        self.assertEqual(direct.references, ())
        self.assertEqual(direct.warnings, ())
        self.assertFalse(direct.required_transition_failed)

        ambiguous_api, _ = mapped_api(
            {
                commit_pulls: [{"number": 181}, {"number": 182}],
                f"/repos/{REPOSITORY}/pulls/181": merged_pull(181),
                f"/repos/{REPOSITORY}/pulls/182": merged_pull(182),
            }
        )
        ambiguous = exporter.resolve_run_correlation(
            REPOSITORY, main, token="not-logged", api_call=ambiguous_api
        )
        self.assertEqual(ambiguous.state, "main_ambiguous")
        self.assertEqual(ambiguous.references, ())
        self.assertTrue(ambiguous.warnings)
        self.assertTrue(ambiguous.required_transition_failed)

    def test_pr_workflow_empty_pull_array_uses_unique_commit_metadata(self) -> None:
        run = completed_run(run_attempt=1, pull_requests=[])
        commit_pulls = (
            f"/repos/{REPOSITORY}/commits/{HEAD_SHA}/pulls?per_page=100&page=1"
        )
        api, _ = mapped_api(
            {
                commit_pulls: [{"number": 181}],
                f"/repos/{REPOSITORY}/pulls/181": merged_pull(
                    state="open", merged=False, merged_at=None
                ),
            }
        )
        correlation = exporter.resolve_run_correlation(
            REPOSITORY, run, token="not-logged", api_call=api
        )
        self.assertEqual(correlation.lifecycle_id, f"cvk-pr181-h{HEAD_SHA}")
        self.assertEqual(correlation.state, "pull_request")

    def test_release_tag_links_to_unique_successful_main_smoke(self) -> None:
        release_run = completed_run(
            run_attempt=1,
            name="release",
            path=".github/workflows/release.yml",
            event="push",
            head_branch=RELEASE_TAG,
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:30:00Z",
            run_started_at="2026-09-02T11:30:00Z",
        )
        main_run_id = 333333333
        main_run = completed_run(
            id=main_run_id,
            run_attempt=1,
            event="push",
            head_branch="main",
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:00:00Z",
            run_started_at="2026-09-02T11:00:00Z",
            updated_at="2026-09-02T11:20:00Z",
        )
        api, _ = mapped_api(
            {
                workflow_search_path(
                    "smoke.yml",
                    f"branch=main&event=push&status=success&head_sha={MERGE_SHA}",
                ): {"workflow_runs": [{"id": main_run_id, "run_attempt": 1}]},
                f"/repos/{REPOSITORY}/actions/runs/{main_run_id}/attempts/1": main_run,
            }
        )
        correlation = exporter.resolve_run_correlation(
            REPOSITORY, release_run, token="not-logged", api_call=api
        )
        self.assertEqual(correlation.lifecycle_id, f"cvk-release-{RELEASE_TAG}")
        self.assertEqual(correlation.state, "release_main_smoke_linked")
        self.assertEqual(
            correlation.references[0].traceparent,
            cicd_otel.github_root_traceparent(main_run_id, 1),
        )
        self.assertEqual(
            correlation.references[0].link_type, "github.tag.after-main-ci"
        )

    def test_release_tag_with_duplicate_main_runs_does_not_guess(self) -> None:
        release_run = completed_run(
            run_attempt=1,
            name="release",
            path=".github/workflows/release.yml",
            event="push",
            head_branch=RELEASE_TAG,
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:30:00Z",
        )
        runs = []
        responses: dict[str, Any] = {}
        for run_id in (333333333, 333333334):
            runs.append({"id": run_id, "run_attempt": 1})
            responses[
                f"/repos/{REPOSITORY}/actions/runs/{run_id}/attempts/1"
            ] = completed_run(
                id=run_id,
                run_attempt=1,
                event="push",
                head_branch="main",
                head_sha=MERGE_SHA,
                pull_requests=[],
                created_at="2026-09-02T11:00:00Z",
                updated_at="2026-09-02T11:20:00Z",
            )
        responses[
            workflow_search_path(
                "smoke.yml",
                f"branch=main&event=push&status=success&head_sha={MERGE_SHA}",
            )
        ] = {"workflow_runs": runs}
        api, _ = mapped_api(responses)
        correlation = exporter.resolve_run_correlation(
            REPOSITORY, release_run, token="not-logged", api_call=api
        )
        self.assertEqual(correlation.state, "release_main_smoke_ambiguous")
        self.assertEqual(correlation.references, ())
        self.assertTrue(correlation.warnings)
        self.assertTrue(correlation.required_transition_failed)

    def test_candidate_refetch_failure_never_creates_false_unique_link(self) -> None:
        release_run = completed_run(
            run_attempt=1,
            name="release",
            path=".github/workflows/release.yml",
            event="push",
            head_branch=RELEASE_TAG,
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:30:00Z",
        )
        first_run_id = 333333333
        failed_run_id = 333333334
        query = workflow_search_path(
            "smoke.yml",
            f"branch=main&event=push&status=success&head_sha={MERGE_SHA}",
        )
        api, _ = mapped_api(
            {
                query: {
                    "workflow_runs": [
                        {"id": first_run_id, "run_attempt": 1},
                        {"id": failed_run_id, "run_attempt": 1},
                    ]
                },
                f"/repos/{REPOSITORY}/actions/runs/{first_run_id}/attempts/1": completed_run(
                    id=first_run_id,
                    run_attempt=1,
                    event="push",
                    head_branch="main",
                    head_sha=MERGE_SHA,
                    pull_requests=[],
                    created_at="2026-09-02T11:00:00Z",
                    updated_at="2026-09-02T11:20:00Z",
                ),
                f"/repos/{REPOSITORY}/actions/runs/{failed_run_id}/attempts/1": exporter.ExportError(
                    "temporary point-lookup failure"
                ),
            }
        )

        correlation = exporter.resolve_run_correlation(
            REPOSITORY, release_run, token="not-logged", api_call=api
        )

        self.assertEqual(correlation.state, "lookup_error")
        self.assertEqual(correlation.references, ())
        self.assertTrue(correlation.required_transition_failed)

    def test_release_publication_is_deterministic_and_links_release_run(self) -> None:
        release_run_id = 444444444
        release_run = completed_run(
            id=release_run_id,
            run_attempt=1,
            name=f"Release {RELEASE_TAG}",
            path=".github/workflows/release.yml",
            event="push",
            head_branch=RELEASE_TAG,
            head_sha=MERGE_SHA,
            pull_requests=[],
            created_at="2026-09-02T11:30:00Z",
            run_started_at="2026-09-02T11:30:00Z",
            updated_at="2026-09-02T11:50:00Z",
        )
        api, _ = mapped_api(
            {
                f"/repos/{REPOSITORY}/releases/{RELEASE_ID}": release_record(),
                f"/repos/{REPOSITORY}/commits/{RELEASE_TAG}": {"sha": MERGE_SHA},
                workflow_search_path(
                    "release.yml",
                    f"branch={RELEASE_TAG}&event=push&status=success&head_sha={MERGE_SHA}",
                ): {
                    "workflow_runs": [{"id": release_run_id, "run_attempt": 1}]
                },
                f"/repos/{REPOSITORY}/actions/runs/{release_run_id}/attempts/1": release_run,
            }
        )
        release, correlation = exporter.resolve_release_publication(
            REPOSITORY,
            RELEASE_ID,
            RELEASE_TAG,
            token="not-logged",
            api_call=api,
        )
        payload, lifecycle = exporter.build_release_published_payload(
            REPOSITORY, release, correlation
        )
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(lifecycle, f"cvk-release-{RELEASE_TAG}")
        self.assertEqual(root["traceId"], cicd_otel.github_release_trace_id(RELEASE_ID))
        self.assertEqual(
            root["spanId"], cicd_otel.github_release_published_span_id(RELEASE_ID)
        )
        self.assertEqual(root["name"], "github.release.published")
        self.assertEqual(root["kind"], 2)
        self.assertEqual(
            link_attributes(root["links"][0])["cvk.link.type"],
            "github.release.publish",
        )
        self.assertEqual(
            root["links"][0]["traceId"],
            cicd_otel.github_trace_id(release_run_id, 1),
        )

    def test_publication_validation_rejects_mutable_or_mismatched_release(self) -> None:
        for release in (
            release_record(immutable=False),
            release_record(target_commitish="c" * 40),
        ):
            responses: dict[str, Any] = {
                f"/repos/{REPOSITORY}/releases/{RELEASE_ID}": release,
            }
            if release["immutable"] is True:
                responses[f"/repos/{REPOSITORY}/commits/{RELEASE_TAG}"] = {
                    "sha": MERGE_SHA
                }
            api, _ = mapped_api(responses)
            with self.assertRaises(exporter.ExportError):
                exporter.load_published_release(
                    REPOSITORY,
                    token="not-logged",
                    release_id=RELEASE_ID,
                    release_tag=RELEASE_TAG,
                    api_call=api,
                )

    def test_release_docs_and_krew_link_automatically_to_publication(self) -> None:
        for name, path in exporter.PUBLICATION_DOWNSTREAM_WORKFLOWS.items():
            with self.subTest(workflow=name):
                run = completed_run(
                    run_attempt=1,
                    name=f"{name} for {RELEASE_TAG}",
                    path=path,
                    event="release",
                    head_branch=RELEASE_TAG,
                    head_sha=MERGE_SHA,
                    pull_requests=[],
                    created_at="2026-09-02T12:00:01Z",
                    run_started_at="2026-09-02T12:00:01Z",
                )
                api, _ = mapped_api(
                    {
                        f"/repos/{REPOSITORY}/releases/tags/{RELEASE_TAG}": release_record(),
                        f"/repos/{REPOSITORY}/commits/{RELEASE_TAG}": {
                            "sha": MERGE_SHA
                        },
                    }
                )
                correlation = exporter.resolve_run_correlation(
                    REPOSITORY, run, token="not-logged", api_call=api
                )
                self.assertEqual(correlation.state, "release_publication_linked")
                self.assertEqual(
                    correlation.references[0].traceparent,
                    cicd_otel.github_release_published_traceparent(RELEASE_ID),
                )
                self.assertEqual(
                    correlation.references[0].link_type,
                    "github.release.published",
                )

    def test_manual_docs_run_does_not_trust_release_title(self) -> None:
        run = completed_run(
            run_attempt=1,
            name="CI build and deploy documentation",
            path=".github/workflows/develop.yml",
            event="workflow_dispatch",
            head_branch="main",
            pull_requests=[],
            display_title=f"Documentation publish - cvk-release-{RELEASE_TAG}",
        )
        api, calls = mapped_api({})
        correlation = exporter.resolve_run_correlation(
            REPOSITORY, run, token="not-logged", api_call=api
        )
        self.assertEqual(correlation.lifecycle_id, f"cvk-run{RUN_ID}-a1")
        self.assertEqual(correlation.references, ())
        self.assertEqual(calls, [])

    def test_lookup_failure_keeps_base_trace_and_records_warning(self) -> None:
        run = completed_run(
            run_attempt=1,
            event="push",
            head_branch="main",
            head_sha=MERGE_SHA,
            pull_requests=[],
        )

        def failing_api(_path: str) -> Any:
            raise exporter.ExportError("temporary API failure")

        correlation = exporter.resolve_run_correlation(
            REPOSITORY, run, token="not-logged", api_call=failing_api
        )
        self.assertEqual(correlation.state, "lookup_error")
        self.assertEqual(correlation.lifecycle_id, f"cvk-run{RUN_ID}-a1")
        self.assertTrue(correlation.warnings)
        self.assertTrue(correlation.required_transition_failed)
        payload, _ = exporter.build_otlp_payload(REPOSITORY, run, [], correlation)
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        self.assertEqual(span_attributes(root)["cvk.correlation.state"], "lookup_error")
        self.assertTrue(
            span_attributes(root)["cvk.correlation.required_transition_failed"]
        )

    def test_reversed_step_timestamps_are_zero_duration_and_unset(self) -> None:
        jobs = completed_jobs()
        step = jobs[0]["steps"][0]
        step["started_at"] = "2026-09-02T10:02:00Z"
        step["completed_at"] = "2026-09-02T10:01:00Z"
        step["conclusion"] = "skipped"
        payload, _ = exporter.build_otlp_payload(REPOSITORY, completed_run(), jobs)
        build = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][3]
        self.assertEqual(build["startTimeUnixNano"], build["endTimeUnixNano"])
        self.assertEqual(build["status"]["code"], 0)

    def test_github_conclusions_map_to_otel_status(self) -> None:
        for conclusion in (
            "action_required",
            "cancelled",
            "failure",
            "stale",
            "startup_failure",
            "timed_out",
        ):
            with self.subTest(conclusion=conclusion):
                self.assertEqual(exporter._status(conclusion)["code"], 2)

        for conclusion in (None, "neutral", "skipped", "unknown", "future_value"):
            with self.subTest(conclusion=conclusion):
                self.assertEqual(exporter._status(conclusion)["code"], 0)

    def test_correlation_pagination_is_bounded_and_order_preserving(self) -> None:
        first = [{"id": number} for number in range(1, 101)]
        api, calls = mapped_api(
            {
                "/correlation?per_page=100&page=1": first,
                "/correlation?per_page=100&page=2": [],
            }
        )
        result = exporter._load_correlation_pages(
            "/correlation", list_field=None, api_call=api
        )
        self.assertEqual(result, first)
        self.assertEqual(
            calls,
            [
                "/correlation?per_page=100&page=1",
                "/correlation?per_page=100&page=2",
            ],
        )

    def test_untrusted_pull_request_title_cannot_spoof_lifecycle_or_link(self) -> None:
        forged_lifecycle = "cvk-pr999-h" + "f" * 40
        forged_parent = "00-" + "1" * 32 + "-" + "2" * 16 + "-01"
        run = completed_run(
            display_title=f"change - {forged_lifecycle} - {forged_parent}",
        )
        self.assertEqual(exporter.lifecycle_for_run(run), f"cvk-pr181-h{HEAD_SHA}")
        self.assertEqual(exporter.upstream_traceparents_for_run(run), [])

    def test_completed_run_is_refetched_and_allowlisted(self) -> None:
        run = completed_run()
        paths: list[str] = []

        def exact_attempt(path: str) -> dict[str, object]:
            paths.append(path)
            return run

        got = exporter.load_completed_run(
            REPOSITORY,
            RUN_ID,
            ATTEMPT,
            token="not-logged",
            api_call=exact_attempt,
        )
        self.assertEqual(got["id"], RUN_ID)
        self.assertEqual(
            paths,
            [f"/repos/{REPOSITORY}/actions/runs/{RUN_ID}/attempts/{ATTEMPT}"],
        )
        with self.assertRaisesRegex(exporter.ExportError, "attempt does not match"):
            exporter.load_completed_run(
                REPOSITORY,
                RUN_ID,
                ATTEMPT,
                token="not-logged",
                api_call=lambda path: completed_run(run_attempt=ATTEMPT + 1),
            )
        bad = completed_run(
            name="smoke", path=".github/workflows/untrusted.yml"
        )
        with self.assertRaisesRegex(exporter.ExportError, "not observed"):
            exporter.load_completed_run(
                REPOSITORY,
                RUN_ID,
                ATTEMPT,
                token="not-logged",
                api_call=lambda path: bad,
            )
        observer = completed_run(path=exporter.OBSERVER_WORKFLOW_PATH)
        with self.assertRaisesRegex(exporter.ExportError, "never observe itself"):
            exporter.load_completed_run(
                REPOSITORY,
                RUN_ID,
                ATTEMPT,
                token="not-logged",
                api_call=lambda path: observer,
            )

    def test_trusted_lab_dispatch_waits_for_completion(self) -> None:
        lab_path = exporter.OBSERVED_WORKFLOW_PATHS["Lab CI (Cat8kv)"]
        responses = [
            completed_run(
                path=lab_path,
                event="workflow_dispatch",
                head_branch="main",
                status="in_progress",
                conclusion=None,
            ),
            completed_run(
                path=lab_path,
                event="workflow_dispatch",
                head_branch="main",
            ),
        ]
        paths: list[str] = []
        sleeps: list[float] = []

        def api_call(path: str) -> dict[str, object]:
            paths.append(path)
            return responses.pop(0)

        run = exporter.load_completed_run(
            REPOSITORY,
            RUN_ID,
            ATTEMPT,
            token="not-logged",
            api_call=api_call,
            trusted_lab_dispatch=True,
            sleep=sleeps.append,
        )

        self.assertEqual(run["status"], "completed")
        self.assertEqual(len(paths), 2)
        self.assertEqual(
            set(paths),
            {f"/repos/{REPOSITORY}/actions/runs/{RUN_ID}/attempts/{ATTEMPT}"},
        )
        self.assertEqual(sleeps, [exporter.COMPLETION_POLL_INTERVAL_SECONDS])

    def test_trusted_lab_dispatch_wait_is_bounded(self) -> None:
        lab = completed_run(
            path=exporter.OBSERVED_WORKFLOW_PATHS["Lab CI (Cat9k)"],
            event="workflow_dispatch",
            head_branch="main",
            status="queued",
            conclusion=None,
        )
        api_call = mock.Mock(return_value=lab)
        sleep = mock.Mock()

        with self.assertRaisesRegex(exporter.ExportError, "did not complete"):
            exporter.load_completed_run(
                REPOSITORY,
                RUN_ID,
                ATTEMPT,
                token="not-logged",
                api_call=api_call,
                trusted_lab_dispatch=True,
                sleep=sleep,
            )

        self.assertEqual(api_call.call_count, exporter.COMPLETION_POLL_ATTEMPTS)
        self.assertEqual(
            sleep.call_count,
            exporter.COMPLETION_POLL_ATTEMPTS - 1,
        )
        sleep.assert_called_with(exporter.COMPLETION_POLL_INTERVAL_SECONDS)

    def test_trusted_lab_dispatch_rejects_wrong_provenance_before_wait(self) -> None:
        lab_path = exporter.OBSERVED_WORKFLOW_PATHS["Lab CI (Cat8kv)"]
        cases = (
            (completed_run(id=RUN_ID + 1), "ID does not match"),
            (
                completed_run(repository={"full_name": "other/repository"}),
                "repository does not match",
            ),
            (completed_run(run_attempt=ATTEMPT + 1), "attempt does not match"),
            (
                completed_run(path=exporter.OBSERVER_WORKFLOW_PATH),
                "never observe itself",
            ),
            (
                completed_run(path=".github/workflows/untrusted.yml"),
                "not observed",
            ),
            (
                completed_run(path=exporter.OBSERVED_WORKFLOW_PATHS["smoke"]),
                "main-branch Cat8/Cat9",
            ),
            (
                completed_run(
                    path=lab_path,
                    event="pull_request",
                    head_branch="main",
                ),
                "main-branch Cat8/Cat9",
            ),
            (
                completed_run(
                    path=lab_path,
                    event="workflow_dispatch",
                    head_branch="feature/untrusted",
                ),
                "main-branch Cat8/Cat9",
            ),
        )
        for run, message in cases:
            with self.subTest(message=message):
                sleep = mock.Mock()
                with self.assertRaisesRegex(exporter.ExportError, message):
                    exporter.load_completed_run(
                        REPOSITORY,
                        RUN_ID,
                        ATTEMPT,
                        token="not-logged",
                        api_call=lambda path, run=run: run,
                        trusted_lab_dispatch=True,
                        sleep=sleep,
                    )
                sleep.assert_not_called()

    def test_trusted_lab_dispatch_rejects_unexpected_status_before_wait(self) -> None:
        lab = completed_run(
            path=exporter.OBSERVED_WORKFLOW_PATHS["Lab CI (Cat8kv)"],
            event="workflow_dispatch",
            head_branch="main",
            status="stale",
        )
        sleep = mock.Mock()
        with self.assertRaisesRegex(exporter.ExportError, "unexpected.*status"):
            exporter.load_completed_run(
                REPOSITORY,
                RUN_ID,
                ATTEMPT,
                token="not-logged",
                api_call=lambda path: lab,
                trusted_lab_dispatch=True,
                sleep=sleep,
            )
        sleep.assert_not_called()

    def test_observer_workflow_has_exclusive_explicit_lab_dispatch(self) -> None:
        workflow = (SCRIPT_DIR.parent / "workflows" / "cicd-otel-export.yaml").read_text()
        workflow_run_trigger = workflow.split("  workflow_dispatch:", 1)[0]
        self.assertNotIn("- Lab CI (Cat8kv)", workflow_run_trigger)
        self.assertNotIn("- Lab CI (Cat9k)", workflow_run_trigger)
        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("observed_run_id:", workflow)
        self.assertIn("observed_run_attempt:", workflow)
        self.assertEqual(workflow.count("required: true"), 2)
        self.assertIn("github.ref == 'refs/heads/main'", workflow)
        self.assertIn("github.actor == 'github-actions[bot]'", workflow)
        self.assertIn(
            "OBSERVED_RUN_ID: ${{ github.event.workflow_run.id || inputs.observed_run_id }}",
            workflow,
        )
        self.assertIn(
            "OBSERVED_RUN_ATTEMPT: ${{ github.event.workflow_run.run_attempt || inputs.observed_run_attempt }}",
            workflow,
        )
        self.assertIn('if [[ "$GITHUB_EVENT_NAME" == "workflow_dispatch" ]]', workflow)
        self.assertIn("observer_mode+=(--trusted-lab-dispatch)", workflow)
        self.assertIn('--run-attempt "$OBSERVED_RUN_ATTEMPT"', workflow)

    def test_release_and_pr_metadata_use_compatible_api_versions(self) -> None:
        requests: list[object] = []

        def opener(request: object, *, timeout: int) -> Response:
            requests.append(request)
            return Response()

        exporter.github_api(
            f"/repos/{REPOSITORY}/pulls/181",
            token="not-logged",
            opener=opener,
        )
        exporter.github_api(
            f"/repos/{REPOSITORY}/releases/{RELEASE_ID}",
            token="not-logged",
            opener=opener,
        )
        self.assertEqual(
            requests[0].get_header("X-github-api-version"),
            exporter.GITHUB_METADATA_API_VERSION,
        )
        self.assertEqual(
            requests[1].get_header("X-github-api-version"),
            exporter.GITHUB_RELEASE_API_VERSION,
        )

    def test_duplicate_step_names_are_rejected(self) -> None:
        jobs = completed_jobs()
        jobs[0]["steps"].append(dict(jobs[0]["steps"][0]))
        with self.assertRaisesRegex(exporter.ExportError, "duplicate step names"):
            exporter.build_otlp_payload(REPOSITORY, completed_run(), jobs)

    def test_otlp_export_is_loopback_only_and_bounded(self) -> None:
        requests: list[object] = []

        def opener(request: object, *, timeout: int) -> Response:
            requests.append((request, timeout))
            return Response()

        payload, _ = exporter.build_otlp_payload(REPOSITORY, completed_run(), [])
        exporter.export_otlp(payload, opener=opener)
        request, timeout = requests[0]
        self.assertEqual(request.full_url, exporter.OTLP_ENDPOINT)
        self.assertEqual(timeout, exporter.OTLP_TIMEOUT_SECONDS)
        self.assertEqual(json.loads(request.data), payload)
        with self.assertRaisesRegex(exporter.ExportError, "loopback"):
            exporter.export_otlp(payload, endpoint="https://collector.example", opener=opener)

        malformed_response = mock.Mock()
        malformed_response.headers = {}
        malformed_response.read.return_value = "not-bytes"
        with self.assertRaisesRegex(exporter.ExportError, "was not bytes"):
            exporter._read_bounded(malformed_response)

    def test_otlp_partial_success_with_rejected_spans_fails(self) -> None:
        payload, _ = exporter.build_otlp_payload(REPOSITORY, completed_run(), [])
        acknowledgement = json.dumps(
            {
                "partialSuccess": {
                    "rejectedSpans": "1",
                    "errorMessage": "one span rejected",
                }
            }
        ).encode()

        with self.assertRaisesRegex(exporter.ExportError, "rejected 1 spans"):
            exporter.export_otlp(
                payload,
                opener=lambda request, timeout: Response(acknowledgement),
            )

    def test_main_exports_base_trace_before_required_transition_failure(self) -> None:
        run = completed_run(
            run_attempt=1,
            event="push",
            head_branch="main",
            head_sha=MERGE_SHA,
            pull_requests=[],
        )
        failed = exporter.RunCorrelation(
            lifecycle_id=exporter.run_lifecycle_id(RUN_ID, 1),
            state="lookup_error",
            warnings=("temporary lookup failure",),
            required_transition_failed=True,
        )
        exported = mock.Mock()
        with (
            mock.patch.object(
                sys,
                "argv",
                [
                    str(MODULE_PATH),
                    "--repository",
                    REPOSITORY,
                    "--run-id",
                    str(RUN_ID),
                    "--run-attempt",
                    "1",
                ],
            ),
            mock.patch.dict(exporter.os.environ, {"GH_TOKEN": "not-logged"}),
            mock.patch.object(exporter, "load_completed_run", return_value=run),
            mock.patch.object(exporter, "load_jobs", return_value=[]),
            mock.patch.object(exporter, "resolve_run_correlation", return_value=failed),
            mock.patch.object(exporter, "export_otlp", new=exported),
            mock.patch("sys.stdout", new=io.StringIO()),
            mock.patch("sys.stderr", new=io.StringIO()),
        ):
            status = exporter.main()

        self.assertEqual(status, 1)
        exported.assert_called_once()

    def test_main_exports_base_trace_before_invalid_zero_id_carrier_failure(self) -> None:
        lifecycle = f"cvk-pr181-h{HEAD_SHA}"
        invalid_traceparent = f"00-{'0' * 32}-{'0' * 16}-01"
        run = completed_run(
            run_attempt=1,
            name="Lab CI (Cat8kv)",
            path=".github/workflows/lab-ci-cat8kv.yaml",
            event="workflow_dispatch",
            display_title=f"Lab CI Cat8kv - {lifecycle} - {invalid_traceparent}",
            pull_requests=[],
        )
        exported = mock.Mock()
        with (
            mock.patch.object(
                sys,
                "argv",
                [
                    str(MODULE_PATH),
                    "--repository",
                    REPOSITORY,
                    "--run-id",
                    str(RUN_ID),
                    "--run-attempt",
                    "1",
                ],
            ),
            mock.patch.dict(exporter.os.environ, {"GH_TOKEN": "not-logged"}),
            mock.patch.object(exporter, "load_completed_run", return_value=run),
            mock.patch.object(exporter, "load_jobs", return_value=[]),
            mock.patch.object(exporter, "export_otlp", new=exported),
            mock.patch("sys.stdout", new=io.StringIO()),
            mock.patch("sys.stderr", new=io.StringIO()),
        ):
            status = exporter.main()

        self.assertEqual(status, 1)
        exported.assert_called_once()
        payload = exported.call_args.args[0]
        root = payload["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
        attributes = span_attributes(root)
        self.assertEqual(attributes["cvk.correlation.state"], "base_metadata_invalid")
        self.assertTrue(attributes["cvk.correlation.required_transition_failed"])
        self.assertEqual(root.get("links", []), [])


if __name__ == "__main__":
    unittest.main()
