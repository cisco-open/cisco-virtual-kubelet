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
import json
import os
import pathlib
import re
import subprocess
import tempfile
import unittest

from generate_mkdocs_licenses import (
    LicenseBundleError,
    verify_mermaid_runtime_notice,
)


ROOT = pathlib.Path(__file__).resolve().parents[2]


class ReleaseContractTests(unittest.TestCase):
    def test_chart_license_is_exact_project_license(self) -> None:
        self.assertEqual(
            (ROOT / "LICENSE").read_bytes(),
            (ROOT / "charts/cisco-virtual-kubelet/LICENSE").read_bytes(),
        )

    def test_docker_context_excludes_local_build_and_secret_material(self) -> None:
        dockerignore = (ROOT / ".dockerignore").read_text(encoding="utf-8")
        for pattern in (
            ".git",
            ".env.*",
            "**/*.key",
            "**/*.pem",
            "bin/",
            "build/",
            "dist/",
            "site/",
            "docs/website/.next/",
            "docs/website/node_modules/",
            "docs/website/out/",
        ):
            self.assertIn(pattern, dockerignore.splitlines())

    def test_release_toolchain_and_provenance_are_pinned(self) -> None:
        dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
        lint_dockerfile = (ROOT / "Dockerfile.config-lint").read_text(encoding="utf-8")
        release = (ROOT / ".github/workflows/release.yml").read_text(encoding="utf-8")
        pinned_builder = (
            "golang:1.26.7-alpine@sha256:"
            "28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468"
        )
        self.assertIn(pinned_builder, dockerfile)
        self.assertIn(pinned_builder, lint_dockerfile)
        self.assertNotIn("apk add", dockerfile + lint_dockerfile)
        self.assertIn("main.Version=${VERSION}", dockerfile)
        self.assertIn("main.GitCommit=${GIT_COMMIT}", dockerfile)
        self.assertIn("main.BuildTime=${BUILD_TIME}", dockerfile)
        self.assertIn("github.com/google/go-licenses/v2@v2.0.1", dockerfile)
        self.assertIn("github.com/moby/spdystream/spdy/PATENTS", dockerfile)
        self.assertIn("go-version: '1.26.7'", release)
        self.assertIn("version: v3.21.4", release)
        self.assertEqual(release.count("version: v0.36.1"), 1)
        self.assertEqual(
            release.count(
                "image=moby/buildkit:v0.32.2@sha256:"
                "28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8"
            ),
            1,
        )
        self.assertNotIn("moby/buildkit:buildx-stable-1", release)
        self.assertNotIn("buildkit-syft-scanner", release)
        self.assertNotIn("sbom: true", release)
        sbom_action_commit = "3ad7283483fc7af8ff2b4ea19663c2d5ca935e26"
        self.assertEqual(
            release.count(f"anchore/sbom-action@{sbom_action_commit} # v0.24.2"),
            1,
        )
        self.assertEqual(
            release.count(
                "anchore/sbom-action/download-syft@" f"{sbom_action_commit} # v0.24.2"
            ),
            1,
        )
        self.assertEqual(release.count("syft-version: v1.51.1"), 1)
        self.assertNotIn("e22c389904149dbc22b58101806040fa8d37a610", release)
        self.assertNotIn("syft-version: v1.50.0", release)
        self.assertEqual(release.count("cosign-release: v3.1.3"), 3)
        self.assertNotRegex(release, r"cosign-release: v(?:2\.|3\.1\.[0-2]$)")
        self.assertIn("Execute exact signed image and verify provenance", release)
        self.assertIn("Exactly one non-prerelease draft", release)
        self.assertIn("name: Require exact curated draft", release)
        self.assertGreaterEqual(release.count(".target_commitish == $commit"), 3)
        self.assertNotIn("ubuntu-latest", release)
        self.assertNotIn("1.25", dockerfile + lint_dockerfile + release)
        self.assertEqual((ROOT / "go.mod").read_text().splitlines()[2], "go 1.26.7")

    def test_workflow_runtimes_are_current_and_do_not_use_mutable_binfmt(
        self,
    ) -> None:
        workflows = {
            path.name: path.read_text(encoding="utf-8")
            for path in sorted((ROOT / ".github/workflows").glob("*.y*ml"))
        }
        python_pin_counts = {
            name: len(re.findall(r"python-version:\s*['\"]3\.13\.15['\"]", text))
            for name, text in workflows.items()
            if "python-version:" in text
        }
        self.assertEqual(
            python_pin_counts,
            {
                "develop.yml": 1,
                "krew-index.yml": 1,
                "recover-v2026.8.1-release-assets.yml": 1,
                "release.yml": 4,
                "smoke.yml": 1,
            },
        )
        combined = "\n".join(workflows.values())
        self.assertNotIn("ubuntu-latest", combined)
        self.assertNotIn("3.13.14", combined)
        self.assertNotIn("docker/setup-qemu-action", combined)
        self.assertNotIn("tonistiigi/binfmt", combined)
        self.assertNotIn("binfmt:latest", combined)
        self.assertNotIn("golang/govulncheck-action", combined)
        self.assertNotIn("govulncheck@latest", combined)
        self.assertEqual(
            combined.count("go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./..."),
            2,
        )
        self.assertIn(
            '"$RUNNER_TEMP/krew-release-bot/krew-release-bot" action',
            workflows["krew-index.yml"],
        )
        self.assertNotIn("count=$(ls config/crd/*.yaml", workflows["smoke.yml"])
        self.assertNotIn("for i in $(seq", workflows["smoke.yml"])
        self.assertIn(
            "for ((attempt = 1; attempt <= 30; attempt++))", workflows["smoke.yml"]
        )
        self.assertIn(
            "for ((attempt = 1; attempt <= 60; attempt++))", workflows["smoke.yml"]
        )

    def test_go_redistribution_files_match_go_1_26_7(self) -> None:
        expected = {
            "LICENSE": "911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad",
            "PATENTS": "96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc",
        }
        for name, digest in expected.items():
            content = (ROOT / "third_party/go" / name).read_bytes()
            self.assertEqual(hashlib.sha256(content).hexdigest(), digest)

    def test_installer_is_syntactically_valid_and_current(self) -> None:
        installer = ROOT / "scripts/install.sh"
        subprocess.run(["bash", "-n", str(installer)], check=True)
        help_result = subprocess.run(
            ["bash", str(installer), "--help"],
            check=True,
            capture_output=True,
            text=True,
        )
        text = installer.read_text(encoding="utf-8")
        self.assertIn('PINNED_GO_VERSION="1.26.7"', text)
        for required in (
            "ffb5f8de10c62550dfddab66b36b57030721e0a44a3218e9e1181d7b59f121ca",
            "5a4ec883379d51ee9ce1040d5e87f8d35e20387574dd8c947feb01eabc3c1b37",
            "675c26c449cbb18fc24b74650de1eabbae6e16f64326fd85a283fb3b58280685",
            "51798d2c42d0e1c6ed7fd9f48728b4193abac9e8aad6dbac2fe96a81f5909bda",
            "go1.26.7+ or go1.27.0+",
        ):
            self.assertIn(required, text)
        self.assertNotIn("1.25", text)
        self.assertIn("Usage: bash", help_result.stdout)
        bad_option = subprocess.run(
            ["bash", str(installer), "--definitely-not-an-option"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(bad_option.returncode, 2)
        self.assertIn("Unknown option", bad_option.stderr)

        with tempfile.TemporaryDirectory() as temp_dir:
            fake_go = pathlib.Path(temp_dir) / "go"
            fake_go.write_text(
                "#!/bin/sh\n"
                'if [ "$1 $2" = "env GOVERSION" ]; then\n'
                "  printf 'go1.26.7\\n'\n"
                "  exit 0\n"
                "fi\n"
                "exit 64\n",
                encoding="utf-8",
            )
            fake_go.chmod(0o755)
            env = os.environ.copy()
            env.pop("GO_VERSION", None)
            env["PATH"] = f"{temp_dir}{os.pathsep}{env['PATH']}"
            explicit_override = subprocess.run(
                ["bash", str(installer), "--go-version", "1.27.0"],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )
        self.assertEqual(explicit_override.returncode, 1)
        self.assertIn(
            "exact Go 1.27.0 was requested",
            explicit_override.stdout,
        )

        # An exact override must reach install_go even when PATH already has a
        # different Go version that is independently supported. Exercise the
        # real dependency-selection and missing-dependency blocks with only
        # the privileged/download functions stubbed.
        supported_function = re.search(
            r"^go_version_is_supported\(\) \{.*?^\}",
            text,
            re.MULTILINE | re.DOTALL,
        )
        selection_function = re.search(
            r"^go_version_satisfies_request\(\) \{.*?^\}",
            text,
            re.MULTILINE | re.DOTALL,
        )
        self.assertIsNotNone(supported_function)
        self.assertIsNotNone(selection_function)
        go_check = text.split(
            "# Check for a patched, module-compatible Go toolchain.", 1
        )[1].split("# Check for kubectl (optional)", 1)[0]
        missing_handler = text.split("# Handle missing dependencies", 1)[1].split(
            "# Verify the selected Go remains compatible", 1
        )[0]
        with tempfile.TemporaryDirectory() as temp_dir:
            fake_go = pathlib.Path(temp_dir) / "go"
            fake_go.write_text(
                "#!/bin/sh\n"
                'if [ "$1 $2" = "env GOVERSION" ]; then\n'
                "  printf 'go1.26.7\\n'\n"
                "  exit 0\n"
                "fi\n"
                "exit 64\n",
                encoding="utf-8",
            )
            fake_go.chmod(0o755)
            env = os.environ.copy()
            env["PATH"] = f"{temp_dir}{os.pathsep}{env['PATH']}"
            forced_install = subprocess.run(
                [
                    "bash",
                    "-c",
                    "set -euo pipefail\n"
                    + supported_function.group(0)
                    + "\n"
                    + selection_function.group(0)
                    + "\n"
                    + "GO_VERSION=1.27.0\n"
                    + "GO_VERSION_EXPLICIT=true\n"
                    + "MISSING_DEPS=false\n"
                    + "NEED_GO=false\n"
                    + "RED= GREEN= YELLOW= BLUE= NC=\n"
                    + go_check
                    + "\n"
                    + 'test "$NEED_GO" = true\n'
                    + 'test "$MISSING_DEPS" = true\n'
                    + "install_build_deps() { :; }\n"
                    + 'install_go() { INSTALLED_GO_VERSION="$GO_VERSION"; }\n'
                    + "INSTALL_DEPS=true\n"
                    + 'INSTALLED_GO_VERSION=""\n'
                    + missing_handler
                    + "\n"
                    + 'test "$INSTALLED_GO_VERSION" = "1.27.0"\n'
                    + 'printf "installed=%s\\n" "$INSTALLED_GO_VERSION"\n',
                ],
                cwd=ROOT,
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )
        self.assertIn("installed=1.27.0", forced_install.stdout)
        # The privileged toolchain must always be replaced from the freshly
        # verified archive; an executable left by an earlier user is not a
        # trust signal. Keep the exact-target, root ownership, and no-group/
        # other-write contract visible in this non-privileged unit test.
        for ownership_contract in (
            'toolchain_base="/usr/local/lib/cisco-vk"',
            'harden_staged_toolchain "$staged_root"',
            'sudo chmod 0755 "$root"',
            'sudo rm -rf -- "$toolchain_root"',
            "stat -c '%u:%g'",
            "-perm /022",
        ):
            self.assertIn(ownership_contract, text)
        self.assertNotIn('test -x "$toolchain_root/bin/go" ||', text)

        hardener = re.search(
            r"^harden_staged_toolchain\(\) \{.*?^\}",
            text,
            re.MULTILINE | re.DOTALL,
        )
        self.assertIsNotNone(hardener)
        with tempfile.TemporaryDirectory() as temp_dir:
            staged = pathlib.Path(temp_dir) / "staged"
            binary = staged / "bin" / "go"
            binary.parent.mkdir(parents=True)
            binary.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            staged.chmod(0o700)  # sudo mktemp's starting mode
            binary.chmod(0o777)
            subprocess.run(
                [
                    "bash",
                    "-c",
                    hardener.group(0)
                    + '\nsudo() { if [[ "$1" = chown ]]; then return 0; fi; "$@"; }\n'
                    + 'harden_staged_toolchain "$1"\n'
                    + 'test -x "$1/bin/go"',
                    "installer-hardener-test",
                    str(staged),
                ],
                check=True,
            )
            self.assertEqual(staged.stat().st_mode & 0o777, 0o755)
            for item in (staged, binary.parent, binary):
                self.assertEqual(item.stat().st_mode & 0o022, 0)
        for obsolete in (
            "1.21.13",
            "docs/INSTALL.md",
            "cmd/virtual-kubelet",
            "examples/systemd",
        ):
            self.assertNotIn(obsolete, text)

        function = re.search(
            r"^go_version_is_supported\(\) \{.*?^\}", text, re.MULTILINE | re.DOTALL
        )
        self.assertIsNotNone(function)
        support_checks = """
go_version_is_supported go1.26.7
go_version_is_supported go1.26.99
go_version_is_supported go1.27.0
go_version_is_supported go1.27.9
! go_version_is_supported go1.25.14
! go_version_is_supported go1.26.6
! go_version_is_supported go1.28.0
! go_version_is_supported devel
"""
        subprocess.run(
            ["bash", "-c", function.group(0) + "\n" + support_checks], check=True
        )

    def test_makefile_respects_the_callers_go_binary(self) -> None:
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertIn("GO_BIN?=go", makefile)
        for obsolete in ("SNAP_GO_PATH", "GO_SNAP_DETECTED", "export GOROOT"):
            self.assertNotIn(obsolete, makefile)

        with tempfile.TemporaryDirectory() as temp_dir:
            fake_go = pathlib.Path(temp_dir) / "go"
            fake_go.write_text(
                "#!/bin/sh\nprintf 'go version go9.9.9 linux/amd64\\n'\n",
                encoding="utf-8",
            )
            fake_go.chmod(0o755)
            env = os.environ.copy()
            env.pop("GO_BIN", None)
            env["PATH"] = f"{temp_dir}{os.pathsep}{env['PATH']}"
            result = subprocess.run(
                ["make", "--no-print-directory", "version"],
                cwd=ROOT,
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )
        self.assertIn("Go Version: go9.9.9", result.stdout)
        self.assertIn("Go Binary: go", result.stdout)

    def test_mermaid_runtime_notice_fields_are_fail_closed(self) -> None:
        license_text = (
            "Copyright (c) Example Authors\n\n"
            "Permission is hereby granted to use this test fixture."
        )
        inventory = [
            {
                "name": "example-runtime",
                "version": "1.2.3",
                "license": "MIT",
                "integrity": "sha512-fixture",
                "licenseFiles": [
                    {
                        "path": "package/LICENSE",
                        "sha256": hashlib.sha256(
                            license_text.encode("utf-8")
                        ).hexdigest(),
                    }
                ],
            }
        ]
        divider = "=" * 80
        notice = "\n".join(
            [
                "MkDocs vendored browser-runtime licenses",
                "",
                "Regression fixture.",
                "",
                divider,
                "Package: example-runtime 1.2.3",
                "License: MIT",
                "npm integrity: sha512-fixture",
                "",
                "--- package/LICENSE ---",
                "",
                license_text,
            ]
        )
        self.assertEqual(
            verify_mermaid_runtime_notice(notice, inventory),
            {("example-runtime", "1.2.3")},
        )

        mutated_notices = {
            "license label": (
                notice.replace("License: MIT", "License: Apache-2.0"),
                "license label differs",
            ),
            "npm integrity": (
                notice.replace("sha512-fixture", "sha512-changed"),
                "integrity differs",
            ),
            "license-file path": (
                notice.replace("package/LICENSE", "package/NOTICE"),
                "license-file paths differ",
            ),
            "license text": (
                notice.replace("Permission is hereby", "Permission was formerly"),
                "license text differs",
            ),
            "unparsed prefix": (
                notice.replace(
                    "--- package/LICENSE ---",
                    "unrecognized content\n\n--- package/LICENSE ---",
                ),
                "license-file layout",
            ),
        }
        for field, (mutated, error) in mutated_notices.items():
            with self.subTest(field=field):
                with self.assertRaisesRegex(LicenseBundleError, error):
                    verify_mermaid_runtime_notice(mutated, inventory)

        bad_hash_inventory = json.loads(json.dumps(inventory))
        bad_hash_inventory[0]["licenseFiles"][0]["sha256"] = "0" * 64
        with self.assertRaisesRegex(LicenseBundleError, "license text differs"):
            verify_mermaid_runtime_notice(notice, bad_hash_inventory)

    def test_documentation_closures_are_locked_licensed_and_premerge_gated(
        self,
    ) -> None:
        lock = (ROOT / "requirements.txt").read_text(encoding="utf-8")
        self.assertIn("mkdocs-material==9.7.7", lock)
        self.assertIn("--hash=sha256:", lock)
        self.assertIn("mkdocs-material==9.7.7", (ROOT / "requirements.in").read_text())

        locked = {
            (re.sub(r"[-_.]+", "-", name).lower(), version)
            for name, version in re.findall(
                r"^([A-Za-z0-9][A-Za-z0-9._-]*)==([^\s\\]+)",
                lock,
                re.MULTILINE,
            )
        }
        bundle = (ROOT / "docs/MKDOCS_THIRD_PARTY_LICENSES.txt").read_text(
            encoding="utf-8"
        )
        python_bundle = bundle.split("MkDocs browser-runtime dependency licenses", 1)[0]
        bundled = {
            (re.sub(r"[-_.]+", "-", name).lower(), version)
            for name, version in re.findall(
                r"^Package: (.+) ([^ ]+)$", python_bundle, re.MULTILINE
            )
        }
        self.assertEqual(bundled, locked)
        for required_notice in (
            "Font Awesome Free License",
            "Pictogrammers Free License",
            "CC0 1.0 Universal",
            "clipboard 2.0.11",
            "good-listener 1.2.2",
            "rxjs 7.8.2",
            "tslib 2.7.0",
        ):
            self.assertIn(required_notice, bundle)

        smoke = (ROOT / ".github/workflows/smoke.yml").read_text(encoding="utf-8")
        deploy = (ROOT / ".github/workflows/develop.yml").read_text(encoding="utf-8")
        for workflow in (smoke, deploy):
            self.assertIn("--require-hashes -r requirements.txt", workflow)
            self.assertIn("generate_mkdocs_licenses.py --check", workflow)
            self.assertIn("--check --site site", workflow)
            self.assertIn("mkdocs build --strict", workflow)
            self.assertIn("npm audit --audit-level=high", workflow)
            self.assertIn("npm run lint", workflow)

        website_bundle = (
            ROOT / "docs/website/public/THIRD_PARTY_LICENSES.txt"
        ).read_text(encoding="utf-8")
        website_generator = (
            ROOT / "docs/website/scripts/generate-third-party-licenses.mjs"
        ).read_text(encoding="utf-8")
        website_package = (ROOT / "docs/website/package.json").read_text(
            encoding="utf-8"
        )
        website_manifest = json.loads(website_package)
        self.assertIn("tailwindcss@4.1.18", website_bundle)
        self.assertIn("Copyright (c) Tailwind Labs, Inc.", website_bundle)
        self.assertIn(
            "fa2d5ae43ae561061b7ce348b89636dbdc6cd71ab5992d4e1cdd046d0b4f28f9",
            website_generator,
        )
        self.assertIn("tailwindPreflightMarker", website_generator)
        self.assertIn("next build && npm run licenses:check-build", website_package)
        core_js_entry = website_generator.split('name: "core-js"', 1)[1].split(
            "  },", 1
        )[0]
        self.assertEqual(core_js_entry.count('license: "MIT"'), 1)

        for package, version in (
            ("mermaid", "11.17.2"),
            ("resize-observer-polyfill", "1.5.1"),
        ):
            self.assertEqual(website_manifest["devDependencies"][package], version)
            self.assertNotIn(package, website_manifest["dependencies"])

        runtime_manifest_path = (
            ROOT / "third_party/mkdocs-material/9.7.7/RUNTIME_ASSETS.json"
        )
        runtime_manifest = json.loads(runtime_manifest_path.read_text())
        runtime_audit = runtime_manifest["mermaidBundleAudit"]
        for path_key, hash_key in (
            ("inventory", "inventorySha256"),
            ("licenses", "licensesSha256"),
        ):
            audited_path = ROOT / runtime_audit[path_key]
            self.assertEqual(
                hashlib.sha256(audited_path.read_bytes()).hexdigest(),
                runtime_audit[hash_key],
            )
        inventory = json.loads((ROOT / runtime_audit["inventory"]).read_text())
        exact_runtime = {
            (package["name"], package["version"]) for package in inventory["packages"]
        }
        self.assertEqual(len(exact_runtime), 79)
        for required_runtime in (
            ("mermaid", "11.17.2"),
            ("@mermaid-js/parser", "1.2.1"),
            ("js-yaml", "4.3.0"),
            ("vscode-uri", "3.1.0"),
            ("hachure-fill", "0.5.2"),
            ("path-data-parser", "0.1.0"),
            ("points-on-curve", "0.2.0"),
            ("points-on-path", "0.2.1"),
            ("resize-observer-polyfill", "1.5.1"),
        ):
            self.assertIn(required_runtime, exact_runtime)
        runtime_licenses = (ROOT / runtime_audit["licenses"]).read_text()
        self.assertEqual(
            verify_mermaid_runtime_notice(
                runtime_licenses.strip(), inventory["packages"]
            ),
            exact_runtime,
        )
        licensed_runtime = set(
            re.findall(r"^Package: (.+) ([^ ]+)$", runtime_licenses, re.MULTILINE)
        )
        self.assertEqual(licensed_runtime, exact_runtime)

        self.assertIn("pull_request:\n    branches: [main]", smoke)
        self.assertIn("version: v3.21.4", smoke)
        self.assertIn("version: v4.2.4", smoke)
        self.assertLess(
            smoke.index("run: go test -race -count=1 ./..."),
            smoke.index("Website lint, audit, build, and license gate"),
        )
        self.assertLess(
            smoke.index("Website lint, audit, build, and license gate"),
            smoke.index("MkDocs strict build and license gate"),
        )
        self.assertLess(
            deploy.index("Build React website"),
            deploy.index("Build MkDocs documentation"),
        )
        mkdocs_config = (ROOT / "mkdocs.yml").read_text()
        self.assertIn(".github/scripts/mkdocs_hooks.py", mkdocs_config)
        self.assertIn("  font: false", mkdocs_config)
        hook = (ROOT / ".github/scripts/mkdocs_hooks.py").read_text()
        self.assertIn('deployed_name = f"bundle.{digest[:16]}.min.js"', hook)
        self.assertIn("Material local loader must preserve source-map offsets", hook)
        mkdocs_gate = (ROOT / ".github/scripts/generate_mkdocs_licenses.py").read_text()
        self.assertIn("installed_mermaid_runtime_inventory", mkdocs_gate)
        self.assertIn("verify_mermaid_runtime_notice", mkdocs_gate)
        self.assertIn(
            "Mermaid runtime license text differs from inventory", mkdocs_gate
        )
        self.assertIn("verify_no_remote_runtime_resources", mkdocs_gate)
        self.assertIn("Material bundle is not SHA-256 content-addressed", mkdocs_gate)
        makefile = (ROOT / "Makefile").read_text()
        self.assertIn("mkdocs-build:", makefile)
        self.assertIn("cd docs/website && $(NPM) ci --include=dev", makefile)

    def test_docs_publish_only_from_immutable_release(self) -> None:
        workflow = (ROOT / ".github/workflows/develop.yml").read_text(encoding="utf-8")
        self.assertIn("release:\n    types: [published]", workflow)
        self.assertIn(".immutable == true", workflow)
        self.assertIn("persist-credentials: false", workflow)
        self.assertNotRegex(
            workflow, r"uses:\s+[^\s#]+@(v\d+|main|master|latest)(?:\s|$)"
        )

    def test_runbook_requires_draft_before_tag_and_schema_approval_continuity(
        self,
    ) -> None:
        runbook = (ROOT / "RELEASE.md").read_text(encoding="utf-8")
        draft = runbook.index("## 3. Create the draft before the tag")
        tag = runbook.index("## 4. Push the tag")
        self.assertLess(draft, tag)
        self.assertIn("Cisco OSPO approval established", runbook)
        self.assertIn("Re-engage OSPO", runbook)
        self.assertIn("networkcontrollers.cisco.vk", runbook)
        self.assertIn("networkcontrollerconfigs.config.cisco.vk", runbook)
        self.assertIn("go1.26.7", runbook)
        self.assertNotIn("go1.25", runbook)

    def test_all_actions_are_commit_pinned(self) -> None:
        mutable = re.compile(r"uses:\s+[^\s#]+@(v\d+|main|master|latest)(?:\s|$)")
        offenders: list[str] = []
        for workflow in sorted((ROOT / ".github/workflows").glob("*.y*ml")):
            for number, line in enumerate(workflow.read_text().splitlines(), 1):
                if mutable.search(line):
                    offenders.append(f"{workflow.name}:{number}: {line.strip()}")
        self.assertEqual(offenders, [])


if __name__ == "__main__":
    unittest.main()
