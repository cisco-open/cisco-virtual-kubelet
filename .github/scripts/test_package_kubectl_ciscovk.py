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
import json
import pathlib
import struct
import sys
import tarfile
import tempfile
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("package_kubectl_ciscovk.py")
SPEC = importlib.util.spec_from_file_location("package_kubectl_ciscovk", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
packager = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = packager
SPEC.loader.exec_module(packager)


VERSION = "v2026.09.0"
COMMIT = "a" * 40
EPOCH = 1_785_542_400


class PackageKubectlCiscoVKTests(unittest.TestCase):
    def test_archive_is_byte_reproducible_and_normalized(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            binary = root / "one" / "kubectl-ciscovk"
            binary.parent.mkdir()
            binary.write_bytes(b"plugin-binary\x00\x01")
            binary.chmod(0o700)
            license_path = root / "LICENSE"
            license_path.write_bytes(b"Apache License fixture\n")
            first = root / "first.tar.gz"
            second = root / "other" / "second.tar.gz"

            packager.create_archive(first, binary, license_path, EPOCH)
            packager.create_archive(second, binary, license_path, EPOCH)

            self.assertEqual(first.read_bytes(), second.read_bytes())
            self.assertEqual(struct.unpack("<I", first.read_bytes()[4:8])[0], EPOCH)
            with tarfile.open(first, "r:gz") as archive:
                members = archive.getmembers()
                self.assertEqual(
                    [member.name for member in members],
                    ["LICENSE", "kubectl-ciscovk"],
                )
                self.assertEqual([member.mode for member in members], [0o644, 0o755])
                self.assertEqual([member.uid for member in members], [0, 0])
                self.assertEqual([member.gid for member in members], [0, 0])
                self.assertEqual([member.uname for member in members], ["", ""])
                self.assertEqual([member.gname for member in members], ["", ""])
                self.assertEqual([member.mtime for member in members], [EPOCH, EPOCH])
            self.assertEqual(
                packager.verify_archive(first, license_path, EPOCH),
                binary.read_bytes(),
            )

            tampered = bytearray(first.read_bytes())
            tampered[4:8] = (EPOCH + 1).to_bytes(4, byteorder="little")
            bad_header = root / "bad-header.tar.gz"
            bad_header.write_bytes(tampered)
            with self.assertRaisesRegex(packager.PackagingError, "gzip header"):
                packager.verify_archive(bad_header, license_path, EPOCH)

    def test_checksums_cover_exact_sorted_artifact_set(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            dist = pathlib.Path(temp_dir)
            for target in reversed(packager.TARGETS):
                (dist / packager.archive_name(VERSION, target)).write_bytes(
                    f"archive-{target.key}".encode()
                )
                (dist / packager.sbom_name(VERSION, target)).write_bytes(
                    json.dumps(
                        {
                            "bomFormat": "CycloneDX",
                            "specVersion": "1.6",
                            "metadata": {"component": {"name": target.key}},
                        }
                    ).encode()
                )

            checksum_path = packager.write_checksums(dist, VERSION, True)
            packager.verify_checksums(dist, VERSION, True)
            names = [
                line.split("  ", 1)[1]
                for line in checksum_path.read_text().splitlines()
            ]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), 8)

            (dist / packager.archive_name(VERSION, packager.TARGETS[0])).write_bytes(
                b"tampered"
            )
            with self.assertRaisesRegex(packager.PackagingError, "checksum mismatch"):
                packager.verify_checksums(dist, VERSION, True)

    def test_final_release_allowlist_has_fifteen_assets(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            dist = pathlib.Path(temp_dir)
            expected = packager.expected_release_assets(dist, VERSION)
            self.assertEqual(len(expected), 15)
            self.assertEqual(len({path.name for path in expected}), 15)
            self.assertEqual(
                len([path for path in expected if path.name.endswith(".tar.gz")]), 4
            )
            self.assertEqual(
                len(
                    [
                        path
                        for path in expected
                        if path.name.endswith(".tar.gz.sigstore.json")
                    ]
                ),
                4,
            )

    def test_final_release_allowlist_rejects_extra_file(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            dist = pathlib.Path(temp_dir)
            for target in packager.TARGETS:
                (dist / packager.archive_name(VERSION, target)).write_bytes(b"archive")
                (dist / packager.sbom_name(VERSION, target)).write_text(
                    json.dumps(
                        {
                            "bomFormat": "CycloneDX",
                            "specVersion": "1.6",
                            "metadata": {"component": {"name": target.key}},
                        }
                    ),
                    encoding="utf-8",
                )
                (dist / packager.archive_signature_name(VERSION, target)).write_text(
                    "{}", encoding="utf-8"
                )
            packager.write_checksums(dist, VERSION, True)
            (dist / packager.checksum_signature_name(VERSION)).write_text(
                "{}", encoding="utf-8"
            )
            (dist / packager.provenance_name(VERSION)).write_text(
                "{}", encoding="utf-8"
            )
            packager.verify_release_assets(dist, VERSION)

            (dist / "unexpected.txt").write_text("no", encoding="utf-8")
            with self.assertRaisesRegex(packager.PackagingError, "15-file allowlist"):
                packager.verify_release_assets(dist, VERSION)

    def test_checksums_reject_missing_sbom(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            dist = pathlib.Path(temp_dir)
            for target in packager.TARGETS:
                (dist / packager.archive_name(VERSION, target)).write_bytes(b"archive")
            with self.assertRaisesRegex(packager.PackagingError, "missing artifacts"):
                packager.write_checksums(dist, VERSION, True)

    def test_sbom_requires_an_object_and_named_component(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            path = pathlib.Path(temp_dir) / "sbom.cdx.json"
            path.write_text("[]", encoding="utf-8")
            with self.assertRaisesRegex(packager.PackagingError, "JSON object"):
                packager.verify_sbom(path)

            path.write_text(
                json.dumps(
                    {
                        "bomFormat": "CycloneDX",
                        "specVersion": "1.6",
                        "metadata": {"component": {"name": ""}},
                    }
                ),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(packager.PackagingError, "no name"):
                packager.verify_sbom(path)

    def test_release_names_are_krew_compatible_and_exclude_windows(self) -> None:
        names = [packager.archive_name(VERSION, target) for target in packager.TARGETS]
        self.assertEqual(
            names,
            [
                "kubectl-ciscovk_v2026.09.0_darwin_amd64.tar.gz",
                "kubectl-ciscovk_v2026.09.0_darwin_arm64.tar.gz",
                "kubectl-ciscovk_v2026.09.0_linux_amd64.tar.gz",
                "kubectl-ciscovk_v2026.09.0_linux_arm64.tar.gz",
            ],
        )
        self.assertNotIn("windows", " ".join(names))

    def test_go_build_command_is_reproducible(self) -> None:
        command = packager.go_build_command(
            "go",
            pathlib.Path("build/linux_amd64/kubectl-ciscovk"),
            VERSION,
            COMMIT,
            "2026-08-01T00:00:00Z",
        )
        rendered = " ".join(command)
        self.assertIn("-mod=readonly", command)
        self.assertIn("-trimpath", command)
        self.assertIn("-buildvcs=false", command)
        self.assertIn("-buildid=", rendered)
        self.assertIn(f"main.Version={VERSION}", rendered)
        self.assertIn(f"main.GitCommit={COMMIT}", rendered)
        self.assertIn("main.BuildTime=2026-08-01T00:00:00Z", rendered)

    def test_build_time_is_utc_and_stable(self) -> None:
        self.assertEqual(packager.build_time(0), "1970-01-01T00:00:00Z")
        self.assertEqual(packager.build_time(EPOCH), "2026-08-01T00:00:00Z")

    def test_supplied_epoch_must_match_commit(self) -> None:
        with mock.patch.object(packager, "commit_epoch", return_value=EPOCH):
            self.assertEqual(
                packager.release_epoch(pathlib.Path("."), COMMIT, EPOCH), EPOCH
            )
            with self.assertRaisesRegex(packager.PackagingError, "does not match"):
                packager.release_epoch(pathlib.Path("."), COMMIT, EPOCH + 1)

    def test_unsafe_identifiers_are_rejected(self) -> None:
        for version in ("", "2026.09.0", "vbad/name", "v bad"):
            with self.subTest(version=version):
                with self.assertRaises(packager.PackagingError):
                    packager.validate_version(version)
        for commit in ("a" * 39, "A" * 40, "../" + "a" * 40):
            with self.subTest(commit=commit):
                with self.assertRaises(packager.PackagingError):
                    packager.validate_commit(commit)

    def test_nonempty_output_is_never_recursively_cleaned(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output = pathlib.Path(temp_dir)
            (output / "existing").write_text("keep", encoding="utf-8")
            with self.assertRaisesRegex(packager.PackagingError, "refusing to reuse"):
                packager.prepare_directory(output)
            self.assertEqual((output / "existing").read_text(), "keep")

    def test_output_directories_must_not_overlap(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            for dist, build in (
                (root, root),
                (root / "dist", root / "dist" / "build"),
                (root / "build" / "dist", root / "build"),
            ):
                with self.subTest(dist=dist, build=build):
                    with self.assertRaisesRegex(packager.PackagingError, "overlap"):
                        packager.verify_output_directories(dist, build)

            packager.verify_output_directories(root / "dist", root / "build")


if __name__ == "__main__":
    unittest.main()
