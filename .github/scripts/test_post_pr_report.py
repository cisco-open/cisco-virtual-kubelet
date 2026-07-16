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

import http.client
import importlib.util
import io
import json
import pathlib
import urllib.error
import unittest
import zipfile
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("post-pr-report.py")
SPEC = importlib.util.spec_from_file_location("post_pr_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class ReporterTests(unittest.TestCase):
    def test_only_native_lab_contexts_are_reported(self) -> None:
        contexts = [entry["context"] for entry in reporter.CONTEXTS]
        self.assertEqual(
            contexts,
            ["lab-ci-next / cat8kv", "lab-ci-next / cat9k"],
        )
        self.assertNotIn("lab-ci / unit-tests", contexts)

    def test_parse_sanitized_evidence_keeps_only_public_schema(self) -> None:
        content = json.dumps(
            {
                "schema": "cvk-lab-evidence/v1",
                "workflow": "cat8kv-01234567-abcd",
                "phase": "Succeeded",
                "unexpected_secret": "must-not-survive",
                "scenarios": [
                    {
                        "crd": "CiscoDevice",
                        "name": "primary",
                        "template": "scenario-primary",
                        "phase": "Succeeded",
                        "started_at": "2026-07-15T10:00:00Z",
                        "finished_at": "2026-07-15T10:01:05Z",
                        "raw_logs": "must-not-survive",
                    }
                ],
            }
        ).encode()

        parsed = reporter.parse_sanitized_evidence(content)

        self.assertNotIn("unexpected_secret", parsed)
        self.assertNotIn("raw_logs", parsed["scenarios"][0])
        self.assertEqual(parsed["scenarios"][0]["name"], "primary")

    def test_malformed_evidence_timestamps_do_not_block_rendering(self) -> None:
        content = json.dumps(
            {
                "schema": "cvk-lab-evidence/v1",
                "scenarios": [
                    {
                        "name": "primary",
                        "started_at": "not-a-timestamp",
                        "finished_at": "also-not-a-timestamp",
                    }
                ],
            }
        ).encode()

        lines = reporter.render_scenarios(reporter.parse_sanitized_evidence(content))

        self.assertTrue(any("primary" in line for line in lines))
        self.assertTrue(any(line.endswith("| — |") for line in lines))

    def test_fetch_job_steps_selects_named_lab_job(self) -> None:
        jobs = {
            "jobs": [
                {"name": "prepare", "steps": [{"name": "wrong"}]},
                {"name": "cat8kv", "steps": [{"name": "right"}]},
                {"name": "report", "steps": [{"name": "wrong-again"}]},
            ]
        }
        with mock.patch.object(reporter, "api", return_value=jobs):
            steps = reporter.fetch_job_steps("cisco-open/repo", 123, "cat8kv")
        self.assertEqual(steps, [{"name": "right"}])

    def test_artifact_redirect_does_not_forward_authorization(self) -> None:
        location = "https://example.invalid/signed-artifact.zip?sig=redacted"
        headers = {"Location": location}
        redirect = urllib.error.HTTPError(
            "https://api.github.com/artifact",
            302,
            "Found",
            headers,
            None,
        )
        opener = mock.Mock()
        opener.open.side_effect = redirect

        with (
            mock.patch.object(reporter.urllib.request, "build_opener", return_value=opener),
            mock.patch.object(reporter, "http_get_bytes", return_value=b"zip") as get_bytes,
        ):
            result = reporter.download_artifact_archive(
                "https://api.github.com/repos/o/r/actions/artifacts/1/zip",
                "github-token",
            )

        self.assertEqual(result, b"zip")
        initial_request = opener.open.call_args.args[0]
        self.assertEqual(initial_request.get_header("Authorization"), "Bearer github-token")
        get_bytes.assert_called_once_with(location)

    def test_fetch_artifact_extracts_sanitized_file(self) -> None:
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            archive.writestr("argo-evidence.json", b'{"schema":"cvk-lab-evidence/v1"}')
        metadata = {
            "artifacts": [
                {
                    "id": 8,
                    "name": "argo-evidence-cat8kv",
                    "expired": False,
                    "archive_download_url": "https://api.github.com/artifact",
                }
            ]
        }
        with (
            mock.patch.object(reporter, "api", return_value=metadata),
            mock.patch.object(
                reporter,
                "download_artifact_archive",
                return_value=buffer.getvalue(),
            ),
            mock.patch.dict(reporter.os.environ, {"GH_TOKEN": "token"}),
        ):
            content = reporter.fetch_artifact_file(
                "cisco-open/repo",
                123,
                "argo-evidence-cat8kv",
            )
        self.assertEqual(content, b'{"schema":"cvk-lab-evidence/v1"}')

    def test_fetch_artifact_http_error_is_nonfatal_and_sanitized(self) -> None:
        metadata = {
            "artifacts": [
                {
                    "id": 8,
                    "name": "argo-evidence-cat8kv",
                    "expired": False,
                    "archive_download_url": "https://api.github.com/artifact",
                }
            ]
        }
        download_error = urllib.error.HTTPError(
            "https://signed.invalid/archive.zip?sig=must-not-appear",
            403,
            "Forbidden",
            {},
            None,
        )
        stderr = io.StringIO()
        with (
            mock.patch.object(reporter, "api", return_value=metadata),
            mock.patch.object(
                reporter,
                "download_artifact_archive",
                side_effect=download_error,
            ),
            mock.patch.dict(reporter.os.environ, {"GH_TOKEN": "token"}),
            mock.patch("sys.stderr", stderr),
        ):
            content = reporter.fetch_artifact_file(
                "cisco-open/repo",
                123,
                "argo-evidence-cat8kv",
            )

        self.assertIsNone(content)
        self.assertIn("artifact storage HTTP 403", stderr.getvalue())
        self.assertNotIn("must-not-appear", stderr.getvalue())
        self.assertNotIn("signed.invalid", stderr.getvalue())

    def test_invalid_artifact_redirect_is_nonfatal_and_sanitized(self) -> None:
        metadata = {
            "artifacts": [
                {
                    "id": 8,
                    "name": "argo-evidence-cat8kv",
                    "expired": False,
                    "archive_download_url": "https://api.github.com/artifact",
                }
            ]
        }
        stderr = io.StringIO()
        with (
            mock.patch.object(reporter, "api", return_value=metadata),
            mock.patch.object(
                reporter,
                "download_artifact_archive",
                side_effect=ValueError("signed URL secret must-not-appear"),
            ),
            mock.patch.dict(reporter.os.environ, {"GH_TOKEN": "token"}),
            mock.patch("sys.stderr", stderr),
        ):
            content = reporter.fetch_artifact_file(
                "cisco-open/repo",
                123,
                "argo-evidence-cat8kv",
            )

        self.assertIsNone(content)
        self.assertIn("invalid artifact response", stderr.getvalue())
        self.assertNotIn("must-not-appear", stderr.getvalue())

    def test_truncated_artifact_download_is_nonfatal_and_sanitized(self) -> None:
        metadata = {
            "artifacts": [
                {
                    "id": 8,
                    "name": "argo-evidence-cat8kv",
                    "expired": False,
                    "archive_download_url": "https://api.github.com/artifact",
                }
            ]
        }
        stderr = io.StringIO()
        with (
            mock.patch.object(reporter, "api", return_value=metadata),
            mock.patch.object(
                reporter,
                "download_artifact_archive",
                side_effect=http.client.IncompleteRead(b"partial", 100),
            ),
            mock.patch.dict(reporter.os.environ, {"GH_TOKEN": "token"}),
            mock.patch("sys.stderr", stderr),
        ):
            content = reporter.fetch_artifact_file(
                "cisco-open/repo",
                123,
                "argo-evidence-cat8kv",
            )

        self.assertIsNone(content)
        self.assertIn("artifact transport failed", stderr.getvalue())
        self.assertNotIn("partial", stderr.getvalue())

    def test_artifact_api_error_keeps_sanitized_diagnostics(self) -> None:
        stderr = io.StringIO()
        error = reporter.GitHubAPIError(
            403,
            "GET",
            "/repos/cisco-open/repo/actions/runs/123/artifacts",
            "Resource not accessible by integration",
            "SAFE-REQUEST-ID",
        )
        with (
            mock.patch.object(reporter, "api", side_effect=error),
            mock.patch("sys.stderr", stderr),
        ):
            content = reporter.fetch_artifact_file(
                "cisco-open/repo",
                123,
                "argo-evidence-cat8kv",
            )

        self.assertIsNone(content)
        self.assertIn("Resource not accessible by integration", stderr.getvalue())
        self.assertIn("SAFE-REQUEST-ID", stderr.getvalue())
        self.assertNotIn("https://", stderr.getvalue())

    def test_api_error_keeps_only_sanitized_diagnostics(self) -> None:
        response = io.BytesIO(b'{"message":"Resource not accessible by integration"}')
        error = urllib.error.HTTPError(
            "https://api.github.com/repos/cisco-open/repo/issues/150/comments",
            403,
            "Forbidden",
            {"X-GitHub-Request-Id": "SAFE-REQUEST-ID"},
            response,
        )
        with mock.patch.object(reporter.urllib.request, "urlopen", side_effect=error):
            with self.assertRaises(reporter.GitHubAPIError) as raised:
                reporter.api(
                    "/repos/cisco-open/repo/issues/150/comments?secret=must-not-appear",
                    method="POST",
                    body={"body": "report"},
                    token="must-not-appear",
                )

        self.assertEqual(raised.exception.status, 403)
        self.assertEqual(raised.exception.method, "POST")
        self.assertEqual(
            raised.exception.path,
            "/repos/cisco-open/repo/issues/150/comments",
        )
        self.assertEqual(
            raised.exception.message,
            "Resource not accessible by integration",
        )
        self.assertEqual(raised.exception.request_id, "SAFE-REQUEST-ID")
        self.assertNotIn("must-not-appear", str(raised.exception))

    def test_report_uses_distinct_marker_and_no_unit_row(self) -> None:
        head_sha = "a" * 40
        with mock.patch.object(reporter, "fetch_run_duration", return_value="1m05s"):
            body = reporter.render_report("cisco-open/repo", head_sha, {})
        self.assertIn("<!-- lab-ci-next-report -->", body)
        self.assertIn("lab workflows test its prospective merge", body)
        self.assertNotIn("Unit tests", body)
        self.assertNotIn("<!-- lab-ci-report:", body)

    def test_user_preseeded_marker_is_not_patched(self) -> None:
        user_comment = {
            "id": 41,
            "body": reporter.COMMENT_MARKER,
            "user": {"login": "untrusted-user"},
        }
        calls: list[tuple[str, str, dict[str, str] | None]] = []

        def fake_api(
            path: str,
            method: str = "GET",
            body: dict[str, str] | None = None,
            token: str | None = None,
        ) -> object:
            del token
            calls.append((path, method, body))
            if method == "GET":
                return [user_comment]
            return {}

        with mock.patch.object(reporter, "api", side_effect=fake_api):
            reporter.upsert_comment("cisco-open/repo", "147", "new report")

        self.assertEqual(len(calls), 2)
        self.assertEqual(
            calls[1],
            (
                "/repos/cisco-open/repo/issues/147/comments",
                "POST",
                {"body": "new report"},
            ),
        )
        self.assertFalse(any("issues/comments/41" in call[0] for call in calls))

    def test_validate_inputs_rejects_non_numeric_pr(self) -> None:
        with self.assertRaises(ValueError):
            reporter.validate_inputs("cisco-open/repo", "a" * 40, "1; rm -rf")

    def test_stale_pr_head_is_detected(self) -> None:
        with mock.patch.object(
            reporter,
            "api",
            return_value={"state": "open", "head": {"sha": "b" * 40}},
        ):
            current = reporter.pr_still_has_head(
                "cisco-open/repo",
                "147",
                "a" * 40,
            )
        self.assertFalse(current)


if __name__ == "__main__":
    unittest.main()
