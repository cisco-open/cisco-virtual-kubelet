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
"""Build and verify reproducible kubectl-ciscovk release archives."""

from __future__ import annotations

import argparse
import datetime
import gzip
import hashlib
import io
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
import tarfile
import tempfile
from dataclasses import dataclass
from typing import Optional, Sequence


VERSION_RE = re.compile(r"^v[A-Za-z0-9][A-Za-z0-9._+-]*$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
CHECKSUM_RE = re.compile(r"^([0-9a-f]{64})  ([A-Za-z0-9._+-]+)$")


class PackagingError(RuntimeError):
    """The requested release artifact does not satisfy the package contract."""


@dataclass(frozen=True, order=True)
class Target:
    os: str
    arch: str

    @property
    def key(self) -> str:
        return f"{self.os}_{self.arch}"


TARGETS = (
    Target("darwin", "amd64"),
    Target("darwin", "arm64"),
    Target("linux", "amd64"),
    Target("linux", "arm64"),
)


def validate_version(version: str) -> str:
    if not VERSION_RE.fullmatch(version):
        raise PackagingError(f"invalid release version: {version!r}")
    return version


def validate_commit(commit: str) -> str:
    if not COMMIT_RE.fullmatch(commit):
        raise PackagingError(f"commit must be a full lowercase SHA-1: {commit!r}")
    return commit


def validate_epoch(value: int) -> int:
    # POSIX gzip stores a uint32 timestamp. Keeping that same bound for the tar
    # metadata makes the archive contract unambiguous on every host.
    if value < 0 or value > 0xFFFFFFFF:
        raise PackagingError(f"SOURCE_DATE_EPOCH is outside the uint32 range: {value}")
    return value


def commit_epoch(repo_root: pathlib.Path, commit: str) -> int:
    result = subprocess.run(
        ["git", "-C", str(repo_root), "show", "-s", "--format=%ct", commit],
        check=True,
        capture_output=True,
        text=True,
    )
    try:
        return validate_epoch(int(result.stdout.strip()))
    except ValueError as error:
        raise PackagingError("git returned a non-numeric commit timestamp") from error


def release_epoch(
    repo_root: pathlib.Path, commit: str, supplied_epoch: Optional[int]
) -> int:
    expected_epoch = commit_epoch(repo_root, validate_commit(commit))
    if supplied_epoch is not None:
        supplied_epoch = validate_epoch(supplied_epoch)
        if supplied_epoch != expected_epoch:
            raise PackagingError(
                "SOURCE_DATE_EPOCH does not match the release commit timestamp"
            )
    return expected_epoch


def verify_checkout(repo_root: pathlib.Path, commit: str) -> None:
    result = subprocess.run(
        ["git", "-C", str(repo_root), "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
    )
    if result.stdout.strip() != validate_commit(commit):
        raise PackagingError(
            f"checkout HEAD {result.stdout.strip()!r} does not match release commit"
        )


def build_time(epoch: int) -> str:
    timestamp = datetime.datetime.fromtimestamp(
        validate_epoch(epoch), datetime.timezone.utc
    )
    return timestamp.strftime("%Y-%m-%dT%H:%M:%SZ")


def archive_name(version: str, target: Target) -> str:
    return f"kubectl-ciscovk_{validate_version(version)}_{target.key}.tar.gz"


def sbom_name(version: str, target: Target) -> str:
    return f"kubectl-ciscovk_{validate_version(version)}_{target.key}.sbom.cdx.json"


def checksums_name(version: str) -> str:
    return f"kubectl-ciscovk_{validate_version(version)}_checksums.txt"


def checksum_signature_name(version: str) -> str:
    return f"{checksums_name(version)}.sigstore.json"


def provenance_name(version: str) -> str:
    return f"kubectl-ciscovk_{validate_version(version)}_provenance.sigstore.json"


def archive_signature_name(version: str, target: Target) -> str:
    return f"{archive_name(version, target)}.sigstore.json"


def prepare_directory(path: pathlib.Path) -> None:
    if path.exists():
        if not path.is_dir():
            raise PackagingError(f"output path is not a directory: {path}")
        if any(path.iterdir()):
            raise PackagingError(f"refusing to reuse non-empty directory: {path}")
        return
    path.mkdir(parents=True)


def verify_output_directories(dist_dir: pathlib.Path, build_dir: pathlib.Path) -> None:
    if (
        dist_dir == build_dir
        or dist_dir in build_dir.parents
        or build_dir in dist_dir.parents
    ):
        raise PackagingError("dist and build directories must not overlap")


def go_build_command(
    go_binary: str,
    output: pathlib.Path,
    version: str,
    commit: str,
    timestamp: str,
) -> list[str]:
    ldflags = " ".join(
        (
            "-s",
            "-w",
            "-buildid=",
            "-X",
            f"main.Version={validate_version(version)}",
            "-X",
            f"main.GitCommit={validate_commit(commit)}",
            "-X",
            f"main.BuildTime={timestamp}",
        )
    )
    return [
        go_binary,
        "build",
        "-mod=readonly",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags",
        ldflags,
        "-o",
        str(output),
        "./tools/kubectl-ciscovk",
    ]


def inspect_binary(go_binary: str, binary: pathlib.Path, target: Target) -> None:
    result = subprocess.run(
        [go_binary, "version", "-m", str(binary)],
        check=True,
        capture_output=True,
        text=True,
    )
    expected = (
        "\tbuild\t-trimpath=true",
        "\tbuild\tCGO_ENABLED=0",
        f"\tbuild\tGOOS={target.os}",
        f"\tbuild\tGOARCH={target.arch}",
    )
    missing = [line for line in expected if line not in result.stdout]
    if missing:
        raise PackagingError(
            f"{binary} is missing required Go build settings: {', '.join(missing)}"
        )
    if "\tbuild\tvcs=" in result.stdout:
        raise PackagingError(f"{binary} unexpectedly contains VCS build metadata")


def build_target(
    repo_root: pathlib.Path,
    build_dir: pathlib.Path,
    target: Target,
    version: str,
    commit: str,
    timestamp: str,
    go_binary: str,
) -> pathlib.Path:
    target_dir = build_dir / target.key
    target_dir.mkdir(parents=True, exist_ok=True)
    binary = target_dir / "kubectl-ciscovk"
    environment = os.environ.copy()
    environment.update(
        {
            "CGO_ENABLED": "0",
            "GOOS": target.os,
            "GOARCH": target.arch,
            "GOENV": "off",
            "GOFLAGS": "",
            "GOTOOLCHAIN": "local",
            "GOWORK": "off",
            "LC_ALL": "C",
            "TZ": "UTC",
        }
    )
    subprocess.run(
        go_build_command(go_binary, binary, version, commit, timestamp),
        cwd=repo_root,
        env=environment,
        check=True,
    )
    binary.chmod(0o755)
    inspect_binary(go_binary, binary, target)
    return binary


def _tar_member(
    name: str, content: bytes, mode: int, epoch: int
) -> tuple[tarfile.TarInfo, io.BytesIO]:
    member = tarfile.TarInfo(name=name)
    member.size = len(content)
    member.mode = mode
    member.uid = 0
    member.gid = 0
    member.uname = ""
    member.gname = ""
    member.mtime = validate_epoch(epoch)
    member.type = tarfile.REGTYPE
    return member, io.BytesIO(content)


def create_archive(
    output: pathlib.Path,
    binary: pathlib.Path,
    license_path: pathlib.Path,
    go_license_dir: pathlib.Path,
    epoch: int,
) -> None:
    if not binary.is_file():
        raise PackagingError(f"plugin binary is missing: {binary}")
    if not license_path.is_file():
        raise PackagingError(f"LICENSE is missing: {license_path}")
    go_license = go_license_dir / "LICENSE"
    go_patents = go_license_dir / "PATENTS"
    if not go_license.is_file() or not go_patents.is_file():
        raise PackagingError(f"Go LICENSE/PATENTS are missing: {go_license_dir}")
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as raw_output:
        with gzip.GzipFile(
            filename="",
            mode="wb",
            compresslevel=9,
            fileobj=raw_output,
            mtime=validate_epoch(epoch),
        ) as compressed:
            with tarfile.open(
                mode="w|", fileobj=compressed, format=tarfile.USTAR_FORMAT
            ) as archive:
                for name, path, mode in (
                    ("LICENSE", license_path, 0o644),
                    ("THIRD_PARTY_LICENSES/go/LICENSE", go_license, 0o644),
                    ("THIRD_PARTY_LICENSES/go/PATENTS", go_patents, 0o644),
                    ("kubectl-ciscovk", binary, 0o755),
                ):
                    member, content = _tar_member(name, path.read_bytes(), mode, epoch)
                    archive.addfile(member, content)
    output.chmod(0o644)


def expected_artifacts(
    dist_dir: pathlib.Path, version: str, require_sboms: bool
) -> list[pathlib.Path]:
    artifacts = [dist_dir / archive_name(version, target) for target in TARGETS]
    if require_sboms:
        artifacts.extend(dist_dir / sbom_name(version, target) for target in TARGETS)
    return sorted(artifacts, key=lambda path: path.name)


def expected_release_assets(dist_dir: pathlib.Path, version: str) -> list[pathlib.Path]:
    assets = expected_artifacts(dist_dir, version, require_sboms=True)
    assets.extend(
        dist_dir / archive_signature_name(version, target) for target in TARGETS
    )
    assets.extend(
        (
            dist_dir / checksums_name(version),
            dist_dir / checksum_signature_name(version),
            dist_dir / provenance_name(version),
        )
    )
    return sorted(assets, key=lambda path: path.name)


def verify_sbom(path: pathlib.Path) -> None:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise PackagingError(f"invalid JSON SBOM: {path.name}") from error
    if not isinstance(document, dict):
        raise PackagingError(f"SBOM is not a JSON object: {path.name}")
    if document.get("bomFormat") != "CycloneDX":
        raise PackagingError(f"SBOM is not CycloneDX: {path.name}")
    if not isinstance(document.get("specVersion"), str):
        raise PackagingError(f"SBOM has no CycloneDX specVersion: {path.name}")
    metadata = document.get("metadata")
    component = metadata.get("component") if isinstance(metadata, dict) else None
    if not isinstance(component, dict) or not isinstance(component.get("name"), str):
        raise PackagingError(f"SBOM has no metadata component: {path.name}")
    if not component["name"].strip():
        raise PackagingError(f"SBOM metadata component has no name: {path.name}")


def write_checksums(
    dist_dir: pathlib.Path, version: str, require_sboms: bool
) -> pathlib.Path:
    artifacts = expected_artifacts(dist_dir, version, require_sboms)
    missing = [str(path) for path in artifacts if not path.is_file()]
    if missing:
        raise PackagingError(f"cannot checksum missing artifacts: {', '.join(missing)}")
    output = dist_dir / checksums_name(version)
    lines = [
        f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
        for path in artifacts
    ]
    with output.open("w", encoding="utf-8", newline="\n") as stream:
        stream.write("".join(lines))
    output.chmod(0o644)
    return output


def read_checksums(path: pathlib.Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line_number, line in enumerate(
        path.read_text(encoding="utf-8").splitlines(), 1
    ):
        match = CHECKSUM_RE.fullmatch(line)
        if not match:
            raise PackagingError(f"invalid checksum line {line_number}: {line!r}")
        digest, name = match.groups()
        if name in checksums:
            raise PackagingError(f"duplicate checksum entry: {name}")
        checksums[name] = digest
    return checksums


def verify_checksums(dist_dir: pathlib.Path, version: str, require_sboms: bool) -> None:
    checksum_path = dist_dir / checksums_name(version)
    if not checksum_path.is_file():
        raise PackagingError(f"checksum file is missing: {checksum_path}")
    actual = read_checksums(checksum_path)
    expected_paths = expected_artifacts(dist_dir, version, require_sboms)
    expected_names = [path.name for path in expected_paths]
    if list(actual) != expected_names:
        raise PackagingError(
            "checksum entries do not exactly match the sorted release artifact set"
        )
    for artifact in expected_paths:
        digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
        if actual[artifact.name] != digest:
            raise PackagingError(f"checksum mismatch: {artifact.name}")
        if artifact.name.endswith(".sbom.cdx.json"):
            verify_sbom(artifact)


def verify_release_assets(dist_dir: pathlib.Path, version: str) -> None:
    expected = expected_release_assets(dist_dir, version)
    actual = sorted(dist_dir.iterdir(), key=lambda path: path.name)
    if [path.name for path in actual] != [path.name for path in expected]:
        raise PackagingError(
            "release directory does not match the exact 15-file allowlist"
        )
    for path in actual:
        if not path.is_file() or path.stat().st_size == 0:
            raise PackagingError(f"release asset is missing or empty: {path.name}")
    verify_checksums(dist_dir, version, require_sboms=True)


def verify_archive(
    archive_path: pathlib.Path,
    license_path: pathlib.Path,
    go_license_dir: pathlib.Path,
    epoch: int,
) -> bytes:
    header = archive_path.read_bytes()[:10]
    expected_header = (
        b"\x1f\x8b\x08\x00"
        + validate_epoch(epoch).to_bytes(4, byteorder="little")
        + b"\x02\xff"
    )
    if header != expected_header:
        raise PackagingError(f"gzip header is not normalized: {archive_path.name}")
    with tarfile.open(archive_path, mode="r:gz") as archive:
        members = archive.getmembers()
        expected_names = [
            "LICENSE",
            "THIRD_PARTY_LICENSES/go/LICENSE",
            "THIRD_PARTY_LICENSES/go/PATENTS",
            "kubectl-ciscovk",
        ]
        if [member.name for member in members] != expected_names:
            raise PackagingError(
                f"{archive_path.name} does not match the license/binary allowlist"
            )
        for member, expected_mode in zip(members, (0o644, 0o644, 0o644, 0o755)):
            if not member.isfile():
                raise PackagingError(
                    f"archive member is not a regular file: {member.name}"
                )
            if member.mode != expected_mode:
                raise PackagingError(
                    f"archive mode for {member.name} is {oct(member.mode)}, "
                    f"expected {oct(expected_mode)}"
                )
            if (member.uid, member.gid, member.uname, member.gname) != (0, 0, "", ""):
                raise PackagingError(
                    f"archive ownership is not normalized: {member.name}"
                )
            if member.mtime != validate_epoch(epoch):
                raise PackagingError(f"archive mtime is not normalized: {member.name}")
        archived_license = archive.extractfile(members[0])
        archived_go_license = archive.extractfile(members[1])
        archived_go_patents = archive.extractfile(members[2])
        archived_binary = archive.extractfile(members[3])
        if None in (
            archived_license,
            archived_go_license,
            archived_go_patents,
            archived_binary,
        ):
            raise PackagingError(f"failed to read archive members from {archive_path}")
        assert archived_license is not None
        assert archived_go_license is not None
        assert archived_go_patents is not None
        assert archived_binary is not None
        if archived_license.read() != license_path.read_bytes():
            raise PackagingError(f"LICENSE differs in {archive_path.name}")
        if archived_go_license.read() != (go_license_dir / "LICENSE").read_bytes():
            raise PackagingError(f"Go LICENSE differs in {archive_path.name}")
        if archived_go_patents.read() != (go_license_dir / "PATENTS").read_bytes():
            raise PackagingError(f"Go PATENTS differs in {archive_path.name}")
        return archived_binary.read()


def native_target() -> Optional[Target]:
    os_name = {"darwin": "darwin", "linux": "linux"}.get(platform.system().lower())
    arch_name = {
        "amd64": "amd64",
        "x86_64": "amd64",
        "arm64": "arm64",
        "aarch64": "arm64",
    }.get(platform.machine().lower())
    if os_name is None or arch_name is None:
        return None
    target = Target(os_name, arch_name)
    return target if target in TARGETS else None


def execute_archived_binary(
    archive_path: pathlib.Path,
    license_path: pathlib.Path,
    go_license_dir: pathlib.Path,
    epoch: int,
    version: str,
    commit: str,
) -> None:
    binary_content = verify_archive(
        archive_path, license_path, go_license_dir, epoch
    )
    with tempfile.TemporaryDirectory(prefix="kubectl-ciscovk-verify-") as temp_dir:
        binary = pathlib.Path(temp_dir) / "kubectl-ciscovk"
        binary.write_bytes(binary_content)
        binary.chmod(0o755)
        result = subprocess.run(
            [str(binary), "version"],
            check=True,
            capture_output=True,
            text=True,
        )
    expected = (
        f"kubectl-ciscovk {validate_version(version)} "
        f"(commit={validate_commit(commit)}, built={build_time(epoch)})\n"
    )
    if result.stdout != expected or result.stderr:
        raise PackagingError(
            f"unexpected version output: stdout={result.stdout!r}, stderr={result.stderr!r}"
        )


def package_release(args: argparse.Namespace) -> None:
    repo_root = args.repo_root.resolve()
    version = validate_version(args.version)
    commit = validate_commit(args.commit)
    verify_checkout(repo_root, commit)
    epoch = release_epoch(repo_root, commit, args.source_date_epoch)
    dist_dir = args.dist_dir.resolve()
    build_dir = args.build_dir.resolve()
    verify_output_directories(dist_dir, build_dir)
    prepare_directory(dist_dir)
    prepare_directory(build_dir)
    timestamp = build_time(epoch)
    license_path = repo_root / "LICENSE"
    go_license_dir = repo_root / "third_party" / "go"

    for target in TARGETS:
        binary = build_target(
            repo_root,
            build_dir,
            target,
            version,
            commit,
            timestamp,
            args.go_binary,
        )
        archive_path = dist_dir / archive_name(version, target)
        create_archive(archive_path, binary, license_path, go_license_dir, epoch)
        verify_archive(archive_path, license_path, go_license_dir, epoch)

    host_target = native_target()
    if host_target is not None:
        execute_archived_binary(
            dist_dir / archive_name(version, host_target),
            license_path,
            go_license_dir,
            epoch,
            version,
            commit,
        )

    print(f"built {len(TARGETS)} reproducible plugin archives in {dist_dir}")


def generate_checksums(args: argparse.Namespace) -> None:
    output = write_checksums(
        args.dist_dir.resolve(),
        validate_version(args.version),
        args.require_sboms,
    )
    verify_checksums(
        args.dist_dir.resolve(),
        validate_version(args.version),
        args.require_sboms,
    )
    print(f"wrote {output}")


def verify_release(args: argparse.Namespace) -> None:
    version = validate_version(args.version)
    commit = validate_commit(args.commit)
    repo_root = args.repo_root.resolve()
    verify_checkout(repo_root, commit)
    epoch = release_epoch(repo_root, commit, args.source_date_epoch)
    target = Target(args.target_os, args.target_arch)
    if target not in TARGETS:
        raise PackagingError(f"unsupported target: {target.key}")
    dist_dir = args.dist_dir.resolve()
    license_path = repo_root / "LICENSE"
    go_license_dir = repo_root / "third_party" / "go"
    verify_checksums(dist_dir, version, args.require_sboms)
    archive_path = dist_dir / archive_name(version, target)
    verify_archive(archive_path, license_path, go_license_dir, epoch)
    if args.execute:
        if native_target() != target:
            raise PackagingError(
                f"cannot execute {target.key} archive on {native_target() or 'this host'}"
            )
        execute_archived_binary(
            archive_path, license_path, go_license_dir, epoch, version, commit
        )
    print(f"verified {archive_path}")


def verify_release_directory(args: argparse.Namespace) -> None:
    verify_release_assets(args.dist_dir.resolve(), validate_version(args.version))
    print(f"verified exact release asset set in {args.dist_dir.resolve()}")


def parser() -> argparse.ArgumentParser:
    root = pathlib.Path(__file__).resolve().parents[2]
    command_parser = argparse.ArgumentParser(description=__doc__)
    subparsers = command_parser.add_subparsers(dest="command", required=True)

    package_parser = subparsers.add_parser("package", help="build release archives")
    package_parser.add_argument("--version", required=True)
    package_parser.add_argument("--commit", required=True)
    package_parser.add_argument("--source-date-epoch", type=int)
    package_parser.add_argument("--repo-root", type=pathlib.Path, default=root)
    package_parser.add_argument(
        "--dist-dir", type=pathlib.Path, default=root / "dist" / "kubectl-ciscovk"
    )
    package_parser.add_argument(
        "--build-dir", type=pathlib.Path, default=root / "build" / "kubectl-ciscovk"
    )
    package_parser.add_argument("--go-binary", default="go")
    package_parser.set_defaults(handler=package_release)

    checksums_parser = subparsers.add_parser(
        "checksums", help="write and verify the release checksum manifest"
    )
    checksums_parser.add_argument("--version", required=True)
    checksums_parser.add_argument("--dist-dir", type=pathlib.Path, required=True)
    checksums_parser.add_argument("--require-sboms", action="store_true")
    checksums_parser.set_defaults(handler=generate_checksums)

    verify_parser = subparsers.add_parser(
        "verify", help="verify one exact archive and the release checksum manifest"
    )
    verify_parser.add_argument("--version", required=True)
    verify_parser.add_argument("--commit", required=True)
    verify_parser.add_argument("--source-date-epoch", type=int, required=True)
    verify_parser.add_argument(
        "--target-os", choices=("darwin", "linux"), required=True
    )
    verify_parser.add_argument(
        "--target-arch", choices=("amd64", "arm64"), required=True
    )
    verify_parser.add_argument("--repo-root", type=pathlib.Path, default=root)
    verify_parser.add_argument("--dist-dir", type=pathlib.Path, required=True)
    verify_parser.add_argument("--require-sboms", action="store_true")
    verify_parser.add_argument("--execute", action="store_true")
    verify_parser.set_defaults(handler=verify_release)

    assets_parser = subparsers.add_parser(
        "verify-assets", help="verify the final signed release asset allowlist"
    )
    assets_parser.add_argument("--version", required=True)
    assets_parser.add_argument("--dist-dir", type=pathlib.Path, required=True)
    assets_parser.set_defaults(handler=verify_release_directory)
    return command_parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    try:
        args = parser().parse_args(argv)
        args.handler(args)
    except (OSError, PackagingError, subprocess.CalledProcessError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
