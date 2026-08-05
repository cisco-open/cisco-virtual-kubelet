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

import hashlib
import importlib.util
import pathlib
import sys
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("render_krew_manifest.py")
SPEC = importlib.util.spec_from_file_location("render_krew_manifest", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
renderer = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = renderer
SPEC.loader.exec_module(renderer)


RELEASE_TAG = "v2026.9.0"


class RenderKrewManifestTests(unittest.TestCase):
    def write_checksums(
        self,
        root: pathlib.Path,
        *,
        include_sboms: bool = False,
        omit: str = "",
        extra: str = "",
    ) -> tuple[pathlib.Path, dict[renderer.Target, str]]:
        entries: dict[str, str] = {}
        target_digests: dict[renderer.Target, str] = {}
        for index, target in enumerate(renderer.TARGETS, 1):
            digest = f"{index:064x}"
            target_digests[target] = digest
            entries[renderer.archive_name(RELEASE_TAG, target)] = digest
            if include_sboms:
                entries[renderer.sbom_name(RELEASE_TAG, target)] = f"{index + 10:064x}"
        entries.pop(omit, None)
        if extra:
            entries[extra] = "f" * 64
        path = root / "checksums.txt"
        path.write_text(
            "".join(f"{entries[name]}  {name}\n" for name in sorted(entries)),
            encoding="utf-8",
        )
        return path, target_digests

    def test_strict_calver_renders_unchanged_version_and_asset_urls(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            checksum_path, expected = self.write_checksums(root)
            checksums = renderer.read_release_checksums(checksum_path, RELEASE_TAG)
            rendered = renderer.render_manifest(
                renderer.DEFAULT_TEMPLATE, RELEASE_TAG, checksums
            )
            self.assertIn(f'version: "{RELEASE_TAG}"', rendered)
            self.assertEqual(rendered.count("    bin: kubectl-ciscovk\n"), 4)
            self.assertNotIn("windows", rendered.lower())
            for target in renderer.TARGETS:
                name = renderer.archive_name(RELEASE_TAG, target)
                self.assertIn(f"/{RELEASE_TAG}/{name}", rendered)
                self.assertIn(f'sha256: "{expected[target]}"', rendered)

    def test_rendering_is_byte_deterministic(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            checksum_path, _ = self.write_checksums(root, include_sboms=True)
            checksums = renderer.read_release_checksums(checksum_path, RELEASE_TAG)
            first = renderer.render_manifest(
                renderer.DEFAULT_TEMPLATE, RELEASE_TAG, checksums
            )
            second = renderer.render_manifest(
                renderer.DEFAULT_TEMPLATE, RELEASE_TAG, checksums
            )
            self.assertEqual(first.encode(), second.encode())

    def test_checksum_contract_rejects_missing_unknown_and_partial_sboms(self) -> None:
        missing = renderer.archive_name(RELEASE_TAG, renderer.TARGETS[0])
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            path, _ = self.write_checksums(root, omit=missing)
            with self.assertRaisesRegex(renderer.KrewManifestError, "missing archives"):
                renderer.read_release_checksums(path, RELEASE_TAG)

            path, _ = self.write_checksums(root, extra="unexpected.tar.gz")
            with self.assertRaisesRegex(renderer.KrewManifestError, "unexpected entries"):
                renderer.read_release_checksums(path, RELEASE_TAG)

            path, _ = self.write_checksums(root)
            lines = path.read_text(encoding="utf-8").splitlines()
            lines.append(
                f"{'a' * 64}  "
                f"{renderer.sbom_name(RELEASE_TAG, renderer.TARGETS[0])}"
            )
            path.write_text(
                "\n".join(sorted(lines, key=lambda line: line.split("  ", 1)[1]))
                + "\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(renderer.KrewManifestError, "all present or all absent"):
                renderer.read_release_checksums(path, RELEASE_TAG)

    def test_checksum_contract_rejects_duplicate_malformed_and_unsorted_lines(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            path, _ = self.write_checksums(root)
            first_line = path.read_text(encoding="utf-8").splitlines()[0]
            with path.open("a", encoding="utf-8") as stream:
                stream.write(first_line + "\n")
            with self.assertRaisesRegex(renderer.KrewManifestError, "duplicate"):
                renderer.read_release_checksums(path, RELEASE_TAG)

            path.write_text("ABC  invalid\n", encoding="utf-8")
            with self.assertRaisesRegex(renderer.KrewManifestError, "invalid checksum line"):
                renderer.read_release_checksums(path, RELEASE_TAG)

            path, _ = self.write_checksums(root)
            lines = path.read_text(encoding="utf-8").splitlines()
            path.write_text("\n".join(reversed(lines)) + "\n", encoding="utf-8")
            with self.assertRaisesRegex(renderer.KrewManifestError, "must be sorted"):
                renderer.read_release_checksums(path, RELEASE_TAG)

    def test_archive_verification_requires_exact_files_and_hashes(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            archives = root / "archives"
            archives.mkdir()
            expected: dict[renderer.Target, str] = {}
            for target in renderer.TARGETS:
                path = archives / renderer.archive_name(RELEASE_TAG, target)
                path.write_bytes(f"archive-{target.key}".encode())
                expected[target] = hashlib.sha256(path.read_bytes()).hexdigest()
            renderer.verify_archives(archives, RELEASE_TAG, expected)

            first = archives / renderer.archive_name(RELEASE_TAG, renderer.TARGETS[0])
            first.write_bytes(b"tampered")
            with self.assertRaisesRegex(renderer.KrewManifestError, "checksum mismatch"):
                renderer.verify_archives(archives, RELEASE_TAG, expected)

            first.write_bytes(b"archive-darwin_amd64")
            (archives / "extra").write_bytes(b"no")
            with self.assertRaisesRegex(renderer.KrewManifestError, "exactly the four"):
                renderer.verify_archives(archives, RELEASE_TAG, expected)

    def test_unsafe_or_non_calendar_release_tags_are_rejected(self) -> None:
        for tag in (
            "2026.09.0",
            "v2026.09.0",
            "v2026.9.01",
            "v2026.00.0",
            "v2026.13.0",
            "v2026.9.0-rc.1",
            "v2026.9.0/../../bad",
            "v026.9.0",
        ):
            with self.subTest(tag=tag):
                with self.assertRaises(renderer.KrewManifestError):
                    renderer.normalize_krew_version(tag)

    def test_output_filename_must_match_plugin_name(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            with self.assertRaisesRegex(renderer.KrewManifestError, "cisco-vk.yaml"):
                renderer.write_manifest(root / "wrong.yaml", "manifest\n")
            output = root / "cisco-vk.yaml"
            renderer.write_manifest(output, "manifest\n")
            self.assertEqual(output.read_text(encoding="utf-8"), "manifest\n")

    def test_post_publication_workflow_is_read_only_and_fail_closed(self) -> None:
        workflow = (
            renderer.REPO_ROOT / ".github" / "workflows" / "krew-index.yml"
        ).read_text(encoding="utf-8")
        self.assertIn("release:\n    types: [published]", workflow)
        self.assertIn("and .immutable == true", workflow)
        self.assertIn('test "$total_asset_count" -eq 16', workflow)
        self.assertIn('select(.name == "sbom.cdx.json")', workflow)
        self.assertIn("contents: read", workflow)
        self.assertNotIn("contents: write", workflow)
        self.assertNotIn("pull_request_target", workflow)
        self.assertNotIn("id-token: write", workflow)
        self.assertNotIn("gh release upload", workflow)
        self.assertNotIn("gh release edit", workflow)
        self.assertNotIn("${{ secrets.", workflow)
        self.assertIn("GITHUB_TOKEN: ''", workflow)
        self.assertIn(
            "3748407285b4cf866e9d4625e376aca927aa3f0b30f30ede83cc33a11566f28b",
            workflow,
        )


if __name__ == "__main__":
    unittest.main()
