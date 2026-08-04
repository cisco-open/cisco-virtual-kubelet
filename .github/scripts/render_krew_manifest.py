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
"""Render a deterministic Krew manifest from signed release checksums."""

from __future__ import annotations

import argparse
import hashlib
import pathlib
import re
import string
import sys
from dataclasses import dataclass
from typing import Optional, Sequence


REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
DEFAULT_TEMPLATE = REPO_ROOT / "krew" / "cisco-vk.yaml.in"
RELEASE_TAG_RE = re.compile(
    r"^v([1-9][0-9]{3})\.([1-9]|1[0-2])\.(0|[1-9][0-9]{0,8})$"
)
CHECKSUM_RE = re.compile(r"^([0-9a-f]{64})  ([A-Za-z0-9][A-Za-z0-9._+-]*)$")
UNRESOLVED_TOKEN_RE = re.compile(r"\$\{[A-Z0-9_]+\}")
RELEASE_BASE_URL = (
    "https://github.com/cisco-open/cisco-virtual-kubelet/releases/download"
)


class KrewManifestError(RuntimeError):
    """The release metadata cannot produce a safe Krew manifest."""


@dataclass(frozen=True, order=True)
class Target:
    os: str
    arch: str

    @property
    def key(self) -> str:
        return f"{self.os}_{self.arch}"

    @property
    def token_prefix(self) -> str:
        return self.key.upper()


TARGETS = (
    Target("darwin", "amd64"),
    Target("darwin", "arm64"),
    Target("linux", "amd64"),
    Target("linux", "arm64"),
)


def normalize_krew_version(release_tag: str) -> str:
    """Validate and return the strict SemVer/CalVer tag Krew requires."""
    match = RELEASE_TAG_RE.fullmatch(release_tag)
    if not match:
        raise KrewManifestError(
            "release tag must match strict final CVK CalVer vYYYY.M.PATCH "
            "without leading zeroes"
        )
    year_text, month_text, patch_text = match.groups()
    month = int(month_text)
    if month < 1 or month > 12:
        raise KrewManifestError(f"release month must be between 1 and 12: {month}")
    return f"v{year_text}.{month_text}.{patch_text}"


def archive_name(release_tag: str, target: Target) -> str:
    normalize_krew_version(release_tag)
    return f"kubectl-ciscovk_{release_tag}_{target.key}.tar.gz"


def sbom_name(release_tag: str, target: Target) -> str:
    normalize_krew_version(release_tag)
    return f"kubectl-ciscovk_{release_tag}_{target.key}.sbom.cdx.json"


def read_release_checksums(
    checksum_path: pathlib.Path, release_tag: str
) -> dict[Target, str]:
    """Read the exact archive-only or archive+SBOM release checksum contract."""
    checksums: dict[str, str] = {}
    ordered_names: list[str] = []
    try:
        lines = checksum_path.read_text(encoding="utf-8").splitlines()
    except OSError as error:
        raise KrewManifestError(f"cannot read checksum file: {checksum_path}") from error
    for line_number, line in enumerate(lines, 1):
        match = CHECKSUM_RE.fullmatch(line)
        if not match:
            raise KrewManifestError(
                f"invalid checksum line {line_number}: {line!r}"
            )
        digest, name = match.groups()
        if name in checksums:
            raise KrewManifestError(f"duplicate checksum entry: {name}")
        checksums[name] = digest
        ordered_names.append(name)

    if ordered_names != sorted(ordered_names):
        raise KrewManifestError("checksum entries must be sorted by artifact name")

    archive_names = {archive_name(release_tag, target) for target in TARGETS}
    sbom_names = {sbom_name(release_tag, target) for target in TARGETS}
    actual_names = set(checksums)
    allowed_sets = (archive_names, archive_names | sbom_names)
    if actual_names not in allowed_sets:
        missing = sorted(archive_names - actual_names)
        unexpected = sorted(actual_names - archive_names - sbom_names)
        partial_sboms = sorted((actual_names & sbom_names))
        details = []
        if missing:
            details.append(f"missing archives: {', '.join(missing)}")
        if unexpected:
            details.append(f"unexpected entries: {', '.join(unexpected)}")
        if partial_sboms and actual_names != archive_names | sbom_names:
            details.append("SBOM checksum entries must be all present or all absent")
        raise KrewManifestError(
            "checksum file does not match the release contract"
            + (f" ({'; '.join(details)})" if details else "")
        )

    return {
        target: checksums[archive_name(release_tag, target)] for target in TARGETS
    }


def verify_archives(
    archive_dir: pathlib.Path,
    release_tag: str,
    expected_checksums: dict[Target, str],
) -> None:
    """Verify the four downloaded archives against the signed checksum data."""
    if not archive_dir.is_dir():
        raise KrewManifestError(f"archive directory is missing: {archive_dir}")
    expected_names = {archive_name(release_tag, target) for target in TARGETS}
    actual_paths = sorted(archive_dir.iterdir(), key=lambda path: path.name)
    if any(not path.is_file() for path in actual_paths):
        raise KrewManifestError("archive directory may contain regular files only")
    if {path.name for path in actual_paths} != expected_names:
        raise KrewManifestError(
            "archive directory must contain exactly the four Krew release archives"
        )
    for target in TARGETS:
        path = archive_dir / archive_name(release_tag, target)
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if digest != expected_checksums[target]:
            raise KrewManifestError(f"archive checksum mismatch: {path.name}")


def render_manifest(
    template_path: pathlib.Path,
    release_tag: str,
    checksums: dict[Target, str],
) -> str:
    """Render the canonical four-platform manifest."""
    values = {"KREW_VERSION": normalize_krew_version(release_tag)}
    for target in TARGETS:
        name = archive_name(release_tag, target)
        values[f"{target.token_prefix}_URI"] = (
            f"{RELEASE_BASE_URL}/{release_tag}/{name}"
        )
        values[f"{target.token_prefix}_SHA256"] = checksums[target]
    try:
        template_text = template_path.read_text(encoding="utf-8")
        rendered = string.Template(template_text).substitute(values)
    except (KeyError, ValueError) as error:
        raise KrewManifestError(f"invalid or unresolved manifest template: {error}") from error
    except OSError as error:
        raise KrewManifestError(f"cannot read manifest template: {template_path}") from error

    if UNRESOLVED_TOKEN_RE.search(rendered):
        raise KrewManifestError("rendered manifest contains unresolved template tokens")
    if "metadata:\n  name: cisco-vk\n" not in rendered:
        raise KrewManifestError("rendered manifest must define metadata.name cisco-vk")
    if rendered.count("    bin: kubectl-ciscovk\n") != len(TARGETS):
        raise KrewManifestError("rendered manifest must define exactly four plugin binaries")
    if "windows" in rendered.lower():
        raise KrewManifestError("Windows must remain absent until native release gates exist")
    return rendered if rendered.endswith("\n") else rendered + "\n"


def write_manifest(output_path: pathlib.Path, rendered: str) -> None:
    if output_path.name != "cisco-vk.yaml":
        raise KrewManifestError("Krew manifest output filename must be cisco-vk.yaml")
    if output_path.is_symlink():
        raise KrewManifestError("refusing to overwrite a symlinked manifest output")
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(rendered, encoding="utf-8", newline="\n")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-tag", required=True)
    parser.add_argument("--checksums", required=True, type=pathlib.Path)
    parser.add_argument("--template", type=pathlib.Path, default=DEFAULT_TEMPLATE)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument(
        "--archives-dir",
        type=pathlib.Path,
        help="also verify the exact four downloaded archives before rendering",
    )
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        checksums = read_release_checksums(args.checksums, args.release_tag)
        if args.archives_dir is not None:
            verify_archives(args.archives_dir, args.release_tag, checksums)
        rendered = render_manifest(args.template, args.release_tag, checksums)
        write_manifest(args.output, rendered)
    except KrewManifestError as error:
        parser.exit(1, f"error: {error}\n")
    print(f"rendered Krew manifest for {args.release_tag} at {args.output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
