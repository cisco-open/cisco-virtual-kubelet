# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
"""Exercise redistribution bytes and fail-closed boundaries without a container."""
from __future__ import annotations

import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE = "github.com/cisco/virtual-kubelet-cisco"
SDWAN = "internal/controlleradapter/sdwan"
IOSXE = "internal/drivers/iosxe"
SCHEMA = IOSXE + "/configdriver/schema"
GENERATED = IOSXE + "/configdriver/generated/1791/vlan"


class EmbeddedLicenseTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory()
        cls.binary = pathlib.Path(cls.build.name) / "package-embedded-licenses"
        subprocess.run([os.environ.get("GO_BIN", "go"), "build", "-o", str(cls.binary),
                        str(ROOT / ".github/scripts/package_embedded_licenses.go")],
                       cwd=ROOT, check=True, env={**os.environ, "GOTOOLCHAIN": "local"})

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = pathlib.Path(self.temp.name)
        self.root = self.base / "repo"
        self.root.mkdir()
        for path in ("LICENSE", "third_party/embedded/README.md", "third_party/cisco-iosxe/README.md",
                     "third_party/cisco-iosxe/LICENSE.md", SDWAN + "/definitions/LICENSE",
                     SDWAN + "/definitions/README.md", SDWAN + "/testdata/upstream-lock.json",
                     SCHEMA + "/yang/1791/.provenance.yaml"):
            self.put(path, (ROOT / path).read_bytes())
        lock = json.loads((self.root / SDWAN / "testdata/upstream-lock.json").read_text())
        self.metadata = {"license": "MPL-2.0", "module_commit": lock["module"]["commit"],
                         "provider_commit": lock["provider"]["commit"]}
        for name in ("scalar-parcels.json", "classic-templates.json"):
            self.put(SDWAN + "/definitions/" + name, json.dumps(self.metadata).encode())
        self.put(SDWAN + "/definitions/selected.json", b'{"payload": "exact embedded bytes"}\n')
        self.put(SDWAN + "/definitions/not-linked.json", b'{"unused":true}')
        self.put(SCHEMA + "/families.yaml", b"family: index\n")
        self.put(GENERATED + "/schema.go", b"package schema\nvar data = []byte{1, 2}\n")
        self.put(IOSXE + "/models.go", b"package iosxe\nvar model = []byte{3}\n")
        self.put("tests/yang/cisco.yang", b"module cisco { description 'Copyright Cisco'; }\n")
        self.put("tests/yang/ietf.yang", b"module ietf { description 'Simplified BSD'; }\n")
        for name in ("generate-parcel-definitions.py", "generate-classic-definitions.py", "upstream-inventory.py"):
            self.put("scripts/sdwan/" + name, b"# generator source\n")
        self.packages = [self.pkg(SDWAN, embeds=["definitions/selected.json"]),
                         self.pkg(SCHEMA, embeds=["families.yaml"]),
                         self.pkg(GENERATED, files=["schema.go"]),
                         self.pkg(IOSXE, files=["models.go"])]

    def put(self, path, content):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)

    def pkg(self, path, embeds=(), files=()):
        return {"Dir": str(self.root / path), "ImportPath": MODULE + "/" + path,
                "Module": {"Path": MODULE}, "EmbedFiles": list(embeds), "GoFiles": list(files)}

    def run_bundle(self, name="bundle", succeeds=True):
        inventory = self.base / "packages.json"
        inventory.write_text("\n".join(json.dumps(p) for p in self.packages))
        output = self.base / name
        result = subprocess.run([str(self.binary), "--repo-root", str(self.root),
                                 "--packages", str(inventory), "--output", str(output)],
                                capture_output=True, text=True)
        if succeeds:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists(), result.stderr)
        return output, result

    def test_exact_bytes_allowlist_and_determinism(self):
        first, _ = self.run_bundle()
        second, _ = self.run_bundle("second")
        manifest = json.loads((first / "MANIFEST.json").read_text())
        paths = [f["path"] for f in manifest["files"]]
        self.assertEqual(paths, sorted(set(paths)))
        self.assertNotIn("source/" + SDWAN + "/definitions/not-linked.json", paths)
        for required in (SDWAN + "/definitions/selected.json", SDWAN + "/definitions/LICENSE",
                         SDWAN + "/definitions/README.md", SDWAN + "/testdata/upstream-lock.json",
                         GENERATED + "/schema.go", IOSXE + "/models.go", "tests/yang/ietf.yang",
                         SCHEMA + "/yang/1791/.provenance.yaml", "third_party/cisco-iosxe/LICENSE.md"):
            self.assertIn("source/" + required, paths)
        actual_files = {p.relative_to(first).as_posix() for p in first.rglob("*") if p.is_file()}
        self.assertEqual(actual_files, set(paths) | {"MANIFEST.json", "README.md"})
        for record in manifest["files"]:
            data = (first / record["path"]).read_bytes()
            self.assertEqual(data, (self.root / record["path"].removeprefix("source/")).read_bytes())
            self.assertEqual(record["sha256"], hashlib.sha256(data).hexdigest())
            self.assertEqual(record["size"], len(data))
        for path in actual_files:
            self.assertEqual((first / path).read_bytes(), (second / path).read_bytes())
            self.assertEqual((first / path).stat().st_mtime, 0)
            self.assertEqual((first / path).stat().st_mode & 0o777, 0o644)

    def test_unknown_embedded_asset_fails_before_output(self):
        self.put("internal/new/data.json", b"{}")
        self.packages.append(self.pkg("internal/new", embeds=["data.json"]))
        _, result = self.run_bundle(succeeds=False)
        self.assertIn("unclassified first-party embedded asset", result.stderr)

    def test_missing_license_and_wrong_upstream_commit_fail(self):
        license_path = self.root / SDWAN / "definitions/LICENSE"
        original = license_path.read_bytes()
        license_path.unlink()
        self.run_bundle(succeeds=False)
        license_path.write_bytes(original)
        self.metadata["provider_commit"] = "0" * 40
        self.put(SDWAN + "/definitions/scalar-parcels.json", json.dumps(self.metadata).encode())
        _, result = self.run_bundle(succeeds=False)
        self.assertIn("definition/provenance mismatch", result.stderr)

    def test_license_drift_and_yang_provenance_fail(self):
        license_path = self.root / "third_party/cisco-iosxe/LICENSE.md"
        original = license_path.read_bytes()
        license_path.write_bytes(original + b"altered")
        self.run_bundle(succeeds=False)
        license_path.write_bytes(original)
        provenance = self.root / SCHEMA / "yang/1791/.provenance.yaml"
        provenance.write_text(provenance.read_text().replace("647250f1", "00000000"))
        _, result = self.run_bundle(succeeds=False)
        self.assertIn("unreviewed YANG provenance", result.stderr)

    def test_symlink_and_traversal_fail(self):
        source = self.root / SDWAN / "definitions/selected.json"
        source.unlink()
        source.symlink_to(self.root / "LICENSE")
        _, result = self.run_bundle(succeeds=False)
        self.assertIn("symlink source", result.stderr)
        self.packages[0]["EmbedFiles"] = ["../../LICENSE"]
        self.run_bundle(succeeds=False)

    def test_incomplete_or_empty_inventory_fails(self):
        self.packages[0]["Error"] = {"Err": "cannot load dependency"}
        self.run_bundle(succeeds=False)
        self.packages = []
        self.run_bundle(succeeds=False)

    def test_valid_binary_without_embedded_data_has_no_borrowed_source(self):
        self.put("internal/plain/plain.go", b"package plain\n")
        self.packages = [self.pkg("internal/plain", files=["plain.go"])]
        output, _ = self.run_bundle()
        paths = {entry["path"] for entry in json.loads((output / "MANIFEST.json").read_text())["files"]}
        self.assertEqual(paths, {"source/LICENSE", "source/third_party/embedded/README.md"})

    def test_existing_output_is_not_overwritten(self):
        output, _ = self.run_bundle()
        original = (output / "MANIFEST.json").read_bytes()
        inventory = self.base / "packages.json"
        result = subprocess.run([str(self.binary), "--repo-root", str(self.root),
                                 "--packages", str(inventory), "--output", str(output)],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("output already exists", result.stderr)
        self.assertEqual((output / "MANIFEST.json").read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
