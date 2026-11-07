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

import base64
import hashlib
import io
import json
import tarfile
import unittest
from unittest.mock import patch

import generate_mermaid_runtime_licenses as generator
from generate_mkdocs_licenses import verify_mermaid_runtime_notice


def tarball(files: list[tuple[str, bytes]]) -> bytes:
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for path, content in files:
            member = tarfile.TarInfo(path)
            member.size = len(content)
            archive.addfile(member, io.BytesIO(content))
    return buffer.getvalue()


class MermaidRuntimeLicenseTests(unittest.TestCase):
    def test_embedded_source_requires_exact_content_and_unique_path(self) -> None:
        source_map = {"sources": ["a.js"], "sourcesContent": ["original"]}
        digest = generator.sha256(b"original")
        self.assertEqual(generator.pinned_source(source_map, "a.js", digest), "original")
        for invalid in (
            {"sources": ["a.js"], "sourcesContent": ["modified"]},
            {"sources": ["a.js"], "sourcesContent": [None]},
            {"sources": ["a.js"], "sourcesContent": []},
            {"sources": ["a.js", "a.js"], "sourcesContent": ["original"] * 2},
        ):
            with self.subTest(source_map=invalid), self.assertRaises(generator.MermaidAuditError):
                generator.pinned_source(invalid, "a.js", digest)

    def test_path_browserify_preserves_original_comment_and_rejects_changed_code(self) -> None:
        header = (
            "// Copyright Joyent, Inc. and other Node contributors.\n"
            "// Original permission text retained verbatim.\n"
            "// USE OR OTHER DEALINGS IN THE SOFTWARE."
        )
        source = header + "\n\n'use strict';\nmodule.exports = {};\n"
        source_map = {"sources": [generator.PATH_BROWSERIFY_SOURCE], "sourcesContent": [source]}
        with patch.object(generator, "PATH_BROWSERIFY_SHA256", generator.sha256(source.encode())):
            notice = generator.path_browserify_notice(source_map)
            self.assertEqual(notice, f"Source: {generator.PATH_BROWSERIFY_SOURCE}\n\n{header}")
            source_map["sourcesContent"] = [source + "// changed code\n"]
            with self.assertRaisesRegex(generator.MermaidAuditError, "source changed"):
                generator.path_browserify_notice(source_map)

    def test_web_worker_notice_requires_audited_bytes_and_expected_module_boundary(self) -> None:
        header = "/**\n * Copyright 2020 Google LLC\n * Original notice.\n */"
        bundle = (
            ",4:[function(require,module,exports){\n" + header
            + "\nmodule.exports = Worker;\n},{}]},{},[3])(3)\n});\n"
        ).encode()
        with patch.object(generator, "ELK_BUNDLE_SHA256", generator.sha256(bundle)):
            self.assertEqual(
                generator.web_worker_notice(bundle),
                "web-worker browser shim embedded in elkjs 0.9.3\n\n" + header,
            )
            with self.assertRaisesRegex(generator.MermaidAuditError, "bundle changed"):
                generator.web_worker_notice(bundle + b"changed")
        broken = bundle.replace(b"module.exports = Worker;", b"module.exports = SomethingElse;")
        with patch.object(generator, "ELK_BUNDLE_SHA256", generator.sha256(broken)):
            with self.assertRaisesRegex(generator.MermaidAuditError, "cannot extract"):
                generator.web_worker_notice(broken)

    def test_tarball_extraction_rejects_missing_duplicate_and_nonregular_sources(self) -> None:
        for files in ([], [("a", b"one"), ("a", b"two")]):
            with tarfile.open(fileobj=io.BytesIO(tarball(files)), mode="r:gz") as archive:
                with self.assertRaises(generator.MermaidAuditError):
                    generator.archive_bytes(archive, "a")
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
            member = tarfile.TarInfo("a")
            member.type = tarfile.SYMTYPE
            member.linkname = "elsewhere"
            archive.addfile(member)
        with tarfile.open(fileobj=io.BytesIO(buffer.getvalue()), mode="r:gz") as archive:
            with self.assertRaises(generator.MermaidAuditError):
                generator.archive_bytes(archive, "a")

    def test_source_provenance_and_parser_notice_cannot_disappear(self) -> None:
        with tarfile.open(fileobj=io.BytesIO(tarball([])), mode="r:gz") as archive:
            with self.assertRaisesRegex(generator.MermaidAuditError, "provenance changed"):
                generator.embedded_notices(generator.ELK_PAIR, archive, {"gitHead": "changed"})
            with self.assertRaisesRegex(generator.MermaidAuditError, "lacks one audited"):
                generator.embedded_notices(("@mermaid-js/parser", "2.0.1"), archive, {})

    def test_embedded_notices_do_not_replace_missing_package_license(self) -> None:
        manifest = {"name": "@mermaid-js/parser", "version": "2.0.1"}
        content = tarball([("package/package.json", json.dumps(manifest).encode())])
        metadata = {**manifest, "dist": {
            "integrity": "sha512-" + base64.b64encode(hashlib.sha512(content).digest()).decode(),
            "tarball": "https://registry.npmjs.org/example.tgz",
        }}
        with patch.object(generator.urllib.request, "urlopen", side_effect=[
            io.BytesIO(json.dumps(metadata).encode()), io.BytesIO(content),
        ]), patch.object(generator, "embedded_notices") as extra:
            with self.assertRaisesRegex(generator.MermaidAuditError, "no license text"):
                generator.fetch_package(("@mermaid-js/parser", "2.0.1"))
            extra.assert_not_called()

    def test_header_uses_actual_versions_and_synthetic_notice_sections_verify(self) -> None:
        packages = []
        for name, version in (("mermaid", "12.2.0"), ("@mermaid-js/parser", "2.0.1")):
            path, notice = "source.js (embedded source notice)", "Original notice."
            entry = {
                "name": name, "version": version, "license": "MIT", "integrity": "sha512-test",
                "licenseFiles": [{"path": path, "sha256": generator.sha256(notice.encode())}],
            }
            packages.append((entry, [(path, notice)]))
        rendered = generator.render_licenses(packages)
        self.assertIn("hash-pinned Mermaid 12.2.0", rendered)
        self.assertIn("prebuilt @mermaid-js/parser 2.0.1", rendered)
        self.assertEqual(
            verify_mermaid_runtime_notice(rendered, [entry for entry, _ in packages]),
            {("mermaid", "12.2.0"), ("@mermaid-js/parser", "2.0.1")},
        )
        for invalid in (packages[:1], packages + packages[:1]):
            with self.assertRaises(generator.MermaidAuditError):
                generator.render_licenses(invalid)


if __name__ == "__main__":
    unittest.main()
